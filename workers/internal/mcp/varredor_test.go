package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func varredorDB(t *testing.T) (context.Context, *pgxpool.Pool, *Varredor, *Pendentes, uuid.UUID) {
	t.Helper()
	ctx, pool, st, dono, _ := pendentesDB(t)
	_, err := pool.Exec(ctx, `TRUNCATE mcp_confirmacoes_usadas`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM audit_log WHERE action = 'mcp.pendente.falhou'`)
	require.NoError(t, err)
	return ctx, pool, &Varredor{DB: pool, Agora: func() time.Time { return t0 }}, st, dono
}

func estadoPend(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (string, *string) {
	t.Helper()
	var e string
	var erro *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT estado, erro FROM mcp_acoes_pendentes WHERE id = $1`, id).Scan(&e, &erro))
	return e, erro
}

func TestVarredor_ExpiraSoOVencido(t *testing.T) {
	ctx, pool, v, st, dono := varredorDB(t)
	velha, _, err := st.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0.Add(-25*time.Hour))
	require.NoError(t, err)
	viva, _, err := st.Criar(ctx, novaDeTeste(dono, `{"id":2}`), t0.Add(-time.Hour))
	require.NoError(t, err)
	v.tick(ctx)
	e, _ := estadoPend(t, ctx, pool, velha.ID)
	require.Equal(t, "expirada", e)
	e, _ = estadoPend(t, ctx, pool, viva.ID)
	require.Equal(t, "pendente", e)
}

func TestVarredor_FechaAprovadaPresa(t *testing.T) {
	ctx, pool, v, st, dono := varredorDB(t)
	presa, _, err := st.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0.Add(-time.Hour))
	require.NoError(t, err)
	recente, _, err := st.Criar(ctx, novaDeTeste(dono, `{"id":2}`), t0.Add(-time.Hour))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE mcp_acoes_pendentes SET estado='aprovada', tentativa=1, aprovada_em=$2 WHERE id=$1`, presa.ID, t0.Add(-11*time.Minute))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE mcp_acoes_pendentes SET estado='aprovada', tentativa=1, aprovada_em=$2 WHERE id=$1`, recente.ID, t0.Add(-9*time.Minute))
	require.NoError(t, err)

	v.tick(ctx)
	e, erro := estadoPend(t, ctx, pool, presa.ID)
	require.Equal(t, "falhou", e)
	require.NotNil(t, erro)
	require.Equal(t, msgInterrompida, *erro)
	e, _ = estadoPend(t, ctx, pool, recente.ID)
	require.Equal(t, "aprovada", e)

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='mcp.pendente.falhou'
		AND resource='mcp_pendente' AND resource_id=$1 AND actor_id IS NULL`, presa.ID).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='mcp.pendente.falhou' AND resource_id=$1`, recente.ID).Scan(&n))
	require.Equal(t, 0, n)

	// o id do pedido fica em meta.mcp.pendente_id, como nas demais linhas mcp.pendente.*
	var meta map[string]any
	require.NoError(t, pool.QueryRow(ctx, `SELECT metadata FROM audit_log WHERE action='mcp.pendente.falhou' AND resource_id=$1`, presa.ID).Scan(&meta))
	require.Equal(t, presa.ID.String(), meta["mcp"].(map[string]any)["pendente_id"])
	require.Equal(t, "presa", meta["codigo"])

	v.tick(ctx) // idempotente: não audita de novo
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='mcp.pendente.falhou' AND resource_id=$1`, presa.ID).Scan(&n))
	require.Equal(t, 1, n)
}

func TestVarredor_PodaConfirmacoesVencidas(t *testing.T) {
	ctx, pool, v, _, _ := varredorDB(t)
	_, err := pool.Exec(ctx, `INSERT INTO mcp_confirmacoes_usadas (hash, expira_em) VALUES (decode(repeat('01',32),'hex'), $1), (decode(repeat('02',32),'hex'), $2)`,
		t0.Add(-2*time.Hour), t0.Add(-30*time.Minute))
	require.NoError(t, err)
	v.tick(ctx)
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM mcp_confirmacoes_usadas`).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM mcp_confirmacoes_usadas WHERE hash=decode(repeat('02',32),'hex')`).Scan(&n))
	require.Equal(t, 1, n)
}

func TestVarredor_LockTomadoNaoFazNada(t *testing.T) {
	ctx, pool, v, st, dono := varredorDB(t)
	velha, _, err := st.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0.Add(-25*time.Hour))
	require.NoError(t, err)

	outro, err := pool.Begin(ctx)
	require.NoError(t, err)
	var ok bool
	require.NoError(t, outro.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, chaveLockVarredor).Scan(&ok))
	require.True(t, ok)

	v.tick(ctx)
	e, _ := estadoPend(t, ctx, pool, velha.ID)
	require.Equal(t, "pendente", e, "com o lock em outra réplica, nada muda")

	require.NoError(t, outro.Rollback(ctx))
	v.tick(ctx)
	e, _ = estadoPend(t, ctx, pool, velha.ID)
	require.Equal(t, "expirada", e)
}

func TestVarredor_RunFazPassadaImediataEParaNoCancel(t *testing.T) {
	ctx, pool, v, st, dono := varredorDB(t)
	velha, _, err := st.Criar(ctx, novaDeTeste(dono, `{"id":1}`), t0.Add(-25*time.Hour))
	require.NoError(t, err)
	v.Intervalo = 10 * time.Millisecond

	rctx, cancel := context.WithCancel(ctx)
	feito := make(chan struct{})
	go func() { v.Run(rctx); close(feito) }()
	require.Eventually(t, func() bool {
		e, _ := estadoPend(t, ctx, pool, velha.ID)
		return e == "expirada"
	}, 5*time.Second, 20*time.Millisecond)
	cancel()
	select {
	case <-feito:
	case <-time.After(5 * time.Second):
		t.Fatal("Run não parou com o ctx cancelado")
	}
}
