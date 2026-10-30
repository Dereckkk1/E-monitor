package handlers

// clientes_acoes_lock_test.go — TRAVA o que as rotas de ativar/desativar/apagar
// cliente e apagar material fazem hoje (status, corpo, linha no banco), ANTES de
// a lógica sair do handler para internal/acoes (Fatia 4, Task 13). Passa no
// código antigo e no novo — exceto o caso "material vinculado", que muda de 500
// para 409 no passo 3 da task (teste dele troca junto).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

type cenarioCliente struct {
	ctx  context.Context
	pool *pgxpool.Pool
	r    chi.Router
}

func novoCenarioCliente(t *testing.T) *cenarioCliente {
	t.Helper()
	ctx, pool := mcptest.DBCatalogo(t)
	ch := &ClientsHandler{Repo: catalog.NewClients(pool)}
	mh := &MaterialsHandler{Repo: catalog.NewMaterials(pool)}
	r := chi.NewRouter()
	r.Delete("/clients/{id}", ch.Delete)
	r.Post("/clients/{id}/deactivate", ch.Deactivate)
	r.Post("/clients/{id}/activate", ch.Activate)
	r.Delete("/materials/{id}", mh.Delete)
	return &cenarioCliente{ctx: ctx, pool: pool, r: r}
}

func (c *cenarioCliente) chamar(metodo, alvo string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c.r.ServeHTTP(rec, httptest.NewRequest(metodo, alvo, nil))
	return rec
}

func (c *cenarioCliente) ativo(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var a bool
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT is_active FROM clients WHERE id=$1`, id).Scan(&a))
	return a
}

func (c *cenarioCliente) existe(t *testing.T, tabela string, id uuid.UUID) bool {
	t.Helper()
	var n int
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT COUNT(*) FROM `+tabela+` WHERE id=$1`, id).Scan(&n))
	return n == 1
}

func TestClienteAtivoHTTP_DesativaEAtiva(t *testing.T) {
	c := novoCenarioCliente(t)
	id := mcptest.Cliente(t, c.ctx, c.pool, "Cliente Ativo")
	require.True(t, c.ativo(t, id))

	rec := c.chamar(http.MethodPost, "/clients/"+id.String()+"/deactivate")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out catalog.Client
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, id, out.ID)
	require.False(t, out.IsActive)
	require.False(t, c.ativo(t, id))

	rec = c.chamar(http.MethodPost, "/clients/"+id.String()+"/activate")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.True(t, out.IsActive)
	require.True(t, c.ativo(t, id))
}

func TestClienteAtivoHTTP_404E400(t *testing.T) {
	c := novoCenarioCliente(t)
	for _, op := range []string{"deactivate", "activate"} {
		rec := c.chamar(http.MethodPost, "/clients/"+uuid.NewString()+"/"+op)
		require.Equal(t, http.StatusNotFound, rec.Code, op)
		require.Contains(t, rec.Body.String(), "not found")

		rec = c.chamar(http.MethodPost, "/clients/xyz/"+op)
		require.Equal(t, http.StatusBadRequest, rec.Code, op)
		require.Contains(t, rec.Body.String(), "invalid id")
	}
}

func TestClienteApagarHTTP_204ESumiu(t *testing.T) {
	c := novoCenarioCliente(t)
	id := mcptest.Cliente(t, c.ctx, c.pool, "Cliente Apagar")
	rec := c.chamar(http.MethodDelete, "/clients/"+id.String())
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.False(t, c.existe(t, "clients", id))
}

func TestClienteApagarHTTP_404E400(t *testing.T) {
	c := novoCenarioCliente(t)
	rec := c.chamar(http.MethodDelete, "/clients/"+uuid.NewString())
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "not found")
	rec = c.chamar(http.MethodDelete, "/clients/xyz")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestClienteApagarHTTP_409ComVinculos(t *testing.T) {
	c := novoCenarioCliente(t)
	id := mcptest.Cliente(t, c.ctx, c.pool, "Cliente Vinculos")
	tipo := mcptest.TipoMaterial(t, c.ctx, c.pool, "Spot Vinc")
	mcptest.Material(t, c.ctx, c.pool, id, tipo, "Mat A")
	mcptest.Material(t, c.ctx, c.pool, id, tipo, "Mat B")
	mcptest.Campanha(t, c.ctx, c.pool, id, "Camp Vinc", "2026-06-01", "2026-06-30")
	_, err := c.pool.Exec(c.ctx,
		`INSERT INTO users (email, password_hash, role, name, client_id) VALUES ($1, 'x', 'viewer', 'V', $2)`,
		"viewer-"+uuid.NewString()[:8]+"@teste", id)
	require.NoError(t, err)

	rec := c.chamar(http.MethodDelete, "/clients/"+id.String())
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	var corpo map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
	require.Equal(t, map[string]any{
		"error": "client_has_dependents", "campaigns": float64(1), "materials": float64(2), "users": float64(1),
	}, corpo)
	require.True(t, c.existe(t, "clients", id))
}

func TestMaterialApagarHTTP_204(t *testing.T) {
	c := novoCenarioCliente(t)
	cli := mcptest.Cliente(t, c.ctx, c.pool, "Cliente Mat")
	tipo := mcptest.TipoMaterial(t, c.ctx, c.pool, "Spot Mat")
	m := mcptest.Material(t, c.ctx, c.pool, cli, tipo, "Mat Del")
	rec := c.chamar(http.MethodDelete, "/materials/"+m.String())
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.False(t, c.existe(t, "materials", m))
}

func TestMaterialApagarHTTP_Inexistente204(t *testing.T) {
	c := novoCenarioCliente(t)
	rec := c.chamar(http.MethodDelete, "/materials/"+uuid.NewString())
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec = c.chamar(http.MethodDelete, "/materials/xyz")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMaterialApagarHTTP_VinculadoHoje500(t *testing.T) {
	c := novoCenarioCliente(t)
	cli := mcptest.Cliente(t, c.ctx, c.pool, "Cliente Mat Vinc")
	tipo := mcptest.TipoMaterial(t, c.ctx, c.pool, "Spot Mat Vinc")
	em := mcptest.Emissora(t, c.ctx, c.pool, "Rádio Mat Vinc", 0)
	m := mcptest.Material(t, c.ctx, c.pool, cli, tipo, "Mat Vinculado")
	camp := mcptest.Campanha(t, c.ctx, c.pool, cli, "Camp Mat Vinc", "2026-06-01", "2026-06-30", em)
	mcptest.Vincular(t, c.ctx, c.pool, camp, m, em)

	rec := c.chamar(http.MethodDelete, "/materials/"+m.String())
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	require.True(t, c.existe(t, "materials", m))
}
