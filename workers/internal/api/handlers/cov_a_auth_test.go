package handlers

// cov_a_auth_test.go — caminhos de erro e variações do login que as travas
// existentes (auth_test.go) não exercitam: repo ausente, falha de banco em cada
// etapa, login por username, JWT_SECRET ausente e senha errada.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/users"
)

const covASenha = "senha-de-teste-bem-longa-123"

func covALogin(h *AuthHandler, corpo string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(corpo))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	return rec
}

func TestCovA_Auth_Login_RepoAusenteFalhaAlto(t *testing.T) {
	h := NewAuthHandler(nil, nil)
	rec := covALogin(h, `{"email":"a@example.com","password":"x"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "internal error")
}

func TestCovA_Auth_Login_Credenciais(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	repo := users.NewRepo(pool)
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := NewAuthHandler(pool, repo)

	hash, err := bcrypt.GenerateFromPassword([]byte(covASenha), 10)
	require.NoError(t, err)
	username := "joao.teste"
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "joao@example.com", Username: &username, PasswordHash: string(hash), Role: "admin", Name: "Joao Teste",
	})
	require.NoError(t, err)

	t.Run("senha errada 401 sem revelar qual campo", func(t *testing.T) {
		rec := covALogin(h, `{"email":"joao@example.com","password":"errada"}`)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid credentials")
	})

	t.Run("e-mail desconhecido 401 com a mesma mensagem", func(t *testing.T) {
		rec := covALogin(h, `{"email":"ninguem@example.com","password":"`+covASenha+`"}`)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid credentials")
	})

	t.Run("username desconhecido 401", func(t *testing.T) {
		rec := covALogin(h, `{"email":"fantasma","password":"`+covASenha+`"}`)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("login por username (sem @) devolve token válido do usuário", func(t *testing.T) {
		rec := covALogin(h, `{"email":"joao.teste","password":"`+covASenha+`"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var resp struct {
			Token     string `json:"token"`
			ExpiresAt string `json:"expires_at"`
			User      struct {
				ID       string  `json:"id"`
				Username *string `json:"username"`
				Role     string  `json:"role"`
			} `json:"user"`
		}
		covADecodeInto(t, rec, &resp)
		assert.Equal(t, u.ID.String(), resp.User.ID)
		require.NotNil(t, resp.User.Username)
		assert.Equal(t, "joao.teste", *resp.User.Username)
		assert.Equal(t, "admin", resp.User.Role)
		assert.NotEmpty(t, resp.ExpiresAt)

		claims, err := auth.ParseToken(resp.Token)
		require.NoError(t, err)
		assert.Equal(t, u.ID, claims.UserID)
		assert.Equal(t, "admin", claims.Role)
		assert.Nil(t, claims.ClientID, "admin não carrega cliente")
	})

	t.Run("JWT_SECRET curto: o login falha com 500 e não emite token", func(t *testing.T) {
		t.Setenv("JWT_SECRET", "curto")
		rec := covALogin(h, `{"email":"joao@example.com","password":"`+covASenha+`"}`)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), "token")
	})
}

func TestCovA_Auth_Login_FalhaDeBancoEmCadaEtapa(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	good := users.NewRepo(pool)
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")

	t.Run("busca do usuário falha: 500 (não confunde com credencial inválida)", func(t *testing.T) {
		h := NewAuthHandler(covAClosedPool(t), users.NewRepo(covAClosedPool(t)))
		for _, ident := range []string{"a@example.com", "semarroba"} {
			rec := covALogin(h, `{"email":"`+ident+`","password":"x"}`)
			assert.Equal(t, http.StatusInternalServerError, rec.Code, ident)
		}
	})

	t.Run("checagem de clientes ativos falha: 500", func(t *testing.T) {
		cli, err := catalog.NewClients(pool).Create(ctx, catalog.CreateClientInput{Name: "Cliente Teste"})
		require.NoError(t, err)
		hash, err := bcrypt.GenerateFromPassword([]byte(covASenha), 10)
		require.NoError(t, err)
		_, err = good.Create(ctx, users.CreateInput{
			Email: "viewer@example.com", PasswordHash: string(hash), Role: "viewer", ClientID: &cli.ID, Name: "Viewer Teste",
		})
		require.NoError(t, err)

		// repo bom (acha o usuário e valida a senha), pool cru fechado (consulta de clientes falha).
		h := NewAuthHandler(covAClosedPool(t), good)
		rec := covALogin(h, `{"email":"viewer@example.com","password":"`+covASenha+`"}`)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), "token")
	})
}
