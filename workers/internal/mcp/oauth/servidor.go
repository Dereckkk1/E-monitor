package oauth

import (
	"encoding/json"
	"net/http"
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
	// MaxClientesPendentes é o teto de clientes registrados que nunca chegaram
	// ao /token. O DCR é aberto e o rate limit é por IP; quem tem muitos IPs
	// ainda encheria a tabela sem este teto.
	MaxClientesPendentes int
	Log                  *zap.Logger
	agora                func() time.Time
}

// NovoServidor monta o authorization server. Quem chama garante que segredo
// (o JWT_SECRET) tem pelo menos 32 bytes — o NovoModulo confere isso antes;
// aqui ele assina o pedido e salga os hashes, e não há segunda checagem.
func NovoServidor(pool *pgxpool.Pool, store *tokens.Store, segredo []byte, origem, portal string, log *zap.Logger) *Servidor {
	return &Servidor{
		Pool:                 pool,
		Tokens:               store,
		Clientes:             &Clientes{db: pool, segredo: segredo},
		Codigos:              &Codigos{db: pool, segredo: segredo},
		Segredo:              segredo,
		Origem:               origem,
		Portal:               portal,
		Registros:            auth.NewLoginRateLimiter(20, time.Hour), // contrato §5: 20 DCR/h por IP
		MaxClientesPendentes: 500,
		Log:                  log,
		agora:                time.Now,
	}
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
