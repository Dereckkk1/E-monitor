package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/db"
	"radiocheck/internal/users"
)

func newUsersTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
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

// reqWithIDParam returns an http.Request whose chi.URLParam("id") == id.
func reqWithIDParam(method, path, body, id string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return req
}

func newAdminCaller() *auth.Claims {
	return &auth.Claims{UserID: uuid.New(), Role: "admin"}
}

// ── List ────────────────────────────────────────────────────────────────

func TestUsers_List_FiltersAndPaginate(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)
	c, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Acme"})

	for i := 0; i < 3; i++ {
		_, err := repo.Create(ctx, users.CreateInput{
			Email: "a" + strconvI(i) + "@x", PasswordHash: "h", Role: "admin", Name: "A",
		})
		require.NoError(t, err)
	}
	_, err := repo.Create(ctx, users.CreateInput{
		Email: "v@x", PasswordHash: "h", Role: "viewer", ClientID: &c.ID, Name: "V",
	})
	require.NoError(t, err)

	h := NewUsersHandler(repo)

	// status=active sem filtro
	req := httptest.NewRequest("GET", "/admin/users", nil)
	req = req.WithContext(auth.ContextWithClaims(req.Context(), newAdminCaller()))
	w := httptest.NewRecorder()
	h.List(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data       []users.User `json:"data"`
		Total      int          `json:"total"`
		TotalPages int          `json:"total_pages"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 4, resp.Total)

	// role=client → só viewer
	req = httptest.NewRequest("GET", "/admin/users?role=client", nil)
	w = httptest.NewRecorder()
	h.List(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 1, resp.Total)
	require.Equal(t, "viewer", resp.Data[0].Role)

	// page_size=2 → 2 páginas pra 4 admins+1 viewer = 5? não, role filter zerou.
	// melhor: sem filtro, page_size=2
	req = httptest.NewRequest("GET", "/admin/users?page_size=2", nil)
	w = httptest.NewRecorder()
	h.List(w, req)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 4, resp.Total)
	require.Equal(t, 2, resp.TotalPages)
	require.Len(t, resp.Data, 2)
}

func TestUsers_List_InvalidRole(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool))
	req := httptest.NewRequest("GET", "/admin/users?role=hacker", nil)
	w := httptest.NewRecorder()
	h.List(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "invalid_role_filter")
}

// ── Get ────────────────────────────────────────────────────────────────

func TestUsers_Get_NotFound(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool))
	req := reqWithIDParam("GET", "/admin/users/x", "", uuid.NewString())
	w := httptest.NewRecorder()
	h.Get(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

// ── Create ─────────────────────────────────────────────────────────────

func TestUsers_Create_Admin_NoClientID_OK(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool))
	body := `{"email":"new@x.test","password":"super-secret-pw-12345","name":"N","role":"admin"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var u users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &u))
	require.Equal(t, "admin", u.Role)
	require.Nil(t, u.ClientID)
}

func TestUsers_Create_Client_RoleConvertsToViewer(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	clients := catalog.NewClients(pool)
	c, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Acme"})
	h := NewUsersHandler(users.NewRepo(pool))

	body := `{"email":"c@acme.com","password":"super-secret-pw-12345","name":"C","role":"client","client_id":"` + c.ID.String() + `"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var u users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &u))
	require.Equal(t, "viewer", u.Role, "API 'client' must persist as DB 'viewer'")
	require.Equal(t, c.ID, *u.ClientID)
}

func TestUsers_Create_Client_RequiresClientID(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool))
	body := `{"email":"c@acme.com","password":"super-secret-pw-12345","name":"C","role":"client"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "client_id_required")
}

func TestUsers_Create_Admin_RejectsClientID(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	clients := catalog.NewClients(pool)
	c, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Acme"})
	h := NewUsersHandler(users.NewRepo(pool))
	body := `{"email":"a@x.test","password":"super-secret-pw-12345","name":"A","role":"admin","client_id":"` + c.ID.String() + `"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "client_id_not_allowed_for_admin")
}

func TestUsers_Create_DuplicateEmail_409(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool))
	body := `{"email":"dup@x.test","password":"super-secret-pw-12345","name":"D","role":"admin"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	// segundo POST com mesmo email
	req = httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w = httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), "email_taken")
}

