package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store de pedidos `critico` (spec §5.2, plano Fatia 4 F4-2/F4-3). Transplante
// de hub backend/src/mcp/write/pendentes.ts para Postgres: toda transição é um
// UPDATE … WHERE estado = … (dois cliques, um vence) e todo SQL filtra pelo
// dono — "não existe" e "não é seu" são o mesmo erro.

const (
	ttlPendente       = 24 * time.Hour
	folgaAposFalha    = time.Hour
	tetoErroPendente  = 2000
	tetoListaPendente = 50
)

var ErrPendenteNaoAchada = errors.New("mcp: pedido não encontrado")

// ErrEstadoPendente: o pedido existe e é do dono, mas não está em `pendente`.
type ErrEstadoPendente struct{ Estado string }

func (e ErrEstadoPendente) Error() string { return "mcp: pedido em estado " + e.Estado }

type Pendente struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	TokenID     *uuid.UUID
	Tool        string
	Args        json.RawMessage
	Motivo      string
	Preview     previa
	Estado      string
	Tentativa   int
	CriadaEm    time.Time
	ExpiraEm    time.Time
	AprovadaEm  *time.Time
	ExecutadaEm *time.Time
	RecusadaEm  *time.Time
	Resultado   json.RawMessage
	Erro        *string
}

type NovaPendente struct {
	UserID  uuid.UUID
	TokenID uuid.UUID
	Tool    string
	Args    []byte // canônicos (argsCanonicos), sem "confirmacao"
	Motivo  string
	Preview previa
}

type Pendentes struct{ DB *pgxpool.Pool }

func hashDosArgs(tool string, args []byte) []byte {
	h := sha256.New()
	h.Write([]byte(tool))
	h.Write([]byte{0})
	h.Write(args)
	return h.Sum(nil)
}

const colunasPendente = `id, user_id, token_id, tool, args, motivo, preview, estado, tentativa,
	criada_em, expira_em, aprovada_em, executada_em, recusada_em, resultado, erro`

func lerPendente(row pgx.Row) (Pendente, error) {
	var p Pendente
	var args, preview, resultado []byte
	if err := row.Scan(&p.ID, &p.UserID, &p.TokenID, &p.Tool, &args, &p.Motivo, &preview, &p.Estado,
		&p.Tentativa, &p.CriadaEm, &p.ExpiraEm, &p.AprovadaEm, &p.ExecutadaEm, &p.RecusadaEm,
		&resultado, &p.Erro); err != nil {
		return Pendente{}, err
	}
	p.Args = args
	if len(resultado) > 0 {
		p.Resultado = resultado
	}
	if err := json.Unmarshal(preview, &p.Preview); err != nil {
		return Pendente{}, err
	}
	return p, nil
}

// cortarErro limita o texto a tetoErroPendente runes (o CHECK do banco conta caracteres).
func cortarErro(s string) string {
	r := []rune(s)
	if len(r) <= tetoErroPendente {
		return s
	}
	return string(r[:tetoErroPendente])
}

