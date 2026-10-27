package mcp

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/mcp/oauth"
	"radiocheck/internal/mcp/tokens"
)

type DepsModulo struct {
	Pool    *pgxpool.Pool
	Segredo []byte // JWT_SECRET
	Origem  string // MCP_ORIGEM_PUBLICA, ex. https://api.e-monitor.online
	Portal  string // NOTIFICATIONS_BASE_URL, ex. https://e-monitor.online
	Versao  string
	Log     *zap.Logger
}

type Modulo struct {
	mcp    http.Handler
	oauth  *oauth.Servidor
	tokens *TokensHTTP
}

// NovoModulo devolve nil (e loga) se o segredo for curto: sem segredo os
// hashes não valem nada, e um panic no boot derrubaria o deploy (regra 6.5).
func NovoModulo(d DepsModulo) *Modulo {
	if len(d.Segredo) < 32 {
		d.Log.Error("mcp: JWT_SECRET ausente ou curto — /mcp e OAuth NÃO montados")
		return nil
	}
	st := tokens.NovoStore(d.Pool, d.Segredo)
	o := oauth.NovoServidor(d.Pool, st, d.Segredo, d.Origem, d.Portal, d.Log)
	a := NovoAutenticador(st, o.URLMetadadosRecurso(), d.Log)
	return &Modulo{
		mcp:    SoPost(a.Middleware(NovoHandlerMCP(ConfigServidor{Versao: d.Versao}))),
		oauth:  o,
		tokens: &TokensHTTP{Tokens: st, DB: d.Pool, Log: d.Log},
	}
}

// MontarPublico: rotas sem JWT do portal (o MCP tem a própria autenticação).
func (m *Modulo) MontarPublico(r chi.Router) {
	r.Handle("/mcp", m.mcp)
	r.Get("/.well-known/oauth-protected-resource", m.oauth.MetadadosRecurso)
	r.Get("/.well-known/oauth-protected-resource/mcp", m.oauth.MetadadosRecurso)
	r.Get("/.well-known/oauth-authorization-server", m.oauth.MetadadosAutorizacao)
	r.Post("/mcp/oauth/register", m.oauth.Registrar)
	r.Get("/mcp/oauth/authorize", m.oauth.Autorizar)
	r.Post("/mcp/oauth/token", m.oauth.Token)
}

// MontarAdmin: dentro de /v1/internal, sob RequireJWTAtivo + RequireRole("admin").
func (m *Modulo) MontarAdmin(r chi.Router) {
	r.Get("/mcp/tokens", m.tokens.Listar)
	r.Post("/mcp/tokens", m.tokens.Criar)
	r.Delete("/mcp/tokens/{id}", m.tokens.Revogar)
	r.Get("/mcp/oauth/pedido", m.oauth.LerPedido)
	r.Post("/mcp/oauth/grant", m.oauth.Conceder)
}
