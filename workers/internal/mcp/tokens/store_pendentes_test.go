package tokens_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/mcp/tokens"
)

// Revogar a credencial revoga os pedidos críticos que ela fez e que ainda esperam aprovação: o que
// uma IA desconectada pediu não pode mais ser aprovado.

func pedidoDoToken(t *testing.T, ctx context.Context, pool *pgxpool.Pool, user, token uuid.UUID, estado string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	var tok *uuid.UUID
	if token != uuid.Nil {
		tok = &token
	}
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO mcp_acoes_pendentes
		(user_id, token_id, tool, args, args_hash, preview, estado, expira_em)
		VALUES ($1, $2, 'arquivar_proposta', '{}', sha256(convert_to($3::text, 'UTF8')), '{"resumo":"x"}', $4, NOW() + INTERVAL '1 day')
		RETURNING id`, user, tok, uuid.NewString(), estado).Scan(&id))
	return id
}

func estadoDoPedido(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (string, *string) {
	t.Helper()
	var estado string
	var erro *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT estado, erro FROM mcp_acoes_pendentes WHERE id = $1`, id).Scan(&estado, &erro))
	return estado, erro
}

func exigirExpirado(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, msg string) {
	t.Helper()
	estado, erro := estadoDoPedido(t, ctx, pool, id)
	require.Equal(t, "expirada", estado, msg)
	require.NotNil(t, erro, msg)
	require.Equal(t, tokens.TextoPedidoRevogado, *erro, msg)
}

func exigirPendente(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, msg string) {
	t.Helper()
	estado, erro := estadoDoPedido(t, ctx, pool, id)
	require.Equal(t, "pendente", estado, msg)
	require.Nil(t, erro, msg)
}

func TestStore_RevogarUmTokenExpiraSoOsPedidosDele(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	st := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste))
	dono := mcptest.Usuario(t, ctx, pool, "admin", true)
	outro := mcptest.Usuario(t, ctx, pool, "admin", true)

	_, a, err := st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoPessoal, Nome: "a", Validade: time.Hour})
	require.NoError(t, err)
	_, b, err := st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoPessoal, Nome: "b", Validade: time.Hour})
	require.NoError(t, err)
	_, c, err := st.Emitir(ctx, tokens.Novo{UserID: outro, Tipo: tokens.TipoPessoal, Nome: "c", Validade: time.Hour})
	require.NoError(t, err)

	doA := pedidoDoToken(t, ctx, pool, dono, a.ID, "pendente")
	doA2 := pedidoDoToken(t, ctx, pool, dono, a.ID, "pendente")
	aprovadaDoA := pedidoDoToken(t, ctx, pool, dono, a.ID, "aprovada")
	executadaDoA := pedidoDoToken(t, ctx, pool, dono, a.ID, "executada")
	doB := pedidoDoToken(t, ctx, pool, dono, b.ID, "pendente")
	doC := pedidoDoToken(t, ctx, pool, outro, c.ID, "pendente")
	semToken := pedidoDoToken(t, ctx, pool, dono, uuid.Nil, "pendente")

	ok, err := st.Revogar(ctx, a.ID, dono)
	require.NoError(t, err)
	require.True(t, ok)

	exigirExpirado(t, ctx, pool, doA, "pedido do token revogado")
	exigirExpirado(t, ctx, pool, doA2, "outro pedido do mesmo token")
	e, _ := estadoDoPedido(t, ctx, pool, aprovadaDoA)
	require.Equal(t, "aprovada", e, "em execução: não se mexe")
	e, _ = estadoDoPedido(t, ctx, pool, executadaDoA)
	require.Equal(t, "executada", e)
	exigirPendente(t, ctx, pool, doB, "outro token do mesmo usuário")
	exigirPendente(t, ctx, pool, doC, "token de outra pessoa")
	exigirPendente(t, ctx, pool, semToken, "sem token_id não é de token nenhum")
}

