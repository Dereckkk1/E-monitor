package tokens_test

import (
	"strings"
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
	require.True(t, strings.HasPrefix(claro, tokens.PrefixoAcesso))
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
	require.True(t, strings.HasPrefix(claro, tokens.PrefixoRefresh))

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

func TestStore_SoOHashVaiAoBanco(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	st := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste))
	dono := mcptest.Usuario(t, ctx, pool, "admin", true)
	claro, tok, err := st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoPessoal, Nome: "n", Validade: time.Hour})
	require.NoError(t, err)

	var guardado string
	require.NoError(t, pool.QueryRow(ctx, `SELECT token_hash FROM mcp_tokens WHERE id = $1`, tok.ID).Scan(&guardado))
	require.Equal(t, tokens.Hash([]byte(mcptest.SegredoTeste), claro), guardado)
	require.NotEqual(t, claro, guardado)
}

func TestStore_RevogarConexaoNaoRevogaDemais(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	st := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste))
	dono := mcptest.Usuario(t, ctx, pool, "admin", true)
	outro := mcptest.Usuario(t, ctx, pool, "admin", true)
	_, err := pool.Exec(ctx, `INSERT INTO mcp_oauth_clients (client_id, nome, redirect_uris) VALUES
		('emc_a', 'A', ARRAY['https://a.test/cb']), ('emc_b', 'B', ARRAY['https://b.test/cb'])`)
	require.NoError(t, err)
	a, b := "emc_a", "emc_b"

	alvoAcesso, _, err := st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoOAuthAcesso, Nome: "A", ClientID: &a, Validade: time.Hour})
	require.NoError(t, err)
	_, alvoRefresh, err := st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoOAuthRefresh, Nome: "A", ClientID: &a, Validade: time.Hour})
	require.NoError(t, err)
	pessoal, _, err := st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoPessoal, Nome: "p", Validade: time.Hour})
	require.NoError(t, err)
	outroCliente, _, err := st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoOAuthAcesso, Nome: "B", ClientID: &b, Validade: time.Hour})
	require.NoError(t, err)
	outraPessoa, _, err := st.Emitir(ctx, tokens.Novo{UserID: outro, Tipo: tokens.TipoOAuthAcesso, Nome: "A", ClientID: &a, Validade: time.Hour})
	require.NoError(t, err)

	ok, err := st.Revogar(ctx, alvoRefresh.ID, dono)
	require.NoError(t, err)
	require.True(t, ok)

	_, err = st.Buscar(ctx, alvoAcesso, tokens.TipoOAuthAcesso)
	require.ErrorIs(t, err, tokens.ErrNaoEncontrado)
	_, err = st.Buscar(ctx, pessoal, tokens.TipoPessoal)
	require.NoError(t, err, "o token pessoal do mesmo usuário sobrevive")
	_, err = st.Buscar(ctx, outroCliente, tokens.TipoOAuthAcesso)
	require.NoError(t, err, "o token do mesmo usuário em outro client_id sobrevive")
	_, err = st.Buscar(ctx, outraPessoa, tokens.TipoOAuthAcesso)
	require.NoError(t, err, "o token de outra pessoa no mesmo client_id sobrevive")
}

func TestStore_RevogarTodosApagaOsCodigosSoDoUsuario(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	st := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste))
	dono := mcptest.Usuario(t, ctx, pool, "admin", true)
	outro := mcptest.Usuario(t, ctx, pool, "admin", true)
	_, err := pool.Exec(ctx, `INSERT INTO mcp_oauth_clients (client_id, nome, redirect_uris)
		VALUES ('emc_c', 'C', ARRAY['https://c.test/cb'])`)
	require.NoError(t, err)
	for i, u := range []any{dono, outro} {
		_, err = pool.Exec(ctx, `INSERT INTO mcp_oauth_codes (code_hash, client_id, user_id, redirect_uri, expires_at)
			VALUES ($1, 'emc_c', $2, 'https://c.test/cb', NOW() + INTERVAL '10 minutes')`, "code-"+string(rune('a'+i)), u)
		require.NoError(t, err)
	}

	_, err = st.RevogarTodos(ctx, dono)
	require.NoError(t, err)

	var doDono, doOutro int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_oauth_codes WHERE user_id = $1`, dono).Scan(&doDono))
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_oauth_codes WHERE user_id = $1`, outro).Scan(&doOutro))
	require.Zero(t, doDono)
	require.Equal(t, 1, doOutro)
}

func TestStore_RevogarTodosContaSoOsVivos(t *testing.T) {
	st, novo := novoStore(t)
	ctx := t.Context()
	a := novo("admin", true)
	_, _, err := st.Emitir(ctx, a)
	require.NoError(t, err)
	vencido := a
	vencido.Validade = -time.Minute
	_, _, err = st.Emitir(ctx, vencido)
	require.NoError(t, err)

	n, err := st.RevogarTodos(ctx, a.UserID)
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "o vencido é revogado mas não conta")
}

func TestStore_DonoAdminAtivoRecusaApagado(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	st := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste))
	id := mcptest.Usuario(t, ctx, pool, "admin", true)
	_, err := pool.Exec(ctx, `UPDATE users SET deleted_at = NOW() WHERE id = $1`, id)
	require.NoError(t, err)

	_, err = st.DonoAdminAtivo(ctx, id)
	require.ErrorIs(t, err, tokens.ErrNaoEncontrado)
}

func TestStore_TocarRegistraUltimoUso(t *testing.T) {
	st, novo := novoStore(t)
	ctx := t.Context()
	claro, tok, err := st.Emitir(ctx, novo("admin", true))
	require.NoError(t, err)
	require.Nil(t, tok.LastUsedAt)

	require.NoError(t, st.Tocar(ctx, tok.ID))
	achado, err := st.Buscar(ctx, claro, tokens.TipoPessoal)
	require.NoError(t, err)
	require.NotNil(t, achado.LastUsedAt)
}

func TestStore_ListarConexoesUmaLinhaPorConexao(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	st := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste))
	dono := mcptest.Usuario(t, ctx, pool, "admin", true)
	_, err := pool.Exec(ctx, `INSERT INTO mcp_oauth_clients (client_id, nome, redirect_uris)
		VALUES ('emc_l', 'L', ARRAY['https://l.test/cb'])`)
	require.NoError(t, err)
	cid := "emc_l"
	for i := 0; i < 2; i++ {
		_, _, err = st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoOAuthRefresh, Nome: "L", ClientID: &cid, Validade: time.Hour})
		require.NoError(t, err)
		_, _, err = st.Emitir(ctx, tokens.Novo{UserID: dono, Tipo: tokens.TipoPessoal, Nome: "p", Validade: time.Hour})
		require.NoError(t, err)
	}

	conexoes, err := st.ListarConexoes(ctx, dono)
	require.NoError(t, err)
	require.Len(t, conexoes, 3, "2 pessoais + 1 conexão OAuth (os 2 refresh do mesmo client colapsam)")
	for i := 1; i < len(conexoes); i++ {
		require.False(t, conexoes[i].CreatedAt.After(conexoes[i-1].CreatedAt), "created_at DESC")
	}
}
