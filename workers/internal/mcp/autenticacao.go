package mcp

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/mcp/auditoria"
	"radiocheck/internal/mcp/oauth"
	"radiocheck/internal/mcp/tokens"
	"radiocheck/internal/reqmetrics"
)

const realm = "emonitor-mcp"

var errRecusada = errors.New("mcp: credencial recusada")

// Autenticador protege o /mcp: bearer → credencial viva → admin ativo, relido
// do banco a cada pedido (spec D4). O token do MCP só abre esta porta.
type Autenticador struct {
	Tokens       *tokens.Store
	URLMetadados string
	// PorToken: 240/min pela hash do bearer, antes de qualquer leitura de banco.
	PorToken *auth.LoginRateLimiter
	// FalhasPorIP conta SÓ falhas: um token válido nunca é barrado por IP
	// (egress do claude.ai e NAT do escritório são compartilhados).
	FalhasPorIP *auth.LoginRateLimiter
	// DB grava no audit_log a recusa de um token que existe mas cujo dono
	// deixou de ser admin ativo.
	DB auditoria.Execer
	// AuditoriaRecusa: no máximo 1 linha de "mcp.token.recusado" por token a
	// cada 10 minutos — um cliente insistindo não inunda o audit_log.
	AuditoriaRecusa *auth.LoginRateLimiter
	Log             *zap.Logger
}

func NovoAutenticador(st *tokens.Store, db auditoria.Execer, urlMetadados string, log *zap.Logger) *Autenticador {
	return &Autenticador{
		Tokens:          st,
		URLMetadados:    urlMetadados,
		PorToken:        auth.NewLoginRateLimiter(240, time.Minute),
		FalhasPorIP:     auth.NewLoginRateLimiter(60, 15*time.Minute),
		DB:              db,
		AuditoriaRecusa: auth.NewLoginRateLimiter(1, 10*time.Minute),
		Log:             log,
	}
}

// ComMetadados devolve o mesmo autenticador apontando o WWW-Authenticate para outros metadados de
// recurso (o /mcp/programacao). Os limitadores são os MESMOS ponteiros: a cota de 240/min é do
// token, não do endereço — dois endereços não dobram a cota.
func (a *Autenticador) ComMetadados(url string) *Autenticador {
	b := *a
	b.URLMetadados = url
	return &b
}

func (a *Autenticador) resolver(r *http.Request, bearer string) (*Principal, error) {
	if !strings.HasPrefix(bearer, tokens.PrefixoAcesso) {
		return nil, errRecusada
	}
	ctx := r.Context()
	tok, err := a.Tokens.Buscar(ctx, bearer, tokens.TipoPessoal, tokens.TipoOAuthAcesso)
	if errors.Is(err, tokens.ErrNaoEncontrado) {
		return nil, errRecusada
	}
	if err != nil {
		return nil, err
	}
	dono, err := a.Tokens.DonoAdminAtivo(ctx, tok.UserID)
	if errors.Is(err, tokens.ErrNaoEncontrado) {
		// A credencial existe e está viva, mas o dono não é (mais) admin
		// ativo: deixa rastro, sem atrasar nem mudar o 401 se a escrita falhar.
		if a.AuditoriaRecusa.Allow(tok.ID.String()) {
			if err := auditoria.Registrar(ctx, a.DB, tok.UserID, "mcp.token.recusado", "mcp_token", &tok.ID, nil); err != nil {
				a.Log.Warn("mcp: auditoria token recusado", zap.Error(err))
			}
		}
		return nil, errRecusada
	}
	if err != nil {
		return nil, err
	}
	if err := a.Tokens.Tocar(ctx, tok.ID); err != nil {
		a.Log.Warn("mcp: registrar uso do token", zap.Error(err))
	}
	return &Principal{UserID: dono.ID, Email: dono.Email, Nome: dono.Nome, TokenID: tok.ID}, nil
}

func (a *Autenticador) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		campos := strings.Fields(r.Header.Get("Authorization"))
		if len(campos) != 2 || !strings.EqualFold(campos[0], "Bearer") {
			a.negar(w, false)
			return
		}
		bearer := campos[1]
		if !a.PorToken.Allow(a.Tokens.Hash(bearer)) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "cota do MCP estourada: 240 chamadas por minuto por credencial", http.StatusTooManyRequests)
			return
		}
		p, err := a.resolver(r, bearer)
		switch {
		case errors.Is(err, errRecusada):
			if !a.FalhasPorIP.Allow(oauth.IPDe(r)) {
				w.Header().Set("Retry-After", "900")
				http.Error(w, "muitas credenciais recusadas deste IP", http.StatusTooManyRequests)
				return
			}
			a.negar(w, true)
			return
		case err != nil:
			a.Log.Error("mcp: autenticar", zap.Error(err))
			http.Error(w, "erro interno", http.StatusInternalServerError)
			return
		}
		// O /admin/monitoring atribui o tráfego do MCP ao admin. Sem isso, o
		// egress do claude.ai apareceria como IP anônimo — e um operador que
		// o banisse derrubaria o MCP de todo mundo.
		reqmetrics.SetUserID(r.Context(), p.UserID)
		next.ServeHTTP(w, r.WithContext(ComPrincipal(r.Context(), p)))
	})
}

// negar: 401 com WWW-Authenticate do contrato §3.4 — sem error= quando não
// veio credencial (é o primeiro passo da descoberta OAuth, não uma falha).
func (a *Autenticador) negar(w http.ResponseWriter, recusada bool) {
	partes := []string{fmt.Sprintf("realm=%q", realm)}
	if recusada {
		partes = append(partes, `error="invalid_token"`)
	}
	partes = append(partes,
		fmt.Sprintf("resource_metadata=%q", a.URLMetadados),
		fmt.Sprintf("scope=%q", oauth.Escopo))
	w.Header().Set("WWW-Authenticate", "Bearer "+strings.Join(partes, ", "))
	http.Error(w, "credencial do MCP ausente ou recusada", http.StatusUnauthorized)
}
