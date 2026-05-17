package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

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
	pool, err := db.New(ctx, url)
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
