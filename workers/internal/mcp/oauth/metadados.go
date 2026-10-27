package oauth

import "net/http"

// MetadadosRecurso — RFC 9728. Servido em /.well-known/oauth-protected-resource
// e em /.well-known/oauth-protected-resource/mcp (clientes tentam os dois).
func (s *Servidor) MetadadosRecurso(w http.ResponseWriter, _ *http.Request) {
	escreverJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.URLRecurso(),
		"authorization_servers":    []string{s.Origem},
		"scopes_supported":         []string{Escopo},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "E-monitor",
	})
}

// MetadadosAutorizacao — RFC 8414.
func (s *Servidor) MetadadosAutorizacao(w http.ResponseWriter, _ *http.Request) {
	escreverJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         s.Origem,
		"authorization_endpoint":                         s.Origem + "/mcp/oauth/authorize",
		"token_endpoint":                                 s.Origem + "/mcp/oauth/token",
		"registration_endpoint":                          s.Origem + "/mcp/oauth/register",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none", "client_secret_post", "client_secret_basic"},
		"scopes_supported":                               []string{Escopo},
		"authorization_response_iss_parameter_supported": true,
	})
}