// Revogar uma conexão OAuth derruba todos os tokens do cliente: o pedido de QUALQUER token dela
// expira, inclusive o do access token que já venceu ou foi trocado no refresh.
func TestStore_RevogarConexaoOAuthExpiraOsPedidosDeTodosOsTokensDela(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	st := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste))
	dono := mcptest.Usuario(t, ctx, pool, "admin", true)
	_, err := pool.Exec(ctx, `INSERT INTO mcp_oauth_clients (client_id, nome, redirect_uris) VALUES ('cli-a', 'A', ARRAY['https://x.teste/cb']), ('cli-b', 'B', ARRAY['https://x.teste/cb'])`)
	require.NoError(t, err)
	cliA, cliB := "cli-a", "cli-b"

	// access token que venceu (hora passou) e a conexão segue pelo refresh
	acessoVelho := insere(t, ctx, pool, dono, "oauth_access", &cliA, "-30 minutes")
	acessoNovo := insere(t, ctx, pool, dono, "oauth_access", &cliA, "30 minutes")
	refresh := insere(t, ctx, pool, dono, "oauth_refresh", &cliA, "10 days")
	deB := insere(t, ctx, pool, dono, "oauth_refresh", &cliB, "10 days")
	pessoal := insere(t, ctx, pool, dono, "pessoal", nil, "10 days")

	p1 := pedidoDoToken(t, ctx, pool, dono, acessoVelho, "pendente")
	p2 := pedidoDoToken(t, ctx, pool, dono, acessoNovo, "pendente")
	pB := pedidoDoToken(t, ctx, pool, dono, deB, "pendente")
	pPessoal := pedidoDoToken(t, ctx, pool, dono, pessoal, "pendente")

	// o id passado é o do refresh (é o que a tela de conexões mostra)
	ok, err := st.Revogar(ctx, refresh, dono)
	require.NoError(t, err)
	require.True(t, ok)

	exigirExpirado(t, ctx, pool, p1, "access token vencido da conexão")
	exigirExpirado(t, ctx, pool, p2, "access token vivo da conexão")
	exigirPendente(t, ctx, pool, pB, "outro cliente OAuth")
	exigirPendente(t, ctx, pool, pPessoal, "token pessoal")
}

func TestStore_RevogarTokenDeOutraPessoaNaoExpiraNada(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	st := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste))
	dono := mcptest.Usuario(t, ctx, pool, "admin", true)
	intruso := mcptest.Usuario(t, ctx, pool, "admin", true)
	_, tok, err := st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoPessoal, Nome: "a", Validade: time.Hour})
	require.NoError(t, err)
	p := pedidoDoToken(t, ctx, pool, dono, tok.ID, "pendente")

	ok, err := st.Revogar(ctx, tok.ID, intruso)
	require.NoError(t, err)
	require.False(t, ok)
	exigirPendente(t, ctx, pool, p, "revogar o token alheio é 404 e não toca nos pedidos")
}

func TestStore_RevogarTodosExpiraTodosOsPedidosPendentesDoUsuario(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	st := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste))
	dono := mcptest.Usuario(t, ctx, pool, "admin", true)
	outro := mcptest.Usuario(t, ctx, pool, "admin", true)
	_, a, err := st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoPessoal, Nome: "a", Validade: time.Hour})
	require.NoError(t, err)
	_, c, err := st.Emitir(ctx, tokens.Novo{UserID: outro, Tipo: tokens.TipoPessoal, Nome: "c", Validade: time.Hour})
	require.NoError(t, err)

	doA := pedidoDoToken(t, ctx, pool, dono, a.ID, "pendente")
	semToken := pedidoDoToken(t, ctx, pool, dono, uuid.Nil, "pendente") // o token sumiu, o pedido é dele
	aprovada := pedidoDoToken(t, ctx, pool, dono, a.ID, "aprovada")
	doC := pedidoDoToken(t, ctx, pool, outro, c.ID, "pendente")

	n, err := st.RevogarTodos(ctx, dono)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	exigirExpirado(t, ctx, pool, doA, "pedido do token")
	exigirExpirado(t, ctx, pool, semToken, "todo pedido aberto do usuário")
	e, _ := estadoDoPedido(t, ctx, pool, aprovada)
	require.Equal(t, "aprovada", e)
	exigirPendente(t, ctx, pool, doC, "outro usuário")
}

func insere(t *testing.T, ctx context.Context, pool *pgxpool.Pool, user uuid.UUID, tipo string, client *string, venceEm string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO mcp_tokens (user_id, tipo, token_hash, nome, client_id, expires_at)
		VALUES ($1, $2, $3, 'n', $4, NOW() + $5::interval) RETURNING id`,
		user, tipo, uuid.NewString(), client, venceEm).Scan(&id))
	return id
}