// Criar grava o pedido ou devolve o aberto igual (F4-3). Numa transação: expira
// o vencido daquele hash, tenta inserir, e no conflito lê o aberto.
func (s *Pendentes) Criar(ctx context.Context, n NovaPendente, agora time.Time) (Pendente, bool, error) {
	hash := hashDosArgs(n.Tool, n.Args)
	pv, err := json.Marshal(n.Preview)
	if err != nil {
		return Pendente{}, false, err
	}
	var tok *uuid.UUID
	if n.TokenID != uuid.Nil {
		tok = &n.TokenID
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Pendente{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE mcp_acoes_pendentes SET estado = 'expirada'
		WHERE user_id = $1 AND args_hash = $2 AND estado = 'pendente' AND expira_em <= $3`,
		n.UserID, hash, agora); err != nil {
		return Pendente{}, false, err
	}
	// Até duas voltas: se o aberto que causou o conflito saiu dos estados
	// abertos entre o INSERT e o SELECT (recusado, executado, expirado), o
	// SELECT não acha nada e o INSERT precisa de outra chance.
	for volta := 0; volta < 2; volta++ {
		p, err := lerPendente(tx.QueryRow(ctx, `INSERT INTO mcp_acoes_pendentes
			(user_id, token_id, tool, args, args_hash, motivo, preview, expira_em, criada_em)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (user_id, args_hash) WHERE estado IN ('pendente', 'aprovada') DO NOTHING
			RETURNING `+colunasPendente,
			n.UserID, tok, n.Tool, n.Args, hash, n.Motivo, pv, agora.Add(ttlPendente), agora))
		if err == nil {
			return p, false, tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Pendente{}, false, err
		}
		p, err = lerPendente(tx.QueryRow(ctx, `SELECT `+colunasPendente+` FROM mcp_acoes_pendentes
			WHERE user_id = $1 AND args_hash = $2 AND estado IN ('pendente', 'aprovada')`, n.UserID, hash))
		if err == nil {
			return p, true, tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Pendente{}, false, err
		}
	}
	return Pendente{}, false, errors.New("mcp: o pedido aberto sumiu duas vezes durante a criação")
}

// diagnostico explica por que um UPDATE condicional não pegou nenhuma linha.
func (s *Pendentes) diagnostico(ctx context.Context, id, dono uuid.UUID, agora time.Time) error {
	var estado string
	var expira time.Time
	err := s.DB.QueryRow(ctx, `SELECT estado, expira_em FROM mcp_acoes_pendentes
		WHERE id = $1 AND user_id = $2`, id, dono).Scan(&estado, &expira)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPendenteNaoAchada
	}
	if err != nil {
		return err
	}
	if estado == "pendente" && !expira.After(agora) {
		if _, err := s.DB.Exec(ctx, `UPDATE mcp_acoes_pendentes SET estado = 'expirada'
			WHERE id = $1 AND estado = 'pendente' AND expira_em <= $2`, id, agora); err != nil {
			return err
		}
		return ErrEstadoPendente{Estado: "expirada"}
	}
	return ErrEstadoPendente{Estado: estado}
}

// Aprovar abre uma tentativa: pendente → aprovada, só para o dono e dentro do prazo.
func (s *Pendentes) Aprovar(ctx context.Context, id, dono uuid.UUID, agora time.Time) (Pendente, error) {
	p, err := lerPendente(s.DB.QueryRow(ctx, `UPDATE mcp_acoes_pendentes
		SET estado = 'aprovada', aprovada_em = $3, tentativa = tentativa + 1, erro = NULL
		WHERE id = $1 AND user_id = $2 AND estado = 'pendente' AND expira_em > $3
		RETURNING `+colunasPendente, id, dono, agora))
	if errors.Is(err, pgx.ErrNoRows) {
		return Pendente{}, s.diagnostico(ctx, id, dono, agora)
	}
	return p, err
}

// Recusar fecha o pedido pendente a pedido do dono.
func (s *Pendentes) Recusar(ctx context.Context, id, dono uuid.UUID, agora time.Time) (Pendente, error) {
	p, err := lerPendente(s.DB.QueryRow(ctx, `UPDATE mcp_acoes_pendentes
		SET estado = 'recusada', recusada_em = $3
		WHERE id = $1 AND user_id = $2 AND estado = 'pendente' AND expira_em > $3
		RETURNING `+colunasPendente, id, dono, agora))
	if errors.Is(err, pgx.ErrNoRows) {
		return Pendente{}, s.diagnostico(ctx, id, dono, agora)
	}
	return p, err
}

// MarcarExecutada fecha a tentativa vigente com sucesso e devolve a linha
// atualizada. Aceita também `falhou` da mesma tentativa (o executor terminou
// depois de ser dado como interrompido). false = ninguém foi atualizado.
func (s *Pendentes) MarcarExecutada(ctx context.Context, id uuid.UUID, tentativa int, resultado json.RawMessage, agora time.Time) (Pendente, bool, error) {
	var res any
	if len(resultado) > 0 {
		res = []byte(resultado)
	}
	p, err := lerPendente(s.DB.QueryRow(ctx, `UPDATE mcp_acoes_pendentes
		SET estado = 'executada', executada_em = $4, resultado = $3, erro = NULL
		WHERE id = $1 AND tentativa = $2 AND estado IN ('aprovada', 'falhou')
		RETURNING `+colunasPendente,
		id, tentativa, res, agora))
	if errors.Is(err, pgx.ErrNoRows) {
		return Pendente{}, false, nil
	}
	if err != nil {
		return Pendente{}, false, err
	}
	return p, true, nil
}

// MarcarFalhaDeNegocio devolve o pedido a `pendente` com o erro, para nova
// tentativa. Aceita `aprovada` e também `falhou` da mesma tentativa (uma
// recusa tardia depois de o varredor dar a execução por interrompida: nada
// rodou). estenderPrazo: o prazo nunca encurta e ganha pelo menos
// folgaAposFalha; sem ele (a ação mudou ou sumiu: não adianta insistir) o
// prazo fica como está.
func (s *Pendentes) MarcarFalhaDeNegocio(ctx context.Context, id uuid.UUID, tentativa int, erro string, agora time.Time, estenderPrazo bool) (bool, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE mcp_acoes_pendentes
		SET estado = 'pendente', erro = $3, aprovada_em = NULL,
		    expira_em = CASE WHEN $5 THEN GREATEST(expira_em, $4::timestamptz) ELSE expira_em END
		WHERE id = $1 AND tentativa = $2 AND estado IN ('aprovada', 'falhou')`,
		id, tentativa, cortarErro(erro), agora.Add(folgaAposFalha), estenderPrazo)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// MarcarInterrompida: exceção na execução — final, sem nova tentativa.
func (s *Pendentes) MarcarInterrompida(ctx context.Context, id uuid.UUID, tentativa int, erro string) (bool, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE mcp_acoes_pendentes SET estado = 'falhou', erro = $3
		WHERE id = $1 AND tentativa = $2 AND estado = 'aprovada'`, id, tentativa, cortarErro(erro))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Obter lê um pedido do dono.
func (s *Pendentes) Obter(ctx context.Context, id, dono uuid.UUID) (Pendente, error) {
	p, err := lerPendente(s.DB.QueryRow(ctx, `SELECT `+colunasPendente+` FROM mcp_acoes_pendentes
		WHERE id = $1 AND user_id = $2`, id, dono))
	if errors.Is(err, pgx.ErrNoRows) {
		return Pendente{}, ErrPendenteNaoAchada
	}
	return p, err
}

// Listar devolve os pedidos do dono, do mais novo ao mais velho, e o total
// (sem paginar). estados nil = todos.
func (s *Pendentes) Listar(ctx context.Context, dono uuid.UUID, estados []string, limit, offset int) ([]Pendente, int, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > tetoListaPendente {
		limit = tetoListaPendente
	}
	if offset < 0 {
		offset = 0
	}
	var filtro any // nil = sem filtro
	if estados != nil {
		filtro = estados
	}
	var total int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM mcp_acoes_pendentes
		WHERE user_id = $1 AND ($2::text[] IS NULL OR estado = ANY($2))`, dono, filtro).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.Query(ctx, `SELECT `+colunasPendente+` FROM mcp_acoes_pendentes
		WHERE user_id = $1 AND ($2::text[] IS NULL OR estado = ANY($2))
		ORDER BY criada_em DESC, id DESC LIMIT $3 OFFSET $4`, dono, filtro, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Pendente{}
	for rows.Next() {
		p, err := lerPendente(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}