func TestUsers_Create_PasswordTooShort_400(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool))
	body := `{"email":"a@x.test","password":"shorty","name":"A","role":"admin"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "password_too_short")
}

func TestUsers_Create_InvalidRole_400(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool))
	body := `{"email":"a@x.test","password":"super-secret-pw-12345","name":"A","role":"operator"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "invalid_role")
}

// ── Patch ───────────────────────────────────────────────────────────────

func TestUsers_Patch_RejectsEmail(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	u, _ := repo.Create(ctx, users.CreateInput{Email: "a@x.test", PasswordHash: "h", Role: "admin", Name: "A"})
	h := NewUsersHandler(repo)
	req := reqWithIDParam("PATCH", "/admin/users/x", `{"email":"new@x.test"}`, u.ID.String())
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "email_immutable")
}

func TestUsers_Patch_CannotReactivateDeleted(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	u, _ := repo.Create(ctx, users.CreateInput{Email: "a@x.test", PasswordHash: "h", Role: "admin", Name: "A"})
	require.NoError(t, repo.SoftDelete(ctx, u.ID))
	h := NewUsersHandler(repo)
	req := reqWithIDParam("PATCH", "/admin/users/x", `{"is_active":true}`, u.ID.String())
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "cannot_reactivate_deleted")
}

func TestUsers_Patch_PromoteViewerToAdmin_ClearsClient(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)
	c, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Acme"})
	u, _ := repo.Create(ctx, users.CreateInput{Email: "p@x.test", PasswordHash: "h", Role: "viewer", ClientID: &c.ID, Name: "P"})

	h := NewUsersHandler(repo)
	req := reqWithIDParam("PATCH", "/admin/users/x", `{"role":"admin"}`, u.ID.String())
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	got, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, "admin", got.Role)
	require.Nil(t, got.ClientID)
}

// ── ResetPassword ───────────────────────────────────────────────────────

func TestUsers_ResetPassword_OK(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	hash, _ := bcrypt.GenerateFromPassword([]byte("super-secret-pw-12345"), 10)
	u, _ := repo.Create(ctx, users.CreateInput{Email: "a@x.test", PasswordHash: string(hash), Role: "admin", Name: "A"})

	h := NewUsersHandler(repo)
	req := reqWithIDParam("POST", "/admin/users/x/password",
		`{"password":"another-super-strong-pw"}`, u.ID.String())
	w := httptest.NewRecorder()
	h.ResetPassword(w, req)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	got, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(got.PasswordHash), []byte("another-super-strong-pw")))
}

func TestUsers_ResetPassword_TooShort(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool))
	req := reqWithIDParam("POST", "/admin/users/x/password",
		`{"password":"short"}`, uuid.NewString())
	w := httptest.NewRecorder()
	h.ResetPassword(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "password_too_short")
}

// ── Delete ──────────────────────────────────────────────────────────────

func TestUsers_Delete_BlocksSelf(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	u, _ := repo.Create(ctx, users.CreateInput{Email: "a@x.test", PasswordHash: "h", Role: "admin", Name: "A"})
	h := NewUsersHandler(repo)

	req := reqWithIDParam("DELETE", "/admin/users/x", "", u.ID.String())
	req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: u.ID, Role: "admin"}))
	w := httptest.NewRecorder()
	h.Delete(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "cannot_delete_self")

	// Confere: NÃO foi soft-deleted
	got, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.Nil(t, got.DeletedAt)
}

func TestUsers_Delete_OK_AndIdempotent(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	u, _ := repo.Create(ctx, users.CreateInput{Email: "a@x.test", PasswordHash: "h", Role: "admin", Name: "A"})
	h := NewUsersHandler(repo)

	other := &auth.Claims{UserID: uuid.New(), Role: "admin"}

	// 1ª: soft delete
	req := reqWithIDParam("DELETE", "/admin/users/x", "", u.ID.String())
	req = req.WithContext(auth.ContextWithClaims(req.Context(), other))
	w := httptest.NewRecorder()
	h.Delete(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)

	// 2ª: idempotente
	req = reqWithIDParam("DELETE", "/admin/users/x", "", u.ID.String())
	req = req.WithContext(auth.ContextWithClaims(req.Context(), other))
	w = httptest.NewRecorder()
	h.Delete(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
}

// strconvI evita importar strconv só pra labels de email
func strconvI(i int) string {
	return string(rune('0' + i))
}
