package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/users"
)

// ── /admin/users ─────────────────────────────────────────────────────────

func postUser(h *UsersHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	return w
}

func TestUsers_Create_WithUsername_Normalized(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool), nil)

	w := postUser(h, `{"email":"u@x.test","username":"  Joao.Silva ","password":"super-secret-pw-12345","name":"U","role":"admin"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var u users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &u))
	require.NotNil(t, u.Username)
	require.Equal(t, "joao.silva", *u.Username)
}

func TestUsers_Create_WithoutUsername_StaysNull(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool), nil)

	// Vazio na criação = "não quero username", não erro.
	w := postUser(h, `{"email":"u@x.test","username":"","password":"super-secret-pw-12345","name":"U","role":"admin"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var u users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &u))
	require.Nil(t, u.Username)
}

func TestUsers_Create_InvalidUsername_400(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool), nil)

	w := postUser(h, `{"email":"u@x.test","username":"tem@arroba","password":"super-secret-pw-12345","name":"U","role":"admin"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "invalid_username")
}

func TestUsers_Create_DuplicateUsername_409(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool), nil)

	w := postUser(h, `{"email":"a@x.test","username":"mesmo","password":"super-secret-pw-12345","name":"A","role":"admin"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	// E-mail diferente, username igual (com caixa diferente): tem que dizer
	// QUAL campo colidiu, senão o admin procura o problema no e-mail.
	w = postUser(h, `{"email":"b@x.test","username":"MESMO","password":"super-secret-pw-12345","name":"B","role":"admin"}`)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), "username_taken")
}

func TestUsers_Patch_SetAndClearUsername(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	h := NewUsersHandler(repo, nil)
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "p@x.test", PasswordHash: "x", Role: "admin", Name: "P",
	})
	require.NoError(t, err)

	req := reqWithIDParam("PATCH", "/admin/users/"+u.ID.String(), `{"username":"Novo.Login"}`, u.ID.String())
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var got users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, "novo.login", *got.Username)

	// Patch sem o campo não mexe no username.
	req = reqWithIDParam("PATCH", "/admin/users/"+u.ID.String(), `{"name":"P2"}`, u.ID.String())
	w = httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, "novo.login", *got.Username)

	// "" remove.
	req = reqWithIDParam("PATCH", "/admin/users/"+u.ID.String(), `{"username":""}`, u.ID.String())
	w = httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	got = users.User{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Nil(t, got.Username)

	// Inválido não grava nada.
	req = reqWithIDParam("PATCH", "/admin/users/"+u.ID.String(), `{"username":"x"}`, u.ID.String())
	w = httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "invalid_username")
}

// ── /auth/login ──────────────────────────────────────────────────────────

func TestAuth_Login_ByUsernameOrEmail(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	repo := users.NewRepo(pool)

	hash, err := bcrypt.GenerateFromPassword([]byte("super-secret-pw-12345"), 10)
	require.NoError(t, err)
	un := "operador"
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "op@example.com", Username: &un, PasswordHash: string(hash),
		Role: "admin", Name: "Op",
	})
	require.NoError(t, err)

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := NewAuthHandler(pool, repo)

	login := func(identifier, password string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"email": identifier, "password": password})
		req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(string(b)))
		w := httptest.NewRecorder()
		h.Login(w, req)
		return w
	}

	for _, id := range []string{"operador", " Operador ", "op@example.com", "OP@example.com"} {
		w := login(id, "super-secret-pw-12345")
		require.Equal(t, http.StatusOK, w.Code, "%q: %s", id, w.Body.String())
		var resp loginResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Equal(t, u.ID, resp.User.ID, id)
		require.Equal(t, "operador", *resp.User.Username, id)
	}

	// Senha errada pelo username = mesma resposta da senha errada pelo e-mail.
	w := login("operador", "errada-errada-errada")
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "invalid credentials")

	// Username inexistente ou fora do formato: mesma resposta, sem oráculo.
	for _, id := range []string{"ninguem", "x", "com espaço"} {
		w := login(id, "super-secret-pw-12345")
		require.Equal(t, http.StatusUnauthorized, w.Code, id)
		require.Contains(t, w.Body.String(), "invalid credentials", id)
	}
}
