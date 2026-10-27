package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/mcp/tokens"
)

func autenticadorDeTeste(t *testing.T) (*Autenticador, *tokens.Store, func(papel string, ativo bool) string) {
	ctx, pool := mcptest.DB(t)
	st := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste))
	a := NovoAutenticador(st, "https://api.teste/.well-known/oauth-protected-resource/mcp", zap.NewNop())
	emitir := func(papel string, ativo bool) string {
		claro, _, err := st.Emitir(ctx, tokens.Novo{UserID: mcptest.Usuario(t, ctx, pool, papel, ativo), Tipo: tokens.TipoPessoal, Nome: "t", Validade: time.Hour})
		require.NoError(t, err)
		return claro
	}
	return a, st, emitir
}

func chamar(a *Autenticador, bearer, ip string) (*httptest.ResponseRecorder, *Principal) {
	var visto *Principal
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visto, _ = PrincipalDe(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.RemoteAddr = ip + ":5555"
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, visto
}

func TestAutenticador_SemCredencial(t *testing.T) {
	a, _, _ := autenticadorDeTeste(t)
	rec, _ := chamar(a, "", "10.0.0.1")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t,
		`Bearer realm="emonitor-mcp", resource_metadata="https://api.teste/.well-known/oauth-protected-resource/mcp", scope="emonitor:all"`,
		rec.Header().Get("WWW-Authenticate"), "sem error= quando não veio credencial")
}

// O caso "refresh usado como bearer" está em TestFluxoPublico_PontaAPonta (oauth).
func TestAutenticador_Recusas(t *testing.T) {
	a, _, emitir := autenticadorDeTeste(t)
	casos := map[string]string{
		"lixo":          "emonitor_mcp_naoexiste",
		"outro prefixo": "plura_mcp_qualquer",
		"operator":      emitir("operator", true),
		"admin inativo": emitir("admin", false),
	}
	for nome, bearer := range casos {
		rec, _ := chamar(a, bearer, "10.0.0.2")
		require.Equal(t, http.StatusUnauthorized, rec.Code, nome)
		require.Contains(t, rec.Header().Get("WWW-Authenticate"), `error="invalid_token"`, nome)
	}
}

func TestAutenticador_AdminAtivoPassaERevogadoNao(t *testing.T) {
	a, st, emitir := autenticadorDeTeste(t)
	claro := emitir("admin", true)
	rec, p := chamar(a, claro, "10.0.0.3")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, p)
	require.Equal(t, "Pessoa de Teste", p.Nome)

	tok, err := st.Buscar(t.Context(), claro, tokens.TipoPessoal)
	require.NoError(t, err)
	require.NotNil(t, tok.LastUsedAt, "uso registrado")
	ok, err := st.Revogar(t.Context(), tok.ID, p.UserID)
	require.NoError(t, err)
	require.True(t, ok)
	rec, _ = chamar(a, claro, "10.0.0.3")
	require.Equal(t, http.StatusUnauthorized, rec.Code, "revogado morre no pedido seguinte")
}

func TestAutenticador_CotaPorToken(t *testing.T) {
	a, _, emitir := autenticadorDeTeste(t)
	a.PorToken = auth.NewLoginRateLimiter(2, time.Minute)
	claro := emitir("admin", true)
	for i := 0; i < 2; i++ {
		rec, _ := chamar(a, claro, "10.0.0.4")
		require.Equal(t, http.StatusOK, rec.Code)
	}
	rec, _ := chamar(a, claro, "10.0.0.4")
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.NotEmpty(t, rec.Header().Get("Retry-After"))
}

func TestAutenticador_FalhasPorIPNuncaBarramTokenValido(t *testing.T) {
	a, _, emitir := autenticadorDeTeste(t)
	a.FalhasPorIP = auth.NewLoginRateLimiter(2, time.Minute)
	for i := 0; i < 2; i++ {
		rec, _ := chamar(a, "emonitor_mcp_errado", "10.0.0.5")
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	}
	rec, _ := chamar(a, "emonitor_mcp_errado", "10.0.0.5")
	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	rec, _ = chamar(a, emitir("admin", true), "10.0.0.5")
	require.Equal(t, http.StatusOK, rec.Code, "egress do claude.ai é compartilhado: token válido passa")
}
