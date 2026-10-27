package oauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"radiocheck/internal/auth"
	"radiocheck/internal/mcp/auditoria"
	"radiocheck/internal/mcp/tokens"
)

const msgPedidoInvalido = "Pedido de autorização inválido ou vencido (vale 10 minutos). Volte ao assistente e conecte de novo."

// LerPedido (GET /v1/internal/mcp/oauth/pedido?p=…, JWT admin) diz à tela
// /autorizar-ia quem está pedindo acesso.
func (s *Servidor) LerPedido(w http.ResponseWriter, r *http.Request) {
	p, err := lerPedido(s.Segredo, r.URL.Query().Get("p"), s.agora())
	if err != nil {
		erroPortal(w, http.StatusBadRequest, msgPedidoInvalido)
		return
	}
	cli, err := s.Clientes.Buscar(r.Context(), p.ClientID)
	if err != nil {
		erroPortal(w, http.StatusBadRequest, msgPedidoInvalido)
		return
	}
	destino, _ := url.Parse(p.RedirectURI)
	escreverJSON(w, http.StatusOK, map[string]any{
		"cliente":   cli.Nome,
		"destino":   destino.Host,
		"expira_em": time.Unix(p.Exp, 0).UTC(),
	})
}

type corpoConceder struct {
	Pedido  string `json:"pedido"`
	Aprovar bool   `json:"aprovar"`
}

// Conceder (POST /v1/internal/mcp/oauth/grant, JWT admin) emite o código —
// ou o access_denied — e devolve a URL para a tela mandar o navegador.
func (s *Servidor) Conceder(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		erroPortal(w, http.StatusUnauthorized, "sessão ausente")
		return
	}
	var c corpoConceder
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&c); err != nil {
		erroPortal(w, http.StatusBadRequest, "corpo inválido")
		return
	}
	p, err := lerPedido(s.Segredo, c.Pedido, s.agora())
	if err != nil {
		erroPortal(w, http.StatusBadRequest, msgPedidoInvalido)
		return
	}
	cli, err := s.Clientes.Buscar(r.Context(), p.ClientID)
	if err != nil {
		erroPortal(w, http.StatusBadRequest, msgPedidoInvalido)
		return
	}
	volta := map[string]string{"state": p.State, "iss": s.Origem}
	if !c.Aprovar {
		volta["error"] = "access_denied"
		volta["error_description"] = "a pessoa recusou o acesso"
		escreverJSON(w, http.StatusOK, map[string]string{"redirect": comParametros(p.RedirectURI, volta)})
		return
	}
	// O JWT do portal vale 8h; a rota está sob RequireRole("admin"), mas a
	// pessoa pode ter sido rebaixada desde então — relê.
	if _, err := s.Tokens.DonoAdminAtivo(r.Context(), claims.UserID); err != nil {
		if errors.Is(err, tokens.ErrNaoEncontrado) {
			erroPortal(w, http.StatusForbidden, "só administrador ativo pode conectar o MCP do E-monitor")
			return
		}
		s.Log.Error("mcp oauth: reler dono no grant", zapErr(err))
		erroPortal(w, http.StatusInternalServerError, "erro interno")
		return
	}
	var desafio *string
	if p.Desafio != "" {
		desafio = &p.Desafio
	}
	codigo, err := s.Codigos.Criar(r.Context(), Codigo{ClientID: cli.ClientID, UserID: claims.UserID, RedirectURI: p.RedirectURI, Desafio: desafio})
	if err != nil {
		s.Log.Error("mcp oauth: criar código", zapErr(err))
		erroPortal(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if err := auditoria.Registrar(r.Context(), s.Pool, claims.UserID, "mcp.oauth.autorizado", "mcp_oauth_client", nil,
		map[string]any{"client_id": cli.ClientID, "cliente": cli.Nome}); err != nil {
		s.Log.Warn("mcp oauth: auditoria do grant", zapErr(err))
	}
	volta["code"] = codigo
	escreverJSON(w, http.StatusOK, map[string]string{"redirect": comParametros(p.RedirectURI, volta)})
}
