package tokens_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/mcp/tokens"
)

func novoStore(t *testing.T) (*tokens.Store, func(papel string, ativo bool) tokens.Novo) {
	ctx, pool := mcptest.DB(t)
	st := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste))
	return st, func(papel string, ativo bool) tokens.Novo {
		return tokens.Novo{
			UserID:   mcptest.Usuario(t, ctx, pool, papel, ativo),
			Tipo:     tokens.TipoPessoal,
			Nome:     "notebook",
			Validade: time.Hour,
		}
	}
}

func TestStore_EmitirEBuscar(t *testing.T) {
	st, novo := novoStore(t)
	ctx := t.Context()
	n := novo("admin", true)
	claro, tok, err := st.Emitir(ctx, n)
	require.NoError(t, err)
	require.Contains(t, claro, tokens.PrefixoAcesso)
	require.Equal(t, n.UserID, tok.UserID)

	achado, err := st.Buscar(ctx, claro, tokens.TipoPessoal, tokens.TipoOAuthAcesso)
	require.NoError(t, err)
	require.Equal(t, tok.ID, achado.ID)

	_, err = st.Buscar(ctx, claro, tokens.TipoOAuthRefresh)
	require.ErrorIs(t, err, tokens.ErrNaoEncontrado, "o tipo filtra")
	_, err = st.Buscar(ctx, claro+"0", tokens.TipoPessoal)
	require.ErrorIs(t, err, tokens.ErrNaoEncontrado)
}

func TestStore_VencidoNaoAcha(t *testing.T) {
	st, novo := novoStore(t)
	n := novo("admin", true)
	n.Validade = -time.Minute
	claro, _, err := st.Emitir(t.Context(), n)
	require.NoError(t, err)
	_, err = st.Buscar(t.Context(), claro, tokens.TipoPessoal)
	require.ErrorIs(t, err, tokens.ErrNaoEncontrado)
}

func TestStore_RevogarSoODono(t *testing.T) {
	st, novo := novoStore(t)
	ctx := t.Context()
	a, b := novo("admin", true), novo("admin", true)
	claro, tok, err := st.Emitir(ctx, a)
	require.NoError(t, err)

	ok, err := st.Revogar(ctx, tok.ID, b.UserID)
	require.NoError(t, err)
	require.False(t, ok, "outra pessoa não revoga")

	ok, err = st.Revogar(ctx, tok.ID, a.UserID)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = st.Buscar(ctx, claro, tokens.TipoPessoal)
	require.ErrorIs(t, err, tokens.ErrNaoEncontrado)
}

func TestStore_RevogarConexaoOAuthDerrubaOParInteiro(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	st := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste))
	dono := mcptest.Usuario(t, ctx, pool, "admin", true)
	_, err := pool.Exec(ctx, `INSERT INTO mcp_oauth_clients (client_id, nome, redirect_uris)
		VALUES ('emc_x', 'Claude', ARRAY['https://claude.ai/api/mcp/auth_callback'])`)
	require.NoError(t, err)
	cid := "emc_x"
	acesso, _, err := st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoOAuthAcesso, Nome: "Claude", ClientID: &cid, Validade: time.Hour})
	require.NoError(t, err)
	_, refresh, err := st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoOAuthRefresh, Nome: "Claude", ClientID: &cid, Validade: time.Hour})
	require.NoError(t, err)

	conexoes, err := st.ListarConexoes(ctx, dono)
	require.NoError(t, err)
	require.Len(t, conexoes, 1, "a conexão OAuth aparece uma vez, pelo refresh")

	ok, err := st.Revogar(ctx, refresh.ID, dono)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = st.Buscar(ctx, acesso, tokens.TipoOAuthAcesso)
	require.ErrorIs(t, err, tokens.ErrNaoEncontrado, "revogar a conexão mata o access também")
}

func TestStore_ConsumirRefreshUmaVez(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	st := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste))
	dono := mcptest.Usuario(t, ctx, pool, "admin", true)
	claro, _, err := st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoOAuthRefresh, Nome: "x", Validade: time.Hour})
	require.NoError(t, err)
	require.Contains(t, claro, tokens.PrefixoRefresh)

	_, err = st.ConsumirRefresh(ctx, claro)
	require.NoError(t, err)
	_, err = st.ConsumirRefresh(ctx, claro)
	require.ErrorIs(t, err, tokens.ErrNaoEncontrado)
}

func TestStore_RevogarTodosEDonoAdminAtivo(t *testing.T) {
	st, novo := novoStore(t)
	ctx := t.Context()
	a := novo("admin", true)
	claro, _, err := st.Emitir(ctx, a)
	require.NoError(t, err)

	dono, err := st.DonoAdminAtivo(ctx, a.UserID)
	require.NoError(t, err)
	require.Equal(t, a.UserID, dono.ID)

	op := novo("operator", true)
	_, err = st.DonoAdminAtivo(ctx, op.UserID)
	require.ErrorIs(t, err, tokens.ErrNaoEncontrado)
	inativo := novo("admin", false)
	_, err = st.DonoAdminAtivo(ctx, inativo.UserID)
	require.ErrorIs(t, err, tokens.ErrNaoEncontrado)

	n, err := st.RevogarTodos(ctx, a.UserID)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	_, err = st.Buscar(ctx, claro, tokens.TipoPessoal)
	require.ErrorIs(t, err, tokens.ErrNaoEncontrado)
}
