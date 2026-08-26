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

	"radiocheck/internal/db"
	"radiocheck/internal/dbtest"
	"radiocheck/internal/hub"
	"radiocheck/internal/users"
)

// ── Testes sem DB ────────────────────────────────────────────────────────

func TestHubSSO_BadJSON(t *testing.T) {
	h := NewHubSSOHandler(nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/sso", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHubSSO_MissingCode(t *testing.T) {
	h := NewHubSSOHandler(nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/sso", strings.NewReader(`{"code":""}`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "missing_code")
}

// papelDoUsuario é a fronteira que a D9 protege: o hub não escolhe role aqui.
func TestPapelDoUsuario(t *testing.T) {
	require.Equal(t, "admin", papelDoUsuario(&hub.UserPayload{Level: "internal"}))
	require.Equal(t, "viewer", papelDoUsuario(&hub.UserPayload{Level: "client"}))

	// O provisionProfile é ignorado de propósito no E-monitor (não há segundo
	// papel de cliente). Se um dia alguém ligar a leitura dele sem allowlist,
	// este teste cai — e é para cair.
	perfilPedindoAdmin := json.RawMessage(`{"role":"admin","userType":"admin"}`)
	require.Equal(t, "viewer", papelDoUsuario(&hub.UserPayload{
		Level: "client", ProvisionProfile: perfilPedindoAdmin,
	}))
}

// ── Testes com DB ────────────────────────────────────────────────────────

func newHubSSOTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	dbtest.GuardOrSkip(t, ctx, pool)
	_, err = pool.Exec(ctx, `TRUNCATE users, clients RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	return ctx, pool
}

// hubFalso responde o que o teste mandar, no lugar do E-Hub de verdade.
//
// Mock no nível do HTTP, e não do nosso código: assim o `internal/hub` inteiro
// — headers, tradução de status, parsing — roda de verdade. O que isto NÃO
// prova é que o hub real responde neste formato; para isso existe o roteiro de
// ponta a ponta com os dois sistemas no ar (§15 do RFC).
func hubFalso(t *testing.T, status int, corpo any) (*hub.Client, *[]string) {
	t.Helper()
	chaves := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chaves = append(chaves, r.Header.Get("X-Hub-Platform-Key"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if corpo != nil {
			_ = json.NewEncoder(w).Encode(corpo)
		}
	}))
	t.Cleanup(srv.Close)
	return hub.New(srv.URL, "pk_teste"), &chaves
}

func payloadCliente(hubClientID string) map[string]any {
	return map[string]any{
		"hubUserId": "665f1a2b3c4d5e6f70819200",
		"email":     "maria@clientex.com.br",
		"name":      "Maria Souza",
		"phone":     "+5511999999999",
		"level":     "client",
		"client": map[string]any{
			"hubClientId": hubClientID,
			"name":        "Cliente X Ltda",
			"cnpj":        "12.345.678/0001-90",
		},
		"provisionProfile": map[string]any{},
		"externalId":       nil,
	}
}

func payloadInterno() map[string]any {
	return map[string]any{
		"hubUserId":        "665f1a2b3c4d5e6f70819999",
		"email":            "time@emidiastec.com",
		"name":             "Equipe",
		"phone":            nil,
		"level":            "internal",
		"client":           nil,
		"provisionProfile": map[string]any{},
		"externalId":       nil,
	}
}

func entrar(t *testing.T, h *HubSSOHandler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/sso", strings.NewReader(`{"code":"hs_abc"}`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	return rec
}

// criaCliente insere um tenant local, opcionalmente já vinculado ao hub.
func criaCliente(t *testing.T, ctx context.Context, pool *pgxpool.Pool, hubID *string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO clients (name, hub_id) VALUES ($1, $2) RETURNING id`,
		"Cliente X Ltda", hubID).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestHubSSO_JIT_CriaViewerVinculadoAoCliente(t *testing.T) {
	ctx, pool := newHubSSOTestPool(t)
	repo := users.NewRepo(pool)
	hubID := "665f2b3c4d5e6f7081920300"
	clientID := criaCliente(t, ctx, pool, &hubID)

	hc, chaves := hubFalso(t, http.StatusOK, payloadCliente(hubID))
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	rec := entrar(t, NewHubSSOHandler(pool, repo, hc))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"pk_teste"}, *chaves, "a chave da plataforma tem que ir no header")

	u, err := repo.GetByEmail(ctx, "maria@clientex.com.br")
	require.NoError(t, err)
	require.Equal(t, "viewer", u.Role)
	require.NotNil(t, u.ClientID)
	require.Equal(t, clientID, *u.ClientID, "tem que cair no tenant resolvido por clients.hub_id")
	require.NotNil(t, u.Phone)
	require.True(t, u.IsActive)
}

