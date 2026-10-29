package mcp

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
)

// ToolsEsperadas trava o conjunto e a ORDEM das tools: clientes guardam o
// tools/list em cache, então nada entra ou sai por acidente (contrato §3).
// Toda fatia que acrescentar tool atualiza esta lista no mesmo commit.
// A ordem é ALFABÉTICA: é a que o SDK devolve em tools/list.
var ToolsEsperadas = []string{
	"assertividade",
	"auditoria_mcp",
	"buscar_emissoras",
	"criar_tipo_material",
	"destinatarios_pos_venda",
	"editar_material",
	"editar_tipo_material",
	"exportar_consolidado",
	"exportar_veiculacoes",
	"falhas_campanhas",
	"falhas_emissoras",
	"falhas_por_dia",
	"financeiro_campanhas",
	"grade_campanha",
	"insights_campanha",
	"listar_campanhas",
	"listar_chaves_api",
	"listar_clientes",
	"listar_entregas_webhook",
	"listar_materiais",
	"listar_materiais_campanha",
	"listar_overrides",
	"listar_pos_vendas",
	"listar_precos_campanha",
	"listar_regras_distribuicao",
	"listar_tipos_material",
	"listar_usuarios",
	"listar_veiculacoes",
	"mapa_cobertura_campanha",
	"marcar_notificacoes_lidas",
	"minhas_notificacoes",
	"obter_campanha",
	"obter_cliente",
	"obter_emissora",
	"obter_falhas_campanha",
	"obter_material",
	"obter_pos_venda",
	"obter_usuario",
	"obter_veiculacao",
	"obter_webhook_cliente",
	"plano_do_dia",
	"pmm_target_cliente",
	"quem_sou_eu",
	"reconhecer_similaridade_material",
	"relatorio_data_hora",
	"resumo_diario_falhas",
	"resumo_relatorio_campanha",
	"veiculacoes_por_material",
	"visao_gerencial",
}

type ConfigServidor struct {
	Versao string
	// Portal: origem do portal (NOTIFICATIONS_BASE_URL) para os links de tela.
	Portal string
	// Leitura: os repositórios das tools de leitura. nil no teste de
	// inventário, que só lista as tools (a chamada falha fechada).
	Leitura *Leitura
	// Escritas: as dependências das tools de escrita (motor de níveis, token de
	// confirmação, auditoria). nil falha fechado: a tool de escrita devolve
	// [interno] antes de qualquer coisa.
	Escritas *Escritas
	Log      *zap.Logger
}

func (c ConfigServidor) log() *zap.Logger {
	if c.Log == nil {
		return zap.NewNop()
	}
	return c.Log
}

// link devolve a URL de uma tela do portal, ou nil se a origem não estiver
// configurada (null = não se aplica).
func (c ConfigServidor) link(caminho string) *string {
	if c.Portal == "" {
		return nil
	}
	u := strings.TrimRight(c.Portal, "/") + caminho
	return &u
}

// cacheEsquemas guarda a inferência de JSON Schema das tools entre pedidos.
// O Server é reconstruído a cada pedido — e duas vezes: o SDK chama o
// getServer uma vez para ler as versões de protocolo e outra para servir —,
// então sem cache cada AddTool refaria a reflexão dos tipos a todo pedido.
var cacheEsquemas = sdk.NewSchemaCache()

// logSDK é o log do go-sdk (servidor e transporte). Vai para stderr, que é o
// log do container; só Warn+ para o tráfego normal não inundar o log.
var logSDK = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

// novoServidor monta um Server por pedido (stateless): o principal entra por
// closure em cada tool, sem estado entre pedidos.
func novoServidor(cfg ConfigServidor, p *Principal) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "emonitor", Version: cfg.Versao},
		&sdk.ServerOptions{Instructions: Instrucoes, SchemaCache: cacheEsquemas, Logger: logSDK})
	registrarSistema(s, p)
	registrarClientes(s, cfg)
	registrarCampanhas(s, cfg)
	registrarCampanhasDetalhe(s, cfg)
	registrarGrade(s, cfg)
	registrarMateriais(s, cfg)
	registrarEmissoras(s, cfg)
	registrarVeiculacoes(s, cfg)
	registrarUsuarios(s, cfg)
	registrarIntegracoes(s, cfg, p)
	registrarInsights(s, cfg)
	registrarGerencial(s, cfg)
	registrarRelatorios(s, cfg)
	registrarRelatoriosCampanha(s, cfg)
	registrarMapa(s, cfg)
	registrarFalhas(s, cfg)
	registrarFalhasCampanhas(s, cfg)
	registrarPosVenda(s, cfg)
	registrarAuditoria(s, cfg)
	registrarEscritasBasicas(s, cfg, p)
	return s
}

// NovoHandlerMCP serve Streamable HTTP sem sessão e com resposta JSON
// (spec D2). Espera o Principal no contexto (ver Autenticador.Middleware).
//
// Falha fechada: sem Principal no contexto o pedido é recusado ANTES de
// chegar ao SDK. Uma mudança no router que esqueça o Autenticador.Middleware
// não pode servir dado nenhum — e assim nenhuma tool precisa checar nil.
func NovoHandlerMCP(cfg ConfigServidor) http.Handler {
	sdkHandler := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
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
		Logger:                     logSDK,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := PrincipalDe(r.Context()); !ok {
			http.Error(w, "MCP sem autenticação montada", http.StatusInternalServerError)
			return
		}
		sdkHandler.ServeHTTP(w, r)
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
		// Recusa lote JSON-RPC (corpo que começa com '['). O go-sdk aceita
		// lotes no protocolo 2025-03-26 — o default quando o cliente não manda
		// Mcp-Protocol-Version —, o que deixaria UM pedido carregar centenas de
		// tools/call contra a cota de 240/min, que é por pedido. O corpo já é
		// limitado a 1 MiB, então lê inteiro e o devolve ao SDK.
		corpo, err := io.ReadAll(r.Body)
		if err != nil {
			var grande *http.MaxBytesError
			if errors.As(err, &grande) {
				http.Error(w, "corpo acima de 1 MiB", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "corpo ilegível", http.StatusBadRequest)
			return
		}
		if bytes.HasPrefix(bytes.TrimLeft(corpo, " \t\r\n"), []byte("[")) {
			http.Error(w, "lotes JSON-RPC não são aceitos: um pedido por vez (a cota de 240/min é por pedido)", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(corpo))
		next.ServeHTTP(w, r)
	})
}
