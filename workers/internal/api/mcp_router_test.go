package api

// mcp_router_test.go — o MCP montado pelo NewRouter de verdade: CORS do
// preflight (clientes de navegador na spec 2026-07-28 mandam Mcp-Method e
// Mcp-Name), 405 antes do 401 e o 401 com o WWW-Authenticate do contrato.
// Nenhum desses caminhos toca o banco, então o módulo sobe sem pool.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/mcp"
)

func mcpRouter(t *testing.T) http.Handler {
	t.Helper()
	m := mcp.NovoModulo(mcp.DepsModulo{
		Segredo: []byte("segredo-de-teste-do-mcp-com-mais-de-32-bytes"),
		Origem:  "https://api.teste",
		Portal:  "https://portal.teste",
		Versao:  "teste",
		Log:     zap.NewNop(),
	})
	require.NotNil(t, m)
	return NewRouter(Deps{MCP: m})
}

func TestRouterMCP_PreflightAceitaCabecalhosDoMCP(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	req.Header.Set("Origin", "https://claude.ai")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	mcpRouter(t).ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	permitidos := rec.Header().Get("Access-Control-Allow-Headers")
	for _, h := range []string{"Authorization", "Mcp-Protocol-Version", "Mcp-Method", "Mcp-Name"} {
		require.Contains(t, permitidos, h)
	}
	require.Contains(t, rec.Header().Get("Access-Control-Expose-Headers"), "WWW-Authenticate")
}

func TestRouterMCP_GetDa405(t *testing.T) {
	rec := httptest.NewRecorder()
	mcpRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestRouterMCP_PostSemBearerDa401DoContrato(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mcpRouter(t).ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	www := rec.Header().Get("WWW-Authenticate")
	require.Contains(t, www, `realm="emonitor-mcp"`)
	require.Contains(t, www, `resource_metadata="https://api.teste/.well-known/oauth-protected-resource/mcp"`)
}

// As duas autenticações não se misturam (spec): o JWT do portal não abre o
// /mcp e o token do MCP não abre o /v1/internal. Nenhum caminho toca o banco.
func TestRouterMCP_JWTDoPortalNaoAbreOMCP(t *testing.T) {
	t.Setenv("JWT_SECRET", "segredo-jwt-de-teste-com-mais-de-32-bytes!!")
	jwt, err := auth.IssueToken(uuid.New(), "admin")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	rec := httptest.NewRecorder()
	mcpRouter(t).ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Header().Get("WWW-Authenticate"), `error="invalid_token"`)
}

func TestRouterMCP_TokenDoMCPNaoAbreOInternal(t *testing.T) {
	t.Setenv("JWT_SECRET", "segredo-jwt-de-teste-com-mais-de-32-bytes!!")
	req := httptest.NewRequest(http.MethodGet, "/v1/internal/mcp/tokens", nil)
	req.Header.Set("Authorization", "Bearer emonitor_mcp_"+strings.Repeat("a1b2c3d4", 8))
	rec := httptest.NewRecorder()
	mcpRouter(t).ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// As rotas de aprovação dos pedidos críticos ficam atrás de JWT + admin: sem
// JWT é 401 e um usuário de cliente (viewer) ou operador é 403, antes de
// qualquer handler (então sem banco).
func TestRouterMCP_PendentesExigemAdmin(t *testing.T) {
	t.Setenv("JWT_SECRET", "segredo-jwt-de-teste-com-mais-de-32-bytes!!")
	r := mcpRouter(t)
	id := uuid.NewString()
	rotas := [][2]string{
		{http.MethodGet, "/v1/internal/mcp/pendentes"},
		{http.MethodPost, "/v1/internal/mcp/pendentes/" + id + "/aprovar"},
		{http.MethodPost, "/v1/internal/mcp/pendentes/" + id + "/recusar"},
	}
	for _, rota := range rotas {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(rota[0], rota[1], nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code, "sem JWT: "+rota[1])

		for _, papel := range []string{"viewer", "operator"} {
			jwt, err := auth.IssueToken(uuid.New(), papel)
			require.NoError(t, err)
			req := httptest.NewRequest(rota[0], rota[1], nil)
			req.Header.Set("Authorization", "Bearer "+jwt)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			require.Equal(t, http.StatusForbidden, rec.Code, papel+": "+rota[1])
		}
	}
}
