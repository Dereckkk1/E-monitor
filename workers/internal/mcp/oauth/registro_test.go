package oauth

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/mcp/mcptest"
)

func TestMetadados(t *testing.T) {
	a := novoAmbiente(t)
	st, rec := a.get("/.well-known/oauth-protected-resource/mcp", "")
	require.Equal(t, http.StatusOK, st)
	require.Equal(t, "https://api.teste/mcp", rec["resource"])
	require.Equal(t, []any{"https://api.teste"}, rec["authorization_servers"])

	st, as := a.get("/.well-known/oauth-authorization-server", "")
	require.Equal(t, http.StatusOK, st)
	require.Equal(t, "https://api.teste", as["issuer"])
	require.Equal(t, "https://api.teste/mcp/oauth/token", as["token_endpoint"])
	require.Equal(t, []any{"S256"}, as["code_challenge_methods_supported"])
	require.Contains(t, as["token_endpoint_auth_methods_supported"], "client_secret_post")
}

func TestRegistro_PublicoEConfidencial(t *testing.T) {
	a := novoAmbiente(t)
	st, pub := a.registrar(map[string]any{"client_name": "Claude", "redirect_uris": []string{redirectClaude}})
	require.Equal(t, http.StatusCreated, st)
	require.True(t, strings.HasPrefix(pub["client_id"].(string), "emc_"))
	require.Equal(t, "none", pub["token_endpoint_auth_method"])
	require.NotContains(t, pub, "client_secret")

	st, conf := a.registrar(map[string]any{
		"client_name": "Gemini Enterprise", "token_endpoint_auth_method": "client_secret_post",
		"redirect_uris": []string{"https://vertexaisearch.cloud.google.com/oauth-redirect"},
	})
	require.Equal(t, http.StatusCreated, st)
	require.True(t, strings.HasPrefix(conf["client_secret"].(string), "emonitor_mcs_"))
}

func TestRegistro_Recusas(t *testing.T) {
	a := novoAmbiente(t)
	st, e := a.registrar(map[string]any{"client_name": "x", "redirect_uris": []string{"http://evil.example/cb"}})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_redirect_uri", e["error"])

	st, e = a.registrar(map[string]any{"client_name": "x", "redirect_uris": []string{}})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_redirect_uri", e["error"])

	st, e = a.registrar(map[string]any{"client_name": "x", "redirect_uris": []string{redirectClaude}, "token_endpoint_auth_method": "private_key_jwt"})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_client_metadata", e["error"], "método desconhecido é recusado, não rebaixado")
}

// Phishing de consentimento: o DCR é aberto, mas o código só pode voltar para
// os assistentes que a gente conhece (ou para loopback, do Claude Code).
func TestRegistro_SoDestinosPermitidos(t *testing.T) {
	a := novoAmbiente(t)
	recusados := []string{
		"https://evil.example/cb",
		"https://сlaude.ai/cb", // c cirílico
		"https://sub.claude.ai/cb",
		"https://claude.ai.evil.example/cb",
	}
	for _, uri := range recusados {
		st, e := a.registrar(map[string]any{"client_name": "x", "redirect_uris": []string{uri}})
		require.Equal(t, http.StatusBadRequest, st, uri)
		require.Equal(t, "invalid_redirect_uri", e["error"], uri)
	}
	_, e := a.registrar(map[string]any{"client_name": "x", "redirect_uris": []string{"https://evil.example/cb"}})
	require.Contains(t, e["error_description"], "MCP_REDIRECT_HOSTS")
	require.Contains(t, e["error_description"], "claude.ai")

	// Um destino ruim no meio derruba o registro inteiro.
	st, _ := a.registrar(map[string]any{"client_name": "x", "redirect_uris": []string{redirectClaude, "https://evil.example/cb"}})
	require.Equal(t, http.StatusBadRequest, st)

	aceitos := []string{
		redirectClaude,
		"https://CLAUDE.AI/api/mcp/auth_callback",
		"https://vertexaisearch.cloud.google.com/oauth-redirect",
		"http://localhost:4321/callback",
		"http://127.0.0.1:1/cb",
		"http://[::1]:65000/cb",
	}
	for _, uri := range aceitos {
		st, e := a.registrar(map[string]any{"client_name": "x", "redirect_uris": []string{uri}})
		require.Equal(t, http.StatusCreated, st, "%s: %v", uri, e)
	}
}

func TestRegistro_HostsDoAmbiente(t *testing.T) {
	a := novoAmbiente(t)
	t.Setenv("MCP_REDIRECT_HOSTS", "a.test, B.TEST")
	srv := NovoServidor(a.pool, a.srv.Tokens, []byte(mcptest.SegredoTeste), "https://api.teste", "https://portal.teste", zap.NewNop())
	registrar := func(uri string) int {
		req := httptest.NewRequest(http.MethodPost, "/mcp/oauth/register",
			strings.NewReader(`{"client_name":"x","redirect_uris":["`+uri+`"]}`))
		rec := httptest.NewRecorder()
		srv.Registrar(rec, req)
		return rec.Code
	}
	require.Equal(t, http.StatusCreated, registrar("https://b.test/cb"))
	require.Equal(t, http.StatusCreated, registrar("https://a.test/cb"))
	require.Equal(t, http.StatusBadRequest, registrar(redirectClaude), "a lista do ambiente substitui a padrão")
	require.Equal(t, http.StatusCreated, registrar("http://localhost:9/cb"), "loopback vale sempre")
}

