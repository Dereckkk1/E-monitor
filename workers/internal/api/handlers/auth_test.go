package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/db"
	"radiocheck/internal/dbtest"
	"radiocheck/internal/users"
)

// TestAuth_Login_BadJSON rejects malformed body with 400 before the DB.
func TestAuth_Login_BadJSON(t *testing.T) {
	h := NewAuthHandler(nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestNewAuthHandler_Defaults verifies the constructor wires the pool.
func TestNewAuthHandler_Defaults(t *testing.T) {
	if h := NewAuthHandler(nil, nil); h == nil {
		t.Fatal("NewAuthHandler returned nil")
	}
}

// TestNewAPIKeysHandler_Defaults verifies the constructor wires the pool.
func TestNewAPIKeysHandler_Defaults(t *testing.T) {
	if h := NewAPIKeysHandler(nil); h == nil {
		t.Fatal("NewAPIKeysHandler returned nil")
	}
}

// TestNewWebhooksHandler_Defaults verifies the constructor wires the pool /
// dependencies even when outbox is nil.
func TestNewWebhooksHandler_Defaults(t *testing.T) {
	if h := NewWebhooksHandler(nil, nil, nil); h == nil {
		t.Fatal("NewWebhooksHandler returned nil")
	}
}

// ── DB-backed integration tests ──────────────────────────────────────────

func newAuthTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	// Guard: recusa TRUNCATE se DB tem dado real (ver dbtest/guard.go).
	dbtest.GuardOrSkip(t, ctx, pool)
	_, err = pool.Exec(ctx, `TRUNCATE users, clients RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	return ctx, pool
}

func TestAuth_Login_DisabledAccount(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	repo := users.NewRepo(pool)

	hash, err := bcrypt.GenerateFromPassword([]byte("super-secret-pw-12345"), 10)
	require.NoError(t, err)
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "off@example.com", PasswordHash: string(hash),
		Role: "admin", Name: "Off",
	})
	require.NoError(t, err)
	disabled := false
	_, err = repo.Update(ctx, u.ID, users.UpdateInput{IsActive: &disabled})
	require.NoError(t, err)

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := NewAuthHandler(pool, repo)
	body := strings.NewReader(`{"email":"off@example.com","password":"super-secret-pw-12345"}`)
	req := httptest.NewRequest("POST", "/login", body)
	w := httptest.NewRecorder()
	h.Login(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "account_disabled")
}

func TestAuth_Login_DeletedAccountTreatedAsInvalid(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	repo := users.NewRepo(pool)

	hash, _ := bcrypt.GenerateFromPassword([]byte("super-secret-pw-12345"), 10)
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "gone@example.com", PasswordHash: string(hash),
		Role: "admin", Name: "Gone",
	})
	require.NoError(t, err)
	require.NoError(t, repo.SoftDelete(ctx, u.ID))

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := NewAuthHandler(pool, repo)
	body := strings.NewReader(`{"email":"gone@example.com","password":"super-secret-pw-12345"}`)
	req := httptest.NewRequest("POST", "/login", body)
	w := httptest.NewRecorder()
	h.Login(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "invalid credentials")
}

func TestAuth_Login_Viewer_TouchesLastLoginAndReturnsClientID(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)

	c, err := clients.Create(ctx, catalog.CreateClientInput{Name: "Acme"})
	require.NoError(t, err)

	hash, _ := bcrypt.GenerateFromPassword([]byte("super-secret-pw-12345"), 10)
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "ll@acme.com", PasswordHash: string(hash),
		Role: "viewer", ClientID: &c.ID, Name: "Last Login",
	})
	require.NoError(t, err)
	require.Nil(t, u.LastLoginAt)

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := NewAuthHandler(pool, repo)
	body := strings.NewReader(`{"email":"ll@acme.com","password":"super-secret-pw-12345"}`)
	req := httptest.NewRequest("POST", "/login", body)
	w := httptest.NewRecorder()
	h.Login(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// Resposta inclui client_id e name.
	var resp struct {
		Token string `json:"token"`
		User  struct {
			ID       string `json:"id"`
			Email    string `json:"email"`
			Role     string `json:"role"`
			Name     string `json:"name"`
			ClientID string `json:"client_id"`
		} `json:"user"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Token)
	require.Equal(t, "viewer", resp.User.Role)
	require.Equal(t, "Last Login", resp.User.Name)
	require.Equal(t, c.ID.String(), resp.User.ClientID)

	// last_login_at agora populado.
	got, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastLoginAt, "last_login_at should be set after login")
}

// ── Login escopado nos clientes ATIVOS da carteira ───────────────────────
//
// Regra: desativar UM cliente da carteira de uma agência não derruba a conta
// inteira — só some com aquele cliente da visão da próxima sessão. Só quando
// NENHUM cliente da carteira está ativo é que o login vira client_disabled.
// Com carteira de 1 (a base instalada inteira hoje) o comportamento é
// idêntico ao anterior.

