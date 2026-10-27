package oauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode"

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

// Registrar — RFC 7591, aberto e só para cliente PÚBLICO: um client_id
// sozinho não dá acesso a nada; quem concede é o admin no /autorizar-ia.
// Confidencial (Gemini Enterprise, que não faz PKCE) é cadastrado pelo
// administrador com registra-cliente-mcp: aberto, ele ficava 7 dias pendente e
// enchia o teto de pendentes, travando toda conexão nova.
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
	switch c.TokenEndpointAuthMethod {
	case "", "none":
	case "client_secret_post", "client_secret_basic":
		erroOAuth(w, http.StatusBadRequest, "invalid_client_metadata",
			"cliente confidencial é cadastrado pelo administrador: registra-cliente-mcp na VM")
		return
	default:
		erroOAuth(w, http.StatusBadRequest, "invalid_client_metadata", "token_endpoint_auth_method não suportado: "+c.TokenEndpointAuthMethod)
		return
	}
	if len(c.RedirectURIs) == 0 || len(c.RedirectURIs) > maxRedirectURIs {
		erroOAuth(w, http.StatusBadRequest, "invalid_redirect_uri", fmt.Sprintf("informe de 1 a %d redirect_uris", maxRedirectURIs))
		return
	}
	uris := make([]string, len(c.RedirectURIs))
	for i, u := range c.RedirectURIs {
		if len(u) > maxTamanhoRedirect {
			erroOAuth(w, http.StatusBadRequest, "invalid_redirect_uri", fmt.Sprintf("redirect_uri com mais de %d bytes", maxTamanhoRedirect))
			return
		}
		// Scheme e host em minúsculas antes de validar e de guardar; path e
		// query ficam como vieram.
		n := normalizarOrigem(u)
		if !redirectValido(n) {
			erroOAuth(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri precisa ser https (http só em loopback) e sem fragmento: "+u)
			return
		}
		if !s.destinoPermitido(n) {
			erroOAuth(w, http.StatusBadRequest, "invalid_redirect_uri", fmt.Sprintf(
				"redirect_uri fora dos callbacks permitidos (%s; http em localhost/127.0.0.1/::1 vale sempre). "+
					"Para liberar outro, o operador do E-monitor inclui o callback exato em MCP_REDIRECT_URIS.",
				strings.Join(s.RedirectsPermitidos, ", ")))
			return
		}
		uris[i] = n
	}
	cli, despejados, err := s.Clientes.Registrar(r.Context(), nomeCliente(c.ClientName), uris, s.MaxClientesPendentes)
	if err != nil {
		s.Log.Error("mcp oauth: registrar cliente", zapErr(err))
		erroOAuth(w, http.StatusInternalServerError, "server_error", "falha ao registrar")
		return
	}
	if despejados > 0 {
		s.Log.Warn("mcp oauth: teto de clientes pendentes cheio; os públicos pendentes mais antigos foram despejados",
			zap.Int64("despejados", despejados), zap.Int("teto", s.MaxClientesPendentes))
	}
	escreverJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  cli.ClientID,
		"client_name":                cli.Nome,
		"redirect_uris":              cli.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"client_id_issued_at":        s.agora().Unix(),
	})
}

// destinoPermitido aplica a lista de callbacks a um redirect_uri já
// normalizado e aprovado no redirectValido: http ali já é loopback e vale
// sempre; https só se for EXATAMENTE um de RedirectsPermitidos.
func (s *Servidor) destinoPermitido(normal string) bool {
	u, err := url.Parse(normal)
	if err != nil {
		return false
	}
	if u.Scheme == "http" {
		return ehLoopback(u.Hostname())
	}
	return u.Scheme == "https" && slices.Contains(s.RedirectsPermitidos, normal)
}

// nomeCliente prepara o client_name para a tela de consentimento: sem
// caracteres de controle/formatação, sem espaços nas pontas, até 100 runas.
func nomeCliente(bruto string) string {
	nome := strings.TrimSpace(limparNome(bruto))
	if nome == "" {
		nome = "Assistente sem nome"
	}
	if r := []rune(nome); len(r) > 100 {
		nome = string(r[:100])
	}
	return nome
}

// limparNome tira caracteres de controle e de formatação (Unicode Cc e Cf —
// U+202E inverte o texto na tela de consentimento, U+200B some) do
// client_name, que o admin lê antes de autorizar.
func limparNome(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.In(r, unicode.Cc, unicode.Cf) {
			return -1
		}
		return r
	}, s)
}
