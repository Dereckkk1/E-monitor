package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// Tetos do registro: cada cliente é uma linha que fica até a faxina (1 ou 7
// dias); sem eles, um registro só já levaria ~60 KB de redirect_uris.
const (
	maxRedirectURIs    = 5
	maxTamanhoRedirect = 2048
)

type corpoRegistro struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// Registrar — RFC 7591, aberto: um client_id sozinho não dá acesso a nada;
// quem concede é o admin no /autorizar-ia. Confidencial (client_secret_post
// ou _basic) é o caminho do Gemini Enterprise, que não faz PKCE.
func (s *Servidor) Registrar(w http.ResponseWriter, r *http.Request) {
	if !s.Registros.Allow(IPDe(r)) {
		w.Header().Set("Retry-After", "3600")
		erroOAuth(w, http.StatusTooManyRequests, "too_many_requests", "limite de 20 registros por hora por IP")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var c corpoRegistro
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		erroOAuth(w, http.StatusBadRequest, "invalid_client_metadata", "corpo JSON inválido")
		return
	}
	if len(c.RedirectURIs) == 0 || len(c.RedirectURIs) > maxRedirectURIs {
		erroOAuth(w, http.StatusBadRequest, "invalid_redirect_uri", fmt.Sprintf("informe de 1 a %d redirect_uris", maxRedirectURIs))
		return
	}
	for _, u := range c.RedirectURIs {
		if len(u) > maxTamanhoRedirect {
			erroOAuth(w, http.StatusBadRequest, "invalid_redirect_uri", fmt.Sprintf("redirect_uri com mais de %d bytes", maxTamanhoRedirect))
			return
		}
		if !redirectValido(u) {
			erroOAuth(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri precisa ser https (http só em loopback) e sem fragmento: "+u)
			return
		}
	}
	metodo := c.TokenEndpointAuthMethod
	confidencial := false
	switch metodo {
	case "", "none":
		metodo = "none"
	case "client_secret_post", "client_secret_basic":
		confidencial = true
	default:
		erroOAuth(w, http.StatusBadRequest, "invalid_client_metadata", "token_endpoint_auth_method não suportado: "+metodo)
		return
	}
	nome := strings.TrimSpace(c.ClientName)
	if nome == "" {
		nome = "Assistente sem nome"
	}
	if r := []rune(nome); len(r) > 100 {
		nome = string(r[:100])
	}
	cli, segredo, err := s.Clientes.Registrar(r.Context(), nome, c.RedirectURIs, confidencial, s.MaxClientesPendentes)
	if errors.Is(err, ErrMuitosPendentes) {
		s.Log.Warn("mcp oauth: teto de clientes pendentes atingido, registro recusado",
			zap.Int("teto", s.MaxClientesPendentes))
		erroOAuth(w, http.StatusServiceUnavailable, "temporarily_unavailable", "registro indisponível no momento; tente mais tarde")
		return
	}
	if err != nil {
		s.Log.Error("mcp oauth: registrar cliente", zapErr(err))
		erroOAuth(w, http.StatusInternalServerError, "server_error", "falha ao registrar")
		return
	}
	resp := map[string]any{
		"client_id":                  cli.ClientID,
		"client_name":                cli.Nome,
		"redirect_uris":              cli.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": metodo,
		"client_id_issued_at":        s.agora().Unix(),
	}
	if confidencial {
		resp["client_secret"] = segredo
		resp["client_secret_expires_at"] = 0
	}
	escreverJSON(w, http.StatusCreated, resp)
}