func TestHubSSO_JIT_InternoViraAdminSemCliente(t *testing.T) {
	ctx, pool := newHubSSOTestPool(t)
	repo := users.NewRepo(pool)

	hc, _ := hubFalso(t, http.StatusOK, payloadInterno())
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	rec := entrar(t, NewHubSSOHandler(pool, repo, hc))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	u, err := repo.GetByEmail(ctx, "time@emidiastec.com")
	require.NoError(t, err)
	require.Equal(t, "admin", u.Role)
	require.Nil(t, u.ClientID)
}

// O token emitido é o mesmo do login normal — 8h, com role e client_id dentro.
func TestHubSSO_EmiteTokenNoMesmoEnvelopeDoLogin(t *testing.T) {
	ctx, pool := newHubSSOTestPool(t)
	repo := users.NewRepo(pool)
	hubID := "665f2b3c4d5e6f7081920300"
	criaCliente(t, ctx, pool, &hubID)

	hc, _ := hubFalso(t, http.StatusOK, payloadCliente(hubID))
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	rec := entrar(t, NewHubSSOHandler(pool, repo, hc))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp loginResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Token)
	require.False(t, resp.ExpiresAt.IsZero())
	require.Equal(t, "maria@clientex.com.br", resp.User.Email)
	require.Equal(t, "viewer", resp.User.Role)
	require.NotNil(t, resp.User.ClientID)
}

// O caso que o §8.1 nomeia: cliente sem tenant local mapeado.
func TestHubSSO_ClienteSemTenantLocal_NaoInventaCadastro(t *testing.T) {
	ctx, pool := newHubSSOTestPool(t)
	repo := users.NewRepo(pool)
	// O cliente existe no hub, mas NINGUÉM mapeou clients.hub_id aqui.
	criaCliente(t, ctx, pool, nil)

	hc, _ := hubFalso(t, http.StatusOK, payloadCliente("hub-id-nao-mapeado"))
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	rec := entrar(t, NewHubSSOHandler(pool, repo, hc))

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "client_not_provisioned")

	// E — o que importa — NADA foi criado. Um usuário órfão de cliente seria
	// pior que o erro: ele logaria sem escopo nenhum.
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n))
	require.Equal(t, 0, n)
}

