package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/mcp/tokens"
)

const redirectClaude = "https://claude.ai/api/mcp/auth_callback"

type ambiente struct {
	t     *testing.T
	ctx   context.Context
	pool  *pgxpool.Pool
	srv   *Servidor
	http  *httptest.Server
	admin uuid.UUID
	jwt   string
}

func novoAmbiente(t *testing.T) *ambiente {
	t.Setenv("JWT_SECRET", mcptest.SegredoTeste)
	ctx, pool := mcptest.DB(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	jwt, err := auth.IssueToken(admin, "admin")
	require.NoError(t, err)
	segredo := []byte(mcptest.SegredoTeste)
	srv := NovoServidor(pool, tokens.NovoStore(pool, segredo), segredo, "https://api.teste", "https://portal.teste", zap.NewNop())

	r := chi.NewRouter()
	r.Get("/.well-known/oauth-protected-resource/mcp", srv.MetadadosRecurso)
	r.Get("/.well-known/oauth-authorization-server", srv.MetadadosAutorizacao)
	r.Post("/mcp/oauth/register", srv.Registrar)
	r.Get("/mcp/oauth/authorize", srv.Autorizar)
	r.Post("/mcp/oauth/token", srv.Token)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireJWT, auth.RequireRole("admin"))
		r.Get("/v1/internal/mcp/oauth/pedido", srv.LerPedido)
		r.Post("/v1/internal/mcp/oauth/grant", srv.Conceder)
	})
	hs := httptest.NewServer(r)
	t.Cleanup(hs.Close)
	return &ambiente{t: t, ctx: ctx, pool: pool, srv: srv, http: hs, admin: admin, jwt: jwt}
}

func (a *ambiente) postJSON(caminho, jwt string, corpo any) (int, map[string]any) {
	b, _ := json.Marshal(corpo)
	req, _ := http.NewRequest(http.MethodPost, a.http.URL+caminho, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	return a.fazer(req)
}

func (a *ambiente) get(caminho, jwt string) (int, map[string]any) {
	req, _ := http.NewRequest(http.MethodGet, a.http.URL+caminho, nil)
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	return a.fazer(req)
}

func (a *ambiente) fazer(req *http.Request) (int, map[string]any) {
	res, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)
	defer res.Body.Close()
	bruto, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(bruto, &m)
	return res.StatusCode, m
}

// token faz POST form no /token; basic, se não vazio, é [id, segredo].
func (a *ambiente) token(form url.Values, basic ...string) (int, map[string]any) {
	return a.fazer(a.pedidoToken(form, basic...))
}

// pedidoToken monta o POST do /token (para quem precisa ler cabeçalhos).
func (a *ambiente) pedidoToken(form url.Values, basic ...string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, a.http.URL+"/mcp/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if len(basic) == 2 {
		req.SetBasicAuth(url.QueryEscape(basic[0]), url.QueryEscape(basic[1]))
	}
	return req
}

// ativar faz de conta que o cliente já chegou ao /token (first_token_at):
// só então o /authorize devolve erro por redirect.
func (a *ambiente) ativar(clientID string) {
	_, err := a.pool.Exec(a.ctx, `UPDATE mcp_oauth_clients SET first_token_at = NOW() WHERE client_id = $1`, clientID)
	require.NoError(a.t, err)
}

// contar devolve um COUNT(*) qualquer.
func (a *ambiente) contar(sql string, args ...any) int {
	var n int
	require.NoError(a.t, a.pool.QueryRow(a.ctx, sql, args...).Scan(&n))
	return n
}

// autorizar chama o /authorize sem seguir o redirect.
func (a *ambiente) autorizar(params url.Values) *http.Response {
	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := cli.Get(a.http.URL + "/mcp/oauth/authorize?" + params.Encode())
	require.NoError(a.t, err)
	res.Body.Close()
	return res
}

func (a *ambiente) registrar(corpo map[string]any) (int, map[string]any) {
	return a.postJSON("/mcp/oauth/register", "", corpo)
}

// conectar faz registro público → authorize → consentimento e devolve
// client_id e o code. Usa o verifier do RFC 7636.
func (a *ambiente) conectar() (clientID, code string) {
	st, reg := a.registrar(map[string]any{"client_name": "Claude", "redirect_uris": []string{redirectClaude}})
	require.Equal(a.t, http.StatusCreated, st)
	clientID = reg["client_id"].(string)
	res := a.autorizar(url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectClaude},
		"code_challenge": {challengeRFC}, "code_challenge_method": {"S256"}, "state": {"st-1"},
		"resource": {"https://api.teste/mcp"},
	})
	require.Equal(a.t, http.StatusFound, res.StatusCode)
	loc, _ := url.Parse(res.Header.Get("Location"))
	st, out := a.postJSON("/v1/internal/mcp/oauth/grant", a.jwt, map[string]any{"pedido": loc.Query().Get("pedido"), "aprovar": true})
	require.Equal(a.t, http.StatusOK, st)
	volta, _ := url.Parse(out["redirect"].(string))
	return clientID, volta.Query().Get("code")
}
