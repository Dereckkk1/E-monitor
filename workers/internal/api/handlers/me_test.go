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

	"radiocheck/internal/auth"
	"radiocheck/internal/db"
	"radiocheck/internal/users"
)

func newMeTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	_, err = pool.Exec(ctx, `TRUNCATE users, clients RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	return ctx, pool
}

func makeMeUser(t *testing.T, ctx context.Context, repo *users.Repo, password string) *users.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	require.NoError(t, err)
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "me@example.com", PasswordHash: string(hash),
		Role: "admin", Name: "Original",
	})
	require.NoError(t, err)
	return u
}

func reqWithClaims(method, path string, body string, claims *auth.Claims) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(auth.ContextWithClaims(req.Context(), claims))
	return req
}

func TestMe_Get(t *testing.T) {
	ctx, pool := newMeTestPool(t)
	repo := users.NewRepo(pool)
	u := makeMeUser(t, ctx, repo, "super-secret-pw-12345")

	h := NewMeHandler(repo)
	req := reqWithClaims("GET", "/me", "", &auth.Claims{UserID: u.ID, Role: "admin"})
	w := httptest.NewRecorder()
	h.Get(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, u.ID, got.ID)
	require.Equal(t, "me@example.com", got.Email)
	require.Equal(t, "Original", got.Name)
}

func TestMe_Get_Unauthorized(t *testing.T) {
	ctx, pool := newMeTestPool(t)
	_ = ctx
	h := NewMeHandler(users.NewRepo(pool))
	req := httptest.NewRequest("GET", "/me", nil) // sem claims
	w := httptest.NewRecorder()
	h.Get(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestMe_Patch_NameAndPhone(t *testing.T) {
	ctx, pool := newMeTestPool(t)
	repo := users.NewRepo(pool)
	u := makeMeUser(t, ctx, repo, "super-secret-pw-12345")

	h := NewMeHandler(repo)
	req := reqWithClaims("PATCH", "/me",
		`{"name":"New Name","phone":"+5511999999999"}`,
		&auth.Claims{UserID: u.ID, Role: "admin"})
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	got, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, "New Name", got.Name)
	require.NotNil(t, got.Phone)
	require.Equal(t, "+5511999999999", *got.Phone)
}

func TestMe_Patch_IgnoresRoleAndClientID(t *testing.T) {
	// Self-service não deve permitir promover-se a admin nem trocar de cliente.
	// O Patch handler aceita só name/phone — outros campos são silenciosamente
	// ignorados (não erra, só ignora).
	ctx, pool := newMeTestPool(t)
	repo := users.NewRepo(pool)
	u := makeMeUser(t, ctx, repo, "super-secret-pw-12345")

	h := NewMeHandler(repo)
	req := reqWithClaims("PATCH", "/me",
		`{"name":"X","role":"viewer","client_id":"00000000-0000-0000-0000-000000000001"}`,
		&auth.Claims{UserID: u.ID, Role: "admin"})
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	got, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, "admin", got.Role, "role should NOT change via /me")
	require.Nil(t, got.ClientID, "client_id should NOT change via /me")
	require.Equal(t, "X", got.Name)
}

func TestMe_ChangePassword_RequiresCurrent(t *testing.T) {
	ctx, pool := newMeTestPool(t)
	repo := users.NewRepo(pool)
	u := makeMeUser(t, ctx, repo, "super-secret-pw-12345")

	h := NewMeHandler(repo)

	// Sem current_password → 400 missing_fields
	w := httptest.NewRecorder()
	h.ChangePassword(w,
		reqWithClaims("POST", "/me/password",
			`{"new_password":"another-strong-pw"}`,
			&auth.Claims{UserID: u.ID, Role: "admin"}))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "missing_fields")

	// Current errado → 401 invalid_current_password
	w = httptest.NewRecorder()
	h.ChangePassword(w,
		reqWithClaims("POST", "/me/password",
			`{"current_password":"WRONG","new_password":"another-strong-pw"}`,
			&auth.Claims{UserID: u.ID, Role: "admin"}))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "invalid_current_password")

	// Nova senha < 12 chars → 400 password_too_short
	w = httptest.NewRecorder()
	h.ChangePassword(w,
		reqWithClaims("POST", "/me/password",
			`{"current_password":"super-secret-pw-12345","new_password":"shorty"}`,
			&auth.Claims{UserID: u.ID, Role: "admin"}))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "password_too_short")

	// Happy path → 204
	w = httptest.NewRecorder()
	h.ChangePassword(w,
		reqWithClaims("POST", "/me/password",
			`{"current_password":"super-secret-pw-12345","new_password":"another-strong-pw-12345"}`,
			&auth.Claims{UserID: u.ID, Role: "admin"}))
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	// Validar: a nova senha realmente passa no bcrypt
	got, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(got.PasswordHash), []byte("another-strong-pw-12345")))
}

func TestMe_ChangePassword_Unauthorized(t *testing.T) {
	_, pool := newMeTestPool(t)
	h := NewMeHandler(users.NewRepo(pool))
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/me/password",
		strings.NewReader(`{"current_password":"x","new_password":"another-strong-pw"}`))
	h.ChangePassword(w, req) // sem claims
	require.Equal(t, http.StatusUnauthorized, w.Code)
}
