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
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/db"
	"radiocheck/internal/dbtest"
	"radiocheck/internal/users"
)

func newUsersTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
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

	h := NewUsersHandler(repo, nil)

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
	h := NewUsersHandler(users.NewRepo(pool), nil)
	req := httptest.NewRequest("GET", "/admin/users?role=hacker", nil)
	w := httptest.NewRecorder()
	h.List(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "invalid_role_filter")
}

// ── Get ────────────────────────────────────────────────────────────────

func TestUsers_Get_NotFound(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool), nil)
	req := reqWithIDParam("GET", "/admin/users/x", "", uuid.NewString())
	w := httptest.NewRecorder()
	h.Get(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

// ── Create ─────────────────────────────────────────────────────────────

func TestUsers_Create_Admin_NoClientID_OK(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool), nil)
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
	h := NewUsersHandler(users.NewRepo(pool), nil)

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
	h := NewUsersHandler(users.NewRepo(pool), nil)
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
	h := NewUsersHandler(users.NewRepo(pool), nil)
	body := `{"email":"a@x.test","password":"super-secret-pw-12345","name":"A","role":"admin","client_id":"` + c.ID.String() + `"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "client_id_not_allowed_for_admin")
}

func TestUsers_Create_DuplicateEmail_409(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool), nil)
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
	h := NewUsersHandler(users.NewRepo(pool), nil)
	body := `{"email":"a@x.test","password":"shorty","name":"A","role":"admin"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "password_too_short")
}

func TestUsers_Create_InvalidRole_400(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool), nil)
	body := `{"email":"a@x.test","password":"super-secret-pw-12345","name":"A","role":"operator"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "invalid_role")
}

// Criar usuário de agência com 2 clientes: o primeiro da lista vira o principal.
func TestUsers_Create_Client_WithClientIDs(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	clients := catalog.NewClients(pool)
	a, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente A"})
	b, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente B"})
	h := NewUsersHandler(users.NewRepo(pool), nil)

	body := `{"email":"ag@acme.com","password":"super-secret-pw-12345","name":"Agência",
	          "role":"client","client_ids":["` + a.ID.String() + `","` + b.ID.String() + `"]}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var u users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &u))
	require.ElementsMatch(t, []uuid.UUID{a.ID, b.ID}, u.ClientIDs)
	require.Equal(t, a.ID, *u.ClientID)
}

// O frontend antigo (janela entre o deploy do backend e o do frontend) manda
// só client_id: tem que continuar criando um usuário com carteira de um.
func TestUsers_Create_Client_LegacyClientIDStillWorks(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	clients := catalog.NewClients(pool)
	c, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente Legado"})
	h := NewUsersHandler(users.NewRepo(pool), nil)

	body := `{"email":"legacy@acme.com","password":"super-secret-pw-12345","name":"L",
	          "role":"client","client_id":"` + c.ID.String() + `"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var u users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &u))
	require.Equal(t, []uuid.UUID{c.ID}, u.ClientIDs)
	require.Equal(t, c.ID, *u.ClientID)
}

// Carteira vazia com role client é o mesmo buraco do client_id ausente: sem
// nenhum cliente, o escopo do JWT não resolve nada. 400, não 500.
func TestUsers_Create_Client_EmptyWalletRejected(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool), nil)

	body := `{"email":"vazio@acme.com","password":"super-secret-pw-12345","name":"V",
	          "role":"client","client_ids":[]}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "client_id_required")
}

