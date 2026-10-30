package mcp

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
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
	// Workers: o supervisor (para "monitorando agora" da visao_gerencial). Pode ser nil.
	Workers ListaWorkers
	// Supervisor: o supervisor de workers que as escritas de campanha e material
	// acionam (troca de emissoras, reload). Pode ser nil — mesma regra dos
	// handlers: sem supervisor, a escrita só grava no banco. Passe nil de
	// verdade, não um *supervisor.Supervisor nil dentro da interface.
	Supervisor acoes.SupervisorDeCampanha
	// Hub: o cliente do hub (campanha.upsert, cliente.upsert, conferência do
	// código). Pode ser nil ou não configurado: a escrita segue sem avisar.
	Hub *hub.Client
	// Ligacoes: a barreira da ligação cliente↔hub na escrita do código da
	// campanha. Pode ser nil.
	Ligacoes *catalog.Ligacoes
}

type Modulo struct {
	mcp http.Handler
	// cfg é a configuração com que o handler MCP foi montado (lida pelos testes
	// da ligação DepsModulo → Leitura).
	cfg    ConfigServidor
	oauth  *oauth.Servidor
	tokens *TokensHTTP
	// pendentesHTTP: as rotas de aprovação dos pedidos críticos (o executor
	// Aprovacoes é nil lá dentro se o índice das escritas falhou).
	pendentesHTTP *PendentesHTTP
	varredor      *Varredor
}

// IniciarVarredor roda o varredor dos pedidos e das confirmações até ctx cancelar.
// Bloqueia: chame com `go`. No-op sem pool.
func (m *Modulo) IniciarVarredor(ctx context.Context) {
	if m == nil || m.varredor == nil || m.varredor.DB == nil {
		return
	}
	m.varredor.Run(ctx)
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
	a := NovoAutenticador(st, d.Pool, o.URLMetadadosRecurso(), d.Log)
	L := NovaLeitura(d.Pool)
	L.Workers = d.Workers
	// Um *hub.Client nil dentro da interface passaria no `!= nil` das ações e
	// daria panic: só entra se existir.
	var aviso acoes.AvisoHub
	if d.Hub != nil {
		aviso = d.Hub
	}
	servico := acoes.NovoServico(d.Pool, d.Supervisor, aviso, d.Ligacoes)
	cfg := ConfigServidor{Versao: d.Versao, Portal: d.Portal, Leitura: L,
		Escritas: NovasEscritas(d.Pool, d.Segredo, servico, L, d.Log), Log: d.Log}
	// Índice quebrado não derruba o boot (regra 6.5): o módulo segue sem aprovações.
	apr, err := NovasAprovacoes(cfg)
	if err != nil {
		d.Log.Error("mcp: índice das escritas inválido — aprovações de pendentes NÃO montadas", zap.Error(err))
	}
	return &Modulo{
		pendentesHTTP: &PendentesHTTP{Tokens: st, Pendentes: cfg.Escritas.Pendentes, Aprovacoes: apr, Log: d.Log},
		varredor:      &Varredor{DB: d.Pool, Log: d.Log},
		mcp:           SoPost(a.Middleware(NovoHandlerMCP(cfg))),
		cfg:           cfg,
		oauth:         o,
		tokens:        &TokensHTTP{Tokens: st, DB: d.Pool, Log: d.Log},
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
	r.Get("/mcp/pendentes", m.pendentesHTTP.Listar)
	r.Post("/mcp/pendentes/{id}/aprovar", m.pendentesHTTP.Aprovar)
	r.Post("/mcp/pendentes/{id}/recusar", m.pendentesHTTP.Recusar)
}
