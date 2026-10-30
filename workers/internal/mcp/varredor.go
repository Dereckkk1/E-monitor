package mcp

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/mcp/auditoria"
)

// Varredor faz a manutenção periódica dos pedidos críticos (plano Fatia 4):
//   - expira `pendente` cujo prazo passou;
//   - fecha como `falhou` a `aprovada` presa (o processo caiu entre aprovar e
//     fechar: ninguém mais a moverá);
//   - apaga os tokens de confirmação já vencidos (mcp_confirmacoes_usadas).
//
// Roda em toda réplica da API; o advisory lock de transação garante uma só
// varredura por vez.
const (
	intervaloVarredor  = time.Minute
	presaAprovadaApos  = 10 * time.Minute // uma execução normal leva segundos
	folgaPoda          = time.Hour        // mesma folga da poda oportunista de Confirmacoes.Consumir
	loteTransicao      = 500
	lotePodaVarredor   = 5000
	timeoutVarredura   = 30 * time.Second
	pelaVarredura      = "varredor-mcp"
	acaoPendenteFalhou = "mcp.pendente.falhou"
)

// chaveLockVarredor é o argumento de pg_try_advisory_xact_lock: hash FNV-64a
// de um nome próprio (mesmo padrão do assertiveness e do campaignalerts), para
// não colidir com o lock de nenhum outro job.
var chaveLockVarredor = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("radiocheck:mcp-varredor"))
	return int64(h.Sum64())
}()

type Varredor struct {
	DB        *pgxpool.Pool
	Agora     func() time.Time
	Intervalo time.Duration
	Log       *zap.Logger
}

func (v *Varredor) agora() time.Time {
	if v.Agora != nil {
		return v.Agora()
	}
	return time.Now()
}

func (v *Varredor) log() *zap.Logger {
	if v.Log != nil {
		return v.Log
	}
	return zap.NewNop()
}

// Run bloqueia até ctx cancelar: uma passada na subida e outra a cada Intervalo.
func (v *Varredor) Run(ctx context.Context) {
	iv := v.Intervalo
	if iv <= 0 {
		iv = intervaloVarredor
	}
	v.log().Info("mcp: varredor iniciado", zap.Duration("intervalo", iv))
	v.passada(ctx)
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			v.log().Info("mcp: varredor parado")
			return
		case <-t.C:
			v.passada(ctx)
		}
	}
}

func (v *Varredor) passada(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, timeoutVarredura)
	defer cancel()
	v.tick(pctx)
}

// tick faz uma varredura. Cada passo roda num savepoint: o erro de um é logado
// e desfeito sem derrubar a transação que segura o lock nem os outros passos.
func (v *Varredor) tick(ctx context.Context) {
	tx, err := v.DB.Begin(ctx)
	if err != nil {
		v.log().Warn("mcp: varredor não abriu transação", zap.Error(err))
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var livre bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, chaveLockVarredor).Scan(&livre); err != nil {
		v.log().Warn("mcp: varredor sem lock", zap.Error(err))
		return
	}
	if !livre {
		return // outra réplica varre agora
	}
	agora := v.agora()
	passos := []struct {
		nome string
		fn   func(context.Context, pgx.Tx, time.Time) (int, error)
	}{
		{"expirar", expirarVencidas},
		{"fechar_presas", fecharPresas},
		{"podar_confirmacoes", podarConfirmacoes},
	}
	for _, p := range passos {
		sp, err := tx.Begin(ctx)
		if err != nil {
			// Não perde o que os passos anteriores já fizeram: segue para o
			// próximo passo e o commit final grava o que deu certo.
			v.log().Warn("mcp: varredor não abriu savepoint", zap.String("passo", p.nome), zap.Error(err))
			continue
		}
		n, err := p.fn(ctx, sp, agora)
		if err != nil {
			v.log().Warn("mcp: passo do varredor falhou", zap.String("passo", p.nome), zap.Error(err))
			_ = sp.Rollback(context.WithoutCancel(ctx))
			continue
		}
		if err := sp.Commit(ctx); err != nil {
			v.log().Warn("mcp: passo do varredor não fechou", zap.String("passo", p.nome), zap.Error(err))
			continue
		}
		if n > 0 {
			v.log().Info("mcp: varredor", zap.String("passo", p.nome), zap.Int("linhas", n))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		v.log().Warn("mcp: varredor não fechou a transação", zap.Error(err))
	}
}

func expirarVencidas(ctx context.Context, tx pgx.Tx, agora time.Time) (int, error) {
	tag, err := tx.Exec(ctx, `UPDATE mcp_acoes_pendentes SET estado = 'expirada'
		WHERE id IN (SELECT id FROM mcp_acoes_pendentes
			WHERE estado = 'pendente' AND expira_em <= $1
			ORDER BY expira_em LIMIT $2 FOR UPDATE SKIP LOCKED)
		AND estado = 'pendente'`, agora, loteTransicao)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func fecharPresas(ctx context.Context, tx pgx.Tx, agora time.Time) (int, error) {
	rows, err := tx.Query(ctx, `UPDATE mcp_acoes_pendentes SET estado = 'falhou', erro = $3
		WHERE id IN (SELECT id FROM mcp_acoes_pendentes
			WHERE estado = 'aprovada' AND aprovada_em <= $1
			ORDER BY aprovada_em LIMIT $2 FOR UPDATE SKIP LOCKED)
		AND estado = 'aprovada'
		RETURNING id, tool`, agora.Add(-presaAprovadaApos), loteTransicao, msgInterrompida)
	if err != nil {
		return 0, err
	}
	type fechada struct {
		id   uuid.UUID
		tool string
	}
	var fechadas []fechada
	for rows.Next() {
		var f fechada
		if err := rows.Scan(&f.id, &f.tool); err != nil {
			rows.Close()
			return 0, err
		}
		fechadas = append(fechadas, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, f := range fechadas {
		id := f.id
		if err := auditoria.RegistrarSistema(ctx, tx, pelaVarredura, acaoPendenteFalhou, "mcp_pendente", &id,
			map[string]any{"codigo": "presa", "tool": f.tool,
				"mcp": map[string]any{"pendente_id": id.String()}}); err != nil {
			return 0, err
		}
	}
	return len(fechadas), nil
}

func podarConfirmacoes(ctx context.Context, tx pgx.Tx, agora time.Time) (int, error) {
	tag, err := tx.Exec(ctx, `DELETE FROM mcp_confirmacoes_usadas WHERE hash IN (
		SELECT hash FROM mcp_confirmacoes_usadas WHERE expira_em < $1
		ORDER BY expira_em LIMIT $2 FOR UPDATE SKIP LOCKED)`, agora.Add(-folgaPoda), lotePodaVarredor)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
