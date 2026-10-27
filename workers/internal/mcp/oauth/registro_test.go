package oauth

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
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