func TestRegistro_NomeSemCaracteresDeControle(t *testing.T) {
	a := novoAmbiente(t)
	st, reg := a.registrar(map[string]any{"client_name": "Cla‮ude​", "redirect_uris": []string{redirectClaude}})
	require.Equal(t, http.StatusCreated, st)
	require.Equal(t, "Claude", reg["client_name"])
	var nome string
	require.NoError(t, a.pool.QueryRow(a.ctx, `SELECT nome FROM mcp_oauth_clients WHERE client_id = $1`, reg["client_id"]).Scan(&nome))
	require.Equal(t, "Claude", nome)

	_, reg = a.registrar(map[string]any{"client_name": "‮​", "redirect_uris": []string{redirectClaude}})
	require.Equal(t, "Assistente sem nome", reg["client_name"], "só controle vira nome vazio")
}

func TestRegistro_LimitePorIP(t *testing.T) {
	a := novoAmbiente(t)
	a.srv.Registros = auth.NewLoginRateLimiter(2, time.Hour)
	corpo := map[string]any{"client_name": "x", "redirect_uris": []string{redirectClaude}}
	for i := 0; i < 2; i++ {
		st, _ := a.registrar(corpo)
		require.Equal(t, http.StatusCreated, st)
	}
	st, _ := a.registrar(corpo)
	require.Equal(t, http.StatusTooManyRequests, st)
}

// O router de prod tem middleware.RealIP, que confia em X-Real-IP — um header
// que o cliente forja e o Cloudflare repassa. A chave tem de ser o
// CF-Connecting-IP, que a borda do Cloudflare sobrescreve.
func TestRegistro_LimiteNaoBurladoPorXRealIP(t *testing.T) {
	a := novoAmbiente(t)
	a.srv.Registros = auth.NewLoginRateLimiter(2, time.Hour)
	r := chi.NewRouter()
	r.Use(middleware.RealIP) // igual ao router.go
	r.Post("/mcp/oauth/register", a.srv.Registrar)
	hs := httptest.NewServer(r)
	defer hs.Close()
	aceitos := 0
	for i := 0; i < 6; i++ {
		req, _ := http.NewRequest(http.MethodPost, hs.URL+"/mcp/oauth/register",
			bytes.NewReader([]byte(`{"client_name":"x","redirect_uris":["`+redirectClaude+`"]}`)))
		req.Header.Set("X-Real-IP", fmt.Sprintf("10.0.0.%d", i))
		req.Header.Set("CF-Connecting-IP", "203.0.113.7")
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		res.Body.Close()
		if res.StatusCode == http.StatusCreated {
			aceitos++
		}
	}
	require.Equal(t, 2, aceitos, "X-Real-IP rotativo não fura o limite")
}

func TestRegistro_TetoDeClientesPendentes(t *testing.T) {
	a := novoAmbiente(t)
	a.srv.MaxClientesPendentes = 2
	corpo := map[string]any{"client_name": "x", "redirect_uris": []string{redirectClaude}}
	ids := []string{}
	for i := 0; i < 2; i++ {
		st, reg := a.registrar(corpo)
		require.Equal(t, http.StatusCreated, st)
		ids = append(ids, reg["client_id"].(string))
	}
	st, e := a.registrar(corpo)
	require.Equal(t, http.StatusServiceUnavailable, st)
	require.Equal(t, "temporarily_unavailable", e["error"])
	require.Equal(t, 2, a.contar(`SELECT COUNT(*) FROM mcp_oauth_clients`))

	a.ativar(ids[0]) // quem já chegou ao /token não conta no teto
	st, _ = a.registrar(corpo)
	require.Equal(t, http.StatusCreated, st)
}

func TestRegistro_TetoDeRedirectURIs(t *testing.T) {
	a := novoAmbiente(t)
	seis := []string{}
	for i := 0; i < 6; i++ {
		seis = append(seis, fmt.Sprintf("%s?n=%d", redirectClaude, i))
	}
	st, e := a.registrar(map[string]any{"client_name": "x", "redirect_uris": seis})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_redirect_uri", e["error"])

	st, _ = a.registrar(map[string]any{"client_name": "x", "redirect_uris": seis[:5]})
	require.Equal(t, http.StatusCreated, st)

	longa := redirectClaude + "?p=" + strings.Repeat("a", 2049-len(redirectClaude)-3)
	require.Len(t, longa, 2049)
	st, e = a.registrar(map[string]any{"client_name": "x", "redirect_uris": []string{longa}})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "invalid_redirect_uri", e["error"])
	require.NotContains(t, e["error_description"], longa, "não ecoa a URI gigante")
}
