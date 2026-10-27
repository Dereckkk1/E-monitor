package oauth

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Autorizar valida o pedido OAuth e manda o navegador para o consentimento
// no portal. Erro antes de o redirect_uri ser confiável vira página 400
// (nunca redireciona para um destino não registrado); depois, volta ao
// cliente com error=…&state=…&iss=… (RFC 9207) — se o cliente já é conhecido.
func (s *Servidor) Autorizar(w http.ResponseWriter, r *http.Request) {
	// Lição do hub (b77b344e): o COOP same-origin separava o pop-up do Gemini
	// Enterprise do window.opener e o código nunca chegava ao /token.
	w.Header().Set("Cross-Origin-Opener-Policy", "unsafe-none")
	q := r.URL.Query()

	cli, err := s.Clientes.Buscar(r.Context(), q.Get("client_id"))
	if errors.Is(err, ErrClienteDesconhecido) {
		http.Error(w, "Cliente OAuth desconhecido. Remova o conector no assistente e adicione de novo.", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.Log.Error("mcp oauth: buscar cliente no authorize", zapErr(err))
		http.Error(w, "Erro interno.", http.StatusInternalServerError)
		return
	}
	redirect := q.Get("redirect_uri")
	if !redirectRegistrado(cli.RedirectURIs, redirect) {
		http.Error(w, "redirect_uri não registrado para este cliente.", http.StatusBadRequest)
		return
	}
	state := q.Get("state")
	falhar := func(codigo, descricao string) {
		// RFC 9700 §4.11.2: o DCR é aberto, então um cliente que nunca chegou
		// ao /token pode ter sido registrado por qualquer um só para usar este
		// erro como open redirect com a nossa origem na frente. Para ele, a
		// página; o redirect de erro fica para quem já obteve token. Vale para
		// confidencial também: registrar um confidencial é tão aberto quanto.
		if !cli.Ativado {
			http.Error(w, "Pedido de autorização inválido ("+codigo+"): "+descricao, http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, comParametros(redirect, map[string]string{
			"error": codigo, "error_description": descricao, "state": state, "iss": s.Origem,
		}), http.StatusFound)
	}

	if q.Get("response_type") != "code" {
		falhar("unsupported_response_type", "só response_type=code")
		return
	}
	desafio, metodo := q.Get("code_challenge"), q.Get("code_challenge_method")
	switch {
	case (desafio == "") != (metodo == ""):
		falhar("invalid_request", "PKCE pela metade: code_challenge e code_challenge_method vêm juntos")
		return
	case metodo != "" && metodo != "S256":
		falhar("invalid_request", "só PKCE S256")
		return
	case desafio != "" && !reChallenge.MatchString(desafio):
		falhar("invalid_request", "code_challenge malformado")
		return
	case desafio == "" && !cli.Confidencial:
		falhar("invalid_request", "PKCE S256 obrigatório para cliente público")
		return
	}
	if rec := q.Get("resource"); rec != "" && normalizarRecurso(rec) != normalizarRecurso(s.URLRecurso()) {
		falhar("invalid_target", "resource deve ser "+s.URLRecurso())
		return
	}

	pedido := assinarPedido(s.Segredo, Pedido{
		ClientID: cli.ClientID, RedirectURI: redirect, State: state, Desafio: desafio,
		Exp: s.agora().Add(10 * time.Minute).Unix(),
	})
	http.Redirect(w, r, s.Portal+"/autorizar-ia?pedido="+url.QueryEscape(pedido), http.StatusFound)
}

// normalizarRecurso põe scheme e host em minúsculas e tira UMA barra final do
// path (RFC 3986 §6.2.2.1 / RFC 8707): clientes mandam https://API.x/mcp/ e é
// o mesmo recurso. O path continua sensível a maiúsculas.
func normalizarRecurso(bruto string) string {
	u, err := url.Parse(bruto)
	if err != nil {
		return bruto
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawPath = strings.TrimSuffix(u.RawPath, "/")
	return u.String()
}