// seedAgencyUser cria dois clientes (com o is_active informado em cada) e um
// usuário Cliente vinculado aos dois. Devolve os ids na ordem A, B.
func seedAgencyUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	email string, aActive, bActive bool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)

	a, err := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente A"})
	require.NoError(t, err)
	b, err := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente B"})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE clients SET is_active = $2 WHERE id = $1`, a.ID, aActive)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE clients SET is_active = $2 WHERE id = $1`, b.ID, bActive)
	require.NoError(t, err)

	hash, err := bcrypt.GenerateFromPassword([]byte("super-secret-pw-12345"), 10)
	require.NoError(t, err)
	u, err := repo.Create(ctx, users.CreateInput{
		Email: email, PasswordHash: string(hash),
		Role: "viewer", ClientID: &a.ID, Name: "Agência",
	})
	require.NoError(t, err)
	require.NoError(t, repo.SetClients(ctx, u.ID, []uuid.UUID{a.ID, b.ID}))
	return a.ID, b.ID
}

// loginAgency faz o POST /login e devolve o recorder + a resposta decodificada.
func loginAgency(t *testing.T, pool *pgxpool.Pool, email string) (*httptest.ResponseRecorder, agencyLoginResp) {
	t.Helper()
	h := NewAuthHandler(pool, users.NewRepo(pool))
	body := strings.NewReader(`{"email":"` + email + `","password":"super-secret-pw-12345"}`)
	req := httptest.NewRequest("POST", "/login", body)
	w := httptest.NewRecorder()
	h.Login(w, req)
	var resp agencyLoginResp
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	}
	return w, resp
}

type agencyLoginResp struct {
	Token string `json:"token"`
	User  struct {
		ClientID  *uuid.UUID  `json:"client_id"`
		ClientIDs []uuid.UUID `json:"client_ids"`
	} `json:"user"`
}

// seedSingleClientUser cria UM cliente com o is_active informado e um usuário
// Cliente vinculado só a ele — a forma da base instalada inteira hoje.
func seedSingleClientUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	email string, active bool) uuid.UUID {
	t.Helper()
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)

	c, err := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente Único"})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE clients SET is_active = $2 WHERE id = $1`, c.ID, active)
	require.NoError(t, err)

	hash, err := bcrypt.GenerateFromPassword([]byte("super-secret-pw-12345"), 10)
	require.NoError(t, err)
	_, err = repo.Create(ctx, users.CreateInput{
		Email: email, PasswordHash: string(hash),
		Role: "viewer", ClientID: &c.ID, Name: "Cliente",
	})
	require.NoError(t, err)
	return c.ID
}

// REGRESSÃO da base instalada: com UM cliente desativado o login continua
// 403 client_disabled, exatamente como antes da carteira multi-cliente.
// Este caminho não tinha teste; a regra "pelo menos um ativo" degenera nele
// quando a carteira tem tamanho 1.
func TestAuth_Login_SingleClient_InactiveClient_Blocked(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	seedSingleClientUser(t, ctx, pool, "solo-off@cliente.test", false)

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	w, _ := loginAgency(t, pool, "solo-off@cliente.test")
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "client_disabled")
}

// REGRESSÃO da base instalada: com UM cliente ativo o escopo do token é
// exatamente [C] e o principal é C — nada é reposicionado nem acrescentado.
func TestAuth_Login_SingleClient_ActiveClient_ScopeUnchanged(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	c := seedSingleClientUser(t, ctx, pool, "solo-on@cliente.test", true)

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	w, resp := loginAgency(t, pool, "solo-on@cliente.test")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	require.NotNil(t, resp.User.ClientID)
	require.Equal(t, c, *resp.User.ClientID)
	require.Equal(t, []uuid.UUID{c}, resp.User.ClientIDs)

	claims, err := auth.ParseToken(resp.Token)
	require.NoError(t, err)
	require.NotNil(t, claims.ClientID)
	require.Equal(t, c, *claims.ClientID)
	require.Equal(t, []uuid.UUID{c}, claims.ClientIDs)
}

func TestAuth_Login_MultiClient_SkipsInactiveClient(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	a, b := seedAgencyUser(t, ctx, pool, "ag1@agencia.test", true, false)

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	w, resp := loginAgency(t, pool, "ag1@agencia.test")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	require.Equal(t, []uuid.UUID{a}, resp.User.ClientIDs, "B está desativado, não pode aparecer")
	require.NotContains(t, resp.User.ClientIDs, b)

	claims, err := auth.ParseToken(resp.Token)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{a}, claims.ClientIDs, "o token é o que manda no escopo")
	require.NotNil(t, claims.ClientID)
	require.Equal(t, a, *claims.ClientID)
}

