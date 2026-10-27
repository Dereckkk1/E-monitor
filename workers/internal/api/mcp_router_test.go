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

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

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
