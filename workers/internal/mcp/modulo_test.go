package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/mcp/tokens"
)

func TestModulo_TokenPessoalChamaQuemSouEu(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	m := NovoModulo(DepsModulo{Pool: pool, Segredo: []byte(mcptest.SegredoTeste), Origem: "https://api.teste", Portal: "https://portal.teste", Versao: "teste", Log: zap.NewNop()})
	require.NotNil(t, m)
	r := chi.NewRouter()
	m.MontarPublico(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	claro, _, err := tokens.NovoStore(pool, []byte(mcptest.SegredoTeste)).Emitir(ctx, tokens.Novo{
		UserID: mcptest.Usuario(t, ctx, pool, "admin", true), Tipo: tokens.TipoPessoal, Nome: "t", Validade: time.Hour})
	require.NoError(t, err)

	pedir := func(metodo, bearer string) *http.Response {
		req, _ := http.NewRequest(metodo, srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"quem_sou_eu","arguments":{}}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		res.Body.Close()
		return res
	}
	require.Equal(t, http.StatusMethodNotAllowed, pedir(http.MethodGet, "").StatusCode, "405 vem antes do 401")
	sem := pedir(http.MethodPost, "")
	require.Equal(t, http.StatusUnauthorized, sem.StatusCode)
	require.Contains(t, sem.Header.Get("WWW-Authenticate"), `resource_metadata="https://api.teste/.well-known/oauth-protected-resource/mcp"`)
	require.Equal(t, http.StatusOK, pedir(http.MethodPost, claro).StatusCode)

	res, err := http.Get(srv.URL + "/.well-known/oauth-authorization-server")
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestModulo_SegredoCurtoNaoMonta(t *testing.T) {
	require.Nil(t, NovoModulo(DepsModulo{Segredo: []byte("curto"), Log: zap.NewNop()}))
}
