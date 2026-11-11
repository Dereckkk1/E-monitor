package oauth

import "net/http"

// MetadadosRecurso — RFC 9728. Servido em /.well-known/oauth-protected-resource
// e em /.well-known/oauth-protected-resource/mcp (clientes tentam os dois).
func (s *Servidor) MetadadosRecurso(w http.ResponseWriter, _ *http.Request) {
	s.escreverMetadadosRecurso(w, s.URLRecurso(), "E-monitor")
}

// MetadadosRecursoProgramacao — RFC 9728 do perfil /mcp/programacao. O `resource` tem de ser o
// endereço que o cliente pediu (clientes conferem), por isso é um documento à parte.
func (s *Servidor) MetadadosRecursoProgramacao(w http.ResponseWriter, _ *http.Request) {
	s.escreverMetadadosRecurso(w, s.URLRecursoProgramacao(), "E-monitor (programação de campanhas)")
}

func (s *Servidor) escreverMetadadosRecurso(w http.ResponseWriter, recurso, nome string) {
	escreverJSON(w, http.StatusOK, map[string]any{
		"resource":                 recurso,
		"authorization_servers":    []string{s.Origem},
		"scopes_supported":         []string{Escopo},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            nome,
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
