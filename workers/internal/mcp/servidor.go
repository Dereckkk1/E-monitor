package mcp

import (
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
)

// ToolsEsperadas trava o conjunto e a ORDEM das tools: clientes guardam o
// tools/list em cache, então nada entra ou sai por acidente (contrato §3).
// Toda fatia que acrescentar tool atualiza esta lista no mesmo commit.
var ToolsEsperadas = []string{
	"quem_sou_eu",
}

type ConfigServidor struct {
	Versao string
	Log    *zap.Logger
}

// novoServidor monta um Server por pedido (stateless): o principal entra por
// closure em cada tool, sem estado entre pedidos.
func novoServidor(cfg ConfigServidor, p *Principal) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "emonitor", Version: cfg.Versao},
		&sdk.ServerOptions{Instructions: Instrucoes})
	registrarSistema(s, p)
	return s
}

// NovoHandlerMCP serve Streamable HTTP sem sessão e com resposta JSON
// (spec D2). Espera o Principal no contexto (ver Autenticador.Middleware).
func NovoHandlerMCP(cfg ConfigServidor) http.Handler {
	return sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		p, _ := PrincipalDe(r.Context())
		return novoServidor(cfg, p)
	}, &sdk.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// Spec D10: em prod o cloudflared fala com a API por localhost:8080
		// com Host api.e-monitor.online; a proteção anti-DNS-rebinding do
		// SDK devolveria 403 a todo pedido. Todo pedido ao /mcp exige bearer,
		// então rebinding não ganha nada. Coberto por
		// TestServidor_HostPublicoPorLoopbackNaoLeva403.
		DisableLocalhostProtection: true,
	})
}

// SoPost recusa GET/DELETE com 405 (contrato §3.1: sem SSE, sem sessão) e
// limita o corpo a 1 MB. Vem ANTES da autenticação: um GET sem bearer tem
// de dar 405, não 401.
func SoPost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "o MCP do E-monitor é sem sessão: só POST", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}
