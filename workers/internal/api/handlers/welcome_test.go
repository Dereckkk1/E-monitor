package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/users"
	"radiocheck/internal/welcome"
)

// noopMailer aceita tudo sem falar SMTP.
type noopMailer struct{ sent int }

func (m *noopMailer) Send(context.Context, []string, string, string, string) error {
	m.sent++
	return nil
}

func newWelcomeSvc(t *testing.T, pool *pgxpool.Pool, mail *noopMailer) *welcome.Service {
	t.Helper()
	key := hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef")) // 32 bytes
	c, err := welcome.NewCipher(key)
	require.NoError(t, err)
	return welcome.New(welcome.Config{
		Repo:        welcome.NewRepo(pool),
		Cipher:      c,
		Mailer:      mail,
		MailEnabled: true,
		BaseURL:     "https://e-monitor.online",
		Log:         zap.NewNop(),
	})
}

// seedAdminCaller cria um admin REAL e devolve as claims dele. Diferente de
// newAdminCaller (UUID aleatório), isto reflete produção: created_by tem FK
// pra users, então o admin que cria o convite precisa existir de fato.
func seedAdminCaller(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *auth.Claims {
	t.Helper()
	u, err := users.NewRepo(pool).Create(ctx, users.CreateInput{
		Email:        "admin" + uuid.NewString()[:8] + "@hub.com",
		PasswordHash: "h", Role: "admin", Name: "Admin",
	})
	require.NoError(t, err)
	return &auth.Claims{UserID: u.ID, Role: "admin"}
}

func reqWithTokenParam(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/public/welcome/"+token, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// ── Criação com send_welcome ─────────────────────────────────────────────

func TestUsers_Create_SendWelcome(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	mail := &noopMailer{}
	svc := newWelcomeSvc(t, pool, mail)
	h := NewUsersHandler(users.NewRepo(pool), svc)
	caller := seedAdminCaller(t, ctx, pool)

	clients := catalog.NewClients(pool)
	c, err := clients.Create(ctx, catalog.CreateClientInput{Name: "Sofá & Cia"})
	require.NoError(t, err)

	body := `{"role":"client","client_id":"` + c.ID.String() + `","name":"Ana Souza",
	          "email":"ana@sofaecia.com.br","password":"senhaSuperSegura1","send_welcome":true}`
	req := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithClaims(req.Context(), caller))
	rec := httptest.NewRecorder()
	h.Create(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var resp struct {
		ID      uuid.UUID `json:"id"`
		Welcome *struct {
			InviteID    uuid.UUID `json:"invite_id"`
			Link        string    `json:"link"`
			EmailStatus string    `json:"email_status"`
			EmailError  string    `json:"email_error"`
		} `json:"welcome"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Welcome, "send_welcome=true tem que devolver o bloco welcome")
	require.Equal(t, "sent", resp.Welcome.EmailStatus, "erro: %s", resp.Welcome.EmailError)
	require.Equal(t, 1, mail.sent)
	require.Contains(t, resp.Welcome.Link, "/boasvindas/")

	// O link devolvido resolve de verdade e traz a senha que o admin digitou.
	token := resp.Welcome.Link[strings.LastIndex(resp.Welcome.Link, "/")+1:]
	wh := NewWelcomeHandler(svc)
	rec2 := httptest.NewRecorder()
	wh.Resolve(rec2, reqWithTokenParam(token))

	require.Equal(t, http.StatusOK, rec2.Code)
	require.Contains(t, rec2.Header().Get("Cache-Control"), "no-store",
		"resposta com senha em claro não pode ser cacheada por proxy")

	var page welcome.Resolved
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &page))
	require.Equal(t, "senhaSuperSegura1", page.Password)
	require.Equal(t, "Ana Souza", page.Name)
	require.Equal(t, "ana@sofaecia.com.br", page.Email)
	require.Equal(t, "Sofá & Cia", page.ClientName)
	require.Equal(t, "client", page.Role)
}

func TestUsers_Create_SemSendWelcome_NaoEmiteConvite(t *testing.T) {
	_, pool := newUsersTestPool(t)
	mail := &noopMailer{}
	h := NewUsersHandler(users.NewRepo(pool), newWelcomeSvc(t, pool, mail))

	body := `{"role":"admin","name":"Dereck","email":"d@hub.com","password":"senhaSuperSegura1"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithClaims(req.Context(), newAdminCaller()))
	rec := httptest.NewRecorder()
	h.Create(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Zero(t, mail.sent, "sem o checkbox, nenhum email sai")
	require.NotContains(t, rec.Body.String(), `"welcome"`)
}

func TestUsers_Create_WelcomeIndisponivel_NaoQuebraACriacao(t *testing.T) {
	_, pool := newUsersTestPool(t)
	// Sem WELCOME_ENC_KEY o serviço sobe desabilitado.
	svc := welcome.New(welcome.Config{
		Repo: welcome.NewRepo(pool), Cipher: nil, Log: zap.NewNop(),
	})
	h := NewUsersHandler(users.NewRepo(pool), svc)

	body := `{"role":"admin","name":"Dereck","email":"d@hub.com",
	          "password":"senhaSuperSegura1","send_welcome":true}`
	req := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithClaims(req.Context(), newAdminCaller()))
	rec := httptest.NewRecorder()
	h.Create(rec, req)

	// O usuário TEM que ser criado: perder a conta porque o email não pôde
	// sair seria muito pior que criar sem boas-vindas.
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"email_status":"unavailable"`)
}

// ── Endpoint público ─────────────────────────────────────────────────────

func TestWelcome_Resolve_TokenInvalido404(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewWelcomeHandler(newWelcomeSvc(t, pool, &noopMailer{}))

	for _, token := range []string{"nao-existe", "", strings.Repeat("a", 43)} {
		rec := httptest.NewRecorder()
		h.Resolve(rec, reqWithTokenParam(token))
		// 404 e NUNCA 401: um 401 faria o interceptor do axios limpar a sessão
		// e redirecionar pro /login — numa página visitada justamente sem sessão.
		require.Equal(t, http.StatusNotFound, rec.Code, "token %q", token)
	}
}

func TestWelcome_Revoke(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	svc := newWelcomeSvc(t, pool, &noopMailer{})
	h := NewWelcomeHandler(svc)

	clients := catalog.NewClients(pool)
	c, err := clients.Create(ctx, catalog.CreateClientInput{Name: "Acme"})
	require.NoError(t, err)
	u, err := users.NewRepo(pool).Create(ctx, users.CreateInput{
		Email: "ana@acme.com", PasswordHash: "h", Role: "viewer", ClientID: &c.ID, Name: "Ana",
	})
	require.NoError(t, err)

	res, err := svc.Issue(ctx, welcome.SendInput{
		UserID: u.ID, Name: "Ana", Email: "ana@acme.com",
		Password: "senha123456789", Role: "viewer",
	})
	require.NoError(t, err)
	token := res.Link[strings.LastIndex(res.Link, "/")+1:]

	req := reqWithIDParam(http.MethodPost, "/admin/welcome-invites/x/revoke", "", res.InviteID.String())
	req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: u.ID, Role: "admin"}))
	rec := httptest.NewRecorder()
	h.Revoke(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec2 := httptest.NewRecorder()
	h.Resolve(rec2, reqWithTokenParam(token))
	require.Equal(t, http.StatusNotFound, rec2.Code, "revogado some pra sempre")
}

func TestWelcome_Revoke_IDInvalido(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewWelcomeHandler(newWelcomeSvc(t, pool, &noopMailer{}))

	req := reqWithIDParam(http.MethodPost, "/admin/welcome-invites/x/revoke", "", "nao-e-uuid")
	rec := httptest.NewRecorder()
	h.Revoke(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	req2 := reqWithIDParam(http.MethodPost, "/admin/welcome-invites/x/revoke", "", uuid.NewString())
	rec2 := httptest.NewRecorder()
	h.Revoke(rec2, req2)
	require.Equal(t, http.StatusNotFound, rec2.Code)
}
