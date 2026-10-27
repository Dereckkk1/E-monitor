package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/mcp/tokens"
)

func TestTokensHTTP_CriarListarRevogar(t *testing.T) {
	t.Setenv("JWT_SECRET", mcptest.SegredoTeste)
	ctx, pool := mcptest.DB(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	jwt, err := auth.IssueToken(admin, "admin")
	require.NoError(t, err)
	h := &TokensHTTP{Tokens: tokens.NovoStore(pool, []byte(mcptest.SegredoTeste)), DB: pool, Log: zap.NewNop()}
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireJWT, auth.RequireRole("admin"))
		r.Get("/mcp/tokens", h.Listar)
		r.Post("/mcp/tokens", h.Criar)
		r.Delete("/mcp/tokens/{id}", h.Revogar)
	})
	fazer := func(metodo, caminho string, corpo any) (*httptest.ResponseRecorder, map[string]any) {
		var b bytes.Buffer
		if corpo != nil {
			_ = json.NewEncoder(&b).Encode(corpo)
		}
		req := httptest.NewRequest(metodo, caminho, &b)
		req.Header.Set("Authorization", "Bearer "+jwt)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return rec, m
	}

	rec, _ := fazer(http.MethodPost, "/mcp/tokens", map[string]any{"nome": "", "validade_dias": 30})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	rec, _ = fazer(http.MethodPost, "/mcp/tokens", map[string]any{"nome": "x", "validade_dias": 400})
	require.Equal(t, http.StatusBadRequest, rec.Code, "teto de 365 dias")

	rec, criado := fazer(http.MethodPost, "/mcp/tokens", map[string]any{"nome": "Claude Code no notebook", "validade_dias": 90})
	require.Equal(t, http.StatusCreated, rec.Code)
	require.True(t, strings.HasPrefix(criado["token"].(string), tokens.PrefixoAcesso))
	id := criado["item"].(map[string]any)["id"].(string)

	rec, lista := fazer(http.MethodGet, "/mcp/tokens", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	itens := lista["items"].([]any)
	require.Len(t, itens, 1)
	require.NotContains(t, itens[0], "token", "o texto claro só aparece na criação")

	rec, _ = fazer(http.MethodDelete, "/mcp/tokens/"+id, nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec, _ = fazer(http.MethodDelete, "/mcp/tokens/"+id, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)

	var auditados int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE actor_id = $1 AND action IN ('mcp.token.criado','mcp.token.revogado')`, admin).Scan(&auditados))
	require.Equal(t, 2, auditados)
}

// O JWT do portal leva o role por até 8h: um admin rebaixado depois de logar
// ainda passa no RequireRole("admin"). Criar não pode confiar nele — o token
// pessoal ressuscitaria se a pessoa voltasse a ser admin.
func TestTokensHTTP_CriarRecusaAdminRebaixado(t *testing.T) {
	t.Setenv("JWT_SECRET", mcptest.SegredoTeste)
	ctx, pool := mcptest.DB(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	jwt, err := auth.IssueToken(admin, "admin")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE users SET role = 'operator' WHERE id = $1`, admin)
	require.NoError(t, err)

	h := &TokensHTTP{Tokens: tokens.NovoStore(pool, []byte(mcptest.SegredoTeste)), DB: pool, Log: zap.NewNop()}
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireJWT, auth.RequireRole("admin"))
		r.Post("/mcp/tokens", h.Criar)
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp/tokens", strings.NewReader(`{"nome":"x","validade_dias":30}`))
	req.Header.Set("Authorization", "Bearer "+jwt)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_tokens WHERE user_id = $1`, admin).Scan(&n))
	require.Zero(t, n, "nenhuma credencial pode ter sido gravada")
}