func TestHubSSO_VinculaPorEmailEmVezDeDuplicar(t *testing.T) {
	ctx, pool := newHubSSOTestPool(t)
	repo := users.NewRepo(pool)
	hubID := "665f2b3c4d5e6f7081920300"
	clientID := criaCliente(t, ctx, pool, &hubID)

	hash, err := bcrypt.GenerateFromPassword([]byte("senha-local-forte-123"), 10)
	require.NoError(t, err)
	local, err := repo.Create(ctx, users.CreateInput{
		Email: "maria@clientex.com.br", PasswordHash: string(hash),
		Role: "viewer", ClientID: &clientID, Name: "Maria (local)",
	})
	require.NoError(t, err)

	hc, _ := hubFalso(t, http.StatusOK, payloadCliente(hubID))
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	require.Equal(t, http.StatusOK, entrar(t, NewHubSSOHandler(pool, repo, hc)).Code)

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n))
	require.Equal(t, 1, n, "não pode existir uma segunda conta para a mesma pessoa")

	depois, err := repo.Get(ctx, local.ID)
	require.NoError(t, err)
	require.Equal(t, "Maria (local)", depois.Name, "o hub não sobrescreve cadastro local nesta fase")

	var gravado *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT hub_id FROM users WHERE id=$1`, local.ID).Scan(&gravado))
	require.NotNil(t, gravado)
	require.Equal(t, "665f1a2b3c4d5e6f70819200", *gravado)
}

func TestHubSSO_SegundaEntradaAchaPeloVinculo(t *testing.T) {
	ctx, pool := newHubSSOTestPool(t)
	repo := users.NewRepo(pool)
	hubID := "665f2b3c4d5e6f7081920300"
	criaCliente(t, ctx, pool, &hubID)
	hc, _ := hubFalso(t, http.StatusOK, payloadCliente(hubID))
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := NewHubSSOHandler(pool, repo, hc)

	require.Equal(t, http.StatusOK, entrar(t, h).Code)
	require.Equal(t, http.StatusOK, entrar(t, h).Code)

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n))
	require.Equal(t, 1, n)
}

// ── Gates locais: o hub é dono da identidade, não da permissão de entrar ──

func TestHubSSO_ContaDesativadaAquiNaoEntra(t *testing.T) {
	ctx, pool := newHubSSOTestPool(t)
	repo := users.NewRepo(pool)
	hubID := "665f2b3c4d5e6f7081920300"
	clientID := criaCliente(t, ctx, pool, &hubID)

	hash, _ := bcrypt.GenerateFromPassword([]byte("senha-local-forte-123"), 10)
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "maria@clientex.com.br", PasswordHash: string(hash),
		Role: "viewer", ClientID: &clientID, Name: "Maria",
	})
	require.NoError(t, err)
	off := false
	_, err = repo.Update(ctx, u.ID, users.UpdateInput{IsActive: &off})
	require.NoError(t, err)

	hc, _ := hubFalso(t, http.StatusOK, payloadCliente(hubID))
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	rec := entrar(t, NewHubSSOHandler(pool, repo, hc))

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "account_disabled")
}

func TestHubSSO_ClienteDesativadoAquiNaoEntra(t *testing.T) {
	ctx, pool := newHubSSOTestPool(t)
	repo := users.NewRepo(pool)
	hubID := "665f2b3c4d5e6f7081920300"
	clientID := criaCliente(t, ctx, pool, &hubID)
	_, err := pool.Exec(ctx, `UPDATE clients SET is_active = FALSE WHERE id = $1`, clientID)
	require.NoError(t, err)

	hc, _ := hubFalso(t, http.StatusOK, payloadCliente(hubID))
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	rec := entrar(t, NewHubSSOHandler(pool, repo, hc))

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "client_disabled")
}

// ── Tradução dos erros do hub ────────────────────────────────────────────

func TestHubSSO_CodigoExpiradoVira410(t *testing.T) {
	_, pool := newHubSSOTestPool(t)
	hc, _ := hubFalso(t, http.StatusGone, map[string]any{"error": map[string]any{"code": "code_invalid_or_expired"}})
	rec := entrar(t, NewHubSSOHandler(pool, users.NewRepo(pool), hc))
	require.Equal(t, http.StatusGone, rec.Code)
	require.Contains(t, rec.Body.String(), "expirou")
}

// A chave recusada é problema NOSSO, não de quem clicou — e um 401 numa rota de
// login convidaria o frontend a tentar renovar uma sessão que não existe.
func TestHubSSO_ChaveRecusadaVira503_NaoRepassa401(t *testing.T) {
	_, pool := newHubSSOTestPool(t)
	hc, _ := hubFalso(t, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "invalid_platform_key"}})
	rec := entrar(t, NewHubSSOHandler(pool, users.NewRepo(pool), hc))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHubSSO_SemEntitlementVira403(t *testing.T) {
	_, pool := newHubSSOTestPool(t)
	hc, _ := hubFalso(t, http.StatusForbidden, map[string]any{"error": map[string]any{"code": "product_not_enabled"}})
	rec := entrar(t, NewHubSSOHandler(pool, users.NewRepo(pool), hc))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHubSSO_NaoConfigurado_503SemChamarNinguem(t *testing.T) {
	_, pool := newHubSSOTestPool(t)
	// baseURL e chave vazias = integração desligada nesta instalação.
	rec := entrar(t, NewHubSSOHandler(pool, users.NewRepo(pool), hub.New("", "")))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
