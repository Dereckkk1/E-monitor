package oauth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/mcp/tokens"
)

const (
	Escopo          = "emonitor:all"
	validadeAcesso  = 30 * 24 * time.Hour
	validadeRefresh = 90 * 24 * time.Hour
)

type Servidor struct {
	Pool      *pgxpool.Pool
	Tokens    *tokens.Store
	Clientes  *Clientes
	Codigos   *Codigos
	Segredo   []byte // JWT_SECRET: assina o pedido e salga os hashes
	Origem    string // origem pública da API, ex. https://api.e-monitor.online
	Portal    string // origem do frontend, ex. https://e-monitor.online
	Registros *auth.LoginRateLimiter
	// MaxClientesPendentes é o teto de clientes públicos registrados que nunca
	// chegaram ao /token. O DCR é aberto e o rate limit é por IP; quem tem
	// muitos IPs ainda encheria a tabela. Cheio, o pendente público mais
	// antigo é despejado (ver Clientes.Registrar).
	MaxClientesPendentes int
	// RedirectsPermitidos: callbacks https aceitos no DCR, comparados EXATOS
	// (scheme e host em minúsculas; path, query e porta como estão). Loopback
	// http vale sempre, em qualquer porta.
	RedirectsPermitidos []string
	Log                 *zap.Logger
	agora               func() time.Time
}

// NovoServidor monta o authorization server. Quem chama garante que segredo
// (o JWT_SECRET) tem pelo menos 32 bytes — o NovoModulo confere isso antes;
// aqui ele assina o pedido e salga os hashes, e não há segunda checagem.
func NovoServidor(pool *pgxpool.Pool, store *tokens.Store, segredo []byte, origem, portal string, log *zap.Logger) *Servidor {
	return &Servidor{
		Pool:                 pool,
		Tokens:               store,
		Clientes:             NovosClientes(pool, segredo),
		Codigos:              &Codigos{db: pool, segredo: segredo},
		Segredo:              segredo,
		Origem:               origem,
		Portal:               portal,
		Registros:            auth.NewLoginRateLimiter(20, time.Hour), // contrato §5: 20 DCR/h por IP
		MaxClientesPendentes: 500,
		RedirectsPermitidos:  redirectsDoAmbiente(log),
		Log:                  log,
		agora:                time.Now,
	}
}

// redirectsPadrao são os callbacks aceitos no DCR quando MCP_REDIRECT_URIS
// não está definido. O DCR é aberto; sem esta lista, qualquer um registraria
// um cliente com nome bonito e redirect próprio e mandaria o link do
// /authorize para um admin (phishing de consentimento). Callback exato, e não
// host: o path e a query também ficam fora do alcance de quem registra.
var redirectsPadrao = []string{
	"https://claude.ai/api/mcp/auth_callback",  // conector do Claude (web, desktop e mobile)
	"https://claude.com/api/mcp/auth_callback", // o mesmo callback no domínio novo da Anthropic
	// Claude Code usa http em loopback, aceito sempre, fora desta lista.
	// Gemini Enterprise é confidencial e não passa pelo DCR: registra-cliente-mcp.
}

// redirectsDoAmbiente lê MCP_REDIRECT_URIS (separado por vírgula). Vazio, ou
// só vírgulas, cai no padrão. Entrada que não seja URL https válida (com
// host, sem userinfo nem fragmento) cai fora com aviso — se todas caírem, a
// lista fica vazia (só loopback), e não volta ao padrão: falha fechada.
func redirectsDoAmbiente(log *zap.Logger) []string {
	var entradas []string
	for _, e := range strings.Split(os.Getenv("MCP_REDIRECT_URIS"), ",") {
		if e = strings.TrimSpace(e); e != "" {
			entradas = append(entradas, e)
		}
	}
	if len(entradas) == 0 {
		return slices.Clone(redirectsPadrao)
	}
	validas := []string{}
	for _, e := range entradas {
		n := normalizarOrigem(e)
		if u, err := url.Parse(n); err != nil || u.Scheme != "https" || !redirectValido(n) {
			log.Warn("mcp oauth: MCP_REDIRECT_URIS ignora entrada que não é URL https válida", zap.String("entrada", e))
			continue
		}
		validas = append(validas, n)
	}
	return validas
}

func (s *Servidor) URLRecurso() string { return s.Origem + "/mcp" }
func (s *Servidor) URLMetadadosRecurso() string {
	return s.Origem + "/.well-known/oauth-protected-resource/mcp"
}

func escreverJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// erroOAuth segue o RFC 6749 §5.2.
func erroOAuth(w http.ResponseWriter, status int, codigo, descricao string) {
	escreverJSON(w, status, map[string]string{"error": codigo, "error_description": descricao})
}

// erroPortal é o formato de erro das rotas /v1/internal (o front lê .error).
func erroPortal(w http.ResponseWriter, status int, msg string) {
	escreverJSON(w, status, map[string]string{"error": msg})
}

func zapErr(err error) zap.Field { return zap.Error(err) }