// Id inexistente na carteira tem que virar client_not_found 400 — inclusive
// quando ele está numa posição SECUNDÁRIA, onde quem estoura o 23503 é o
// SetClients e não o INSERT do usuário.
func TestUsers_Create_Client_UnknownClientIs400(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	clients := catalog.NewClients(pool)
	real, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente Real"})
	h := NewUsersHandler(users.NewRepo(pool), nil)
	bogus := uuid.New()

	// (a) único elemento da carteira é inválido → falha já no INSERT.
	body := `{"email":"ghost1@acme.com","password":"super-secret-pw-12345","name":"G",
	          "role":"client","client_ids":["` + bogus.String() + `"]}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "client_not_found")

	// (b) inválido em posição secundária → falha no SetClients. Sem tradução
	// do 23503 isso viraria 500.
	body = `{"email":"ghost2@acme.com","password":"super-secret-pw-12345","name":"G",
	         "role":"client","client_ids":["` + real.ID.String() + `","` + bogus.String() + `"]}`
	req = httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w = httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "client_not_found")

	// Trade-off documentado do caso (b): o INSERT do usuário já commitou antes
	// do SetContext falhar, então ghost2 EXISTE com a carteira de um elemento.
	// Não é vazamento (o vínculo inválido nunca entrou — SetClients é
	// transacional), mas o admin que corrigir o id e reenviar toma
	// email_taken 409 e precisa editar o usuário em vez de recriar.
	orphan, err := users.NewRepo(pool).GetByEmail(ctx, "ghost2@acme.com")
	require.NoError(t, err, "usuário parcial fica gravado — se isso mudar, atualize o comentário")
	require.Equal(t, []uuid.UUID{real.ID}, orphan.ClientIDs,
		"carteira parcial tem só o principal: SetClients falhou inteiro")
}

// ── Patch ───────────────────────────────────────────────────────────────

// PATCH client_ids SUBSTITUI a carteira inteira (não é append): quem sai da
// lista perde o acesso, que é o ponto — a carteira é o escopo de leitura.
func TestUsers_Patch_ReplacesWallet(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)
	a, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente A"})
	b, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente B"})
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "ag@acme.com", PasswordHash: "h", Role: "viewer", ClientID: &a.ID, Name: "Ag",
	})
	require.NoError(t, err)
	h := NewUsersHandler(repo, nil)

	// [a] → [a, b]
	req := reqWithIDParam("PATCH", "/admin/users/x",
		`{"client_ids":["`+a.ID.String()+`","`+b.ID.String()+`"]}`, u.ID.String())
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.ElementsMatch(t, []uuid.UUID{a.ID, b.ID}, got.ClientIDs)
	require.Equal(t, a.ID, *got.ClientID, "principal atual continua na lista → é mantido")

	// [a, b] → [b]: `a` some da carteira E deixa de ser o principal.
	req = reqWithIDParam("PATCH", "/admin/users/x",
		`{"client_ids":["`+b.ID.String()+`"]}`, u.ID.String())
	w = httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, []uuid.UUID{b.ID}, got.ClientIDs)
	require.Equal(t, b.ID, *got.ClientID)
}

// Virar admin zera a carteira: senão o vínculo antigo sobreviveria e o filtro
// por cliente do /admin/users continuaria achando o usuário.
func TestUsers_Patch_ToAdmin_ClearsWallet(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)
	a, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente A"})
	b, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente B"})
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "ag@acme.com", PasswordHash: "h", Role: "viewer", ClientID: &a.ID, Name: "Ag",
	})
	require.NoError(t, err)
	require.NoError(t, repo.SetClients(ctx, u.ID, []uuid.UUID{a.ID, b.ID}))

	h := NewUsersHandler(repo, nil)
	req := reqWithIDParam("PATCH", "/admin/users/x", `{"role":"admin"}`, u.ID.String())
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, "admin", got.Role)
	require.Nil(t, got.ClientID)
	require.Empty(t, got.ClientIDs)

	persisted, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.Empty(t, persisted.ClientIDs, "carteira tem que ter sido apagada no banco")

	// E não dá pra reencher: admin com carteira é o vazamento que a poda do
	// Update fechou. O CHECK users_client_role_consistency barra o principal,
	// então o PATCH morre em 400 e a carteira continua vazia.
	req = reqWithIDParam("PATCH", "/admin/users/x",
		`{"client_ids":["`+b.ID.String()+`"]}`, u.ID.String())
	w = httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "role_client_inconsistent")

	persisted, err = repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.Empty(t, persisted.ClientIDs, "admin não pode acabar com carteira por caminho nenhum")
	require.Nil(t, persisted.ClientID)
}

// Promover admin → cliente mandando SÓ client_ids (é o que o formulário novo
// manda): o principal sai da lista, e o CHECK users_client_role_consistency
// não pode ser violado no meio do caminho.
func TestUsers_Patch_ToClient_WithClientIDsOnly(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)
	a, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente A"})
	b, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente B"})
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "adm@acme.com", PasswordHash: "h", Role: "admin", Name: "Adm",
	})
	require.NoError(t, err)

	h := NewUsersHandler(repo, nil)
	req := reqWithIDParam("PATCH", "/admin/users/x",
		`{"role":"client","client_ids":["`+a.ID.String()+`","`+b.ID.String()+`"]}`, u.ID.String())
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, "viewer", got.Role)
	require.ElementsMatch(t, []uuid.UUID{a.ID, b.ID}, got.ClientIDs)
	require.Equal(t, a.ID, *got.ClientID)
}

func TestUsers_Patch_RejectsEmail(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	u, _ := repo.Create(ctx, users.CreateInput{Email: "a@x.test", PasswordHash: "h", Role: "admin", Name: "A"})
	h := NewUsersHandler(repo, nil)
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
	h := NewUsersHandler(repo, nil)
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

	h := NewUsersHandler(repo, nil)
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

	h := NewUsersHandler(repo, nil)
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
	h := NewUsersHandler(users.NewRepo(pool), nil)
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
	h := NewUsersHandler(repo, nil)

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
	h := NewUsersHandler(repo, nil)

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

// PATCH com um id inválido na carteira não pode DESTRUIR a carteira atual.
//
// O Update e o SetClients são transações separadas: se o Update gravar o
// principal (podando os vínculos antigos) e o SetClients falhar depois, a
// carteira fica truncada e o admin só vê um erro — parece que nada mudou.
func TestUsers_Patch_UnknownClientInWallet_DoesNotTruncate(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)
	a, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente A"})
	b, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente B"})
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "trunca@acme.com", PasswordHash: "h", Role: "viewer", ClientID: &a.ID, Name: "Trunca",
	})
	require.NoError(t, err)
	require.NoError(t, repo.SetClients(ctx, u.ID, []uuid.UUID{a.ID, b.ID}))
	h := NewUsersHandler(repo, nil)

	req := reqWithIDParam("PATCH", "/admin/users/x",
		`{"client_ids":["`+a.ID.String()+`","`+b.ID.String()+`","`+uuid.NewString()+`"]}`,
		u.ID.String())
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "client_not_found")

	got, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{a.ID, b.ID}, got.ClientIDs,
		"carteira anterior tem que sobreviver a um PATCH recusado")
	require.Equal(t, a.ID, *got.ClientID)
}

// A regra "mantém o principal atual quando ele continua na carteira" (§5.3)
// tem que valer também vinda do PATCH — não só do SetClients direto.
func TestUsers_Patch_KeepsCurrentPrincipalWhenStillInWallet(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)
	a, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente A"})
	b, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente B"})
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "principal@acme.com", PasswordHash: "h", Role: "viewer", ClientID: &a.ID, Name: "P",
	})
	require.NoError(t, err)
	require.NoError(t, repo.SetClients(ctx, u.ID, []uuid.UUID{a.ID, b.ID}))
	h := NewUsersHandler(repo, nil)

	// Manda a MESMA carteira com o principal em segundo lugar. Promover
	// client_ids[0] aqui trocaria o principal a cada salvamento do formulário.
	req := reqWithIDParam("PATCH", "/admin/users/x",
		`{"client_ids":["`+b.ID.String()+`","`+a.ID.String()+`"]}`, u.ID.String())
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, a.ID, *got.ClientID, "principal atual tem que ser preservado")
	require.ElementsMatch(t, []uuid.UUID{a.ID, b.ID}, got.ClientIDs)
}

// Mandar client_id E client_ids na mesma requisição: client_ids vence, igual ao
// Create. Aplicar os dois faria o Update podar a carteira pro client_id antes
// do SetClients reconstruí-la — e um SetClients que falhasse depois deixaria a
// carteira truncada, que é exatamente o que o fix anterior eliminou.
func TestUsers_Patch_ClientIDsWinsOverClientID(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)
	a, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente A"})
	b, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente B"})
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "ambos@acme.com", PasswordHash: "h", Role: "viewer", ClientID: &a.ID, Name: "Ambos",
	})
	require.NoError(t, err)
	h := NewUsersHandler(repo, nil)

	req := reqWithIDParam("PATCH", "/admin/users/x",
		`{"client_id":"`+a.ID.String()+`","client_ids":["`+a.ID.String()+`","`+b.ID.String()+`"]}`,
		u.ID.String())
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.ElementsMatch(t, []uuid.UUID{a.ID, b.ID}, got.ClientIDs,
		"client_ids tem que vencer — o client_id sozinho truncaria pra 1")
}