func TestAuth_Login_AllClientsInactive_Blocked(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	seedAgencyUser(t, ctx, pool, "ag2@agencia.test", false, false)

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	w, _ := loginAgency(t, pool, "ag2@agencia.test")
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "client_disabled")
}

func TestAuth_Login_MultiClient_BothActive_KeepsBoth(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	a, b := seedAgencyUser(t, ctx, pool, "ag3@agencia.test", true, true)

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	w, resp := loginAgency(t, pool, "ag3@agencia.test")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	require.ElementsMatch(t, []uuid.UUID{a, b}, resp.User.ClientIDs)
	require.NotNil(t, resp.User.ClientID)
	require.Equal(t, a, *resp.User.ClientID, "principal segue sendo A")

	claims, err := auth.ParseToken(resp.Token)
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{a, b}, claims.ClientIDs)
	require.NotNil(t, claims.ClientID)
	require.Equal(t, a, *claims.ClientID)
}

func TestAuth_Login_PrincipalInactive_RepositionsPrincipal(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	_, b := seedAgencyUser(t, ctx, pool, "ag4@agencia.test", false, true)

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	w, resp := loginAgency(t, pool, "ag4@agencia.test")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	require.Equal(t, []uuid.UUID{b}, resp.User.ClientIDs)
	require.NotNil(t, resp.User.ClientID)
	require.Equal(t, b, *resp.User.ClientID, "principal desativado é reposicionado pro ativo")

	claims, err := auth.ParseToken(resp.Token)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{b}, claims.ClientIDs)
	require.NotNil(t, claims.ClientID)
	require.Equal(t, b, *claims.ClientID)
}

// Com DOIS candidatos ativos, "qual vira o principal" deixa de ser vácuo: o
// escolhido é o primeiro vínculo ainda ativo na ORDEM CANÔNICA da carteira (a
// mesma que users.Repo devolve — user_clients.created_at, client_id), não a
// ordem em que o Postgres devolveu as linhas do SELECT, que sem ORDER BY não é
// garantida. Sem isso o rótulo da sessão poderia alternar entre logins.
func TestAuth_Login_PrincipalReposition_FollowsWalletOrder(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)

	var ids []uuid.UUID
	for _, n := range []string{"C1", "C2", "C3"} {
		c, err := clients.Create(ctx, catalog.CreateClientInput{Name: n})
		require.NoError(t, err)
		ids = append(ids, c.ID)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("super-secret-pw-12345"), 10)
	require.NoError(t, err)
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "ag5@agencia.test", PasswordHash: string(hash),
		Role: "viewer", ClientID: &ids[0], Name: "Agência",
	})
	require.NoError(t, err)
	require.NoError(t, repo.SetClients(ctx, u.ID, ids))
	// Desativa só o principal: sobram DOIS candidatos ativos pra assumir.
	_, err = pool.Exec(ctx, `UPDATE clients SET is_active = FALSE WHERE id = $1`, ids[0])
	require.NoError(t, err)

	// Expectativa derivada da carteira como o repo a ordena, não hard-coded.
	fresh, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	var wantWallet []uuid.UUID
	for _, id := range fresh.ClientIDs {
		if id != ids[0] {
			wantWallet = append(wantWallet, id)
		}
	}
	require.Len(t, wantWallet, 2)

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	w, resp := loginAgency(t, pool, "ag5@agencia.test")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	claims, err := auth.ParseToken(resp.Token)
	require.NoError(t, err)
	require.Equal(t, wantWallet, claims.ClientIDs, "carteira ativa mantém a ordem canônica")
	require.NotNil(t, claims.ClientID)
	require.Equal(t, wantWallet[0], *claims.ClientID,
		"principal = primeiro vínculo AINDA ATIVO na ordem canônica da carteira")
	require.Equal(t, wantWallet, resp.User.ClientIDs)
	require.Equal(t, wantWallet[0], *resp.User.ClientID)
}

func TestAuth_Login_AdminResponse_OmitsClientID(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	repo := users.NewRepo(pool)

	hash, _ := bcrypt.GenerateFromPassword([]byte("super-secret-pw-12345"), 10)
	_, err := repo.Create(ctx, users.CreateInput{
		Email: "ad@x.test", PasswordHash: string(hash),
		Role: "admin", Name: "AD",
	})
	require.NoError(t, err)

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := NewAuthHandler(pool, repo)
	body := strings.NewReader(`{"email":"ad@x.test","password":"super-secret-pw-12345"}`)
	req := httptest.NewRequest("POST", "/login", body)
	w := httptest.NewRecorder()
	h.Login(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// JSON não deve conter "client_id" (omitempty + nil pointer).
	require.NotContains(t, w.Body.String(), `"client_id"`)
}
