package handlers

// campaign_materials_db_test.go — TRAVA as rotas de vínculo material↔campanha e a
// de troca de tipo do material (status, corpo, linha no banco, chamadas ao
// supervisor, categoria recalculada) ANTES de a lógica sair do handler para
// internal/acoes (plano da Fatia 3, Task 9). Passam no código antigo e no novo.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

type cenarioVinculo struct {
	*cenarioRegra
	sup *mcptest.SupervisorFalso
	h   *CampaignMaterialsHandler
	r   chi.Router
	// outro material do mesmo cliente, ainda sem vínculo
	outro uuid.UUID
}

func novoCenarioVinculo(t *testing.T) *cenarioVinculo {
	t.Helper()
	c := &cenarioVinculo{cenarioRegra: novoCenarioRegra(t)}
	c.outro = mcptest.Material(t, c.ctx, c.pool, c.cliente, c.tipoA, "Outro spot")
	c.sup = &mcptest.SupervisorFalso{}
	c.h = &CampaignMaterialsHandler{
		Repo: catalog.NewCampaignMaterials(c.pool), CampaignRepo: catalog.NewCampaigns(c.pool), Supervisor: c.sup,
	}
	c.r = chi.NewRouter()
	c.r.Post("/campaigns/{campaignID}/materials", c.h.Link)
	c.r.Put("/campaigns/{campaignID}/materials/{materialID}/stations", c.h.UpdateStations)
	c.r.Delete("/campaigns/{campaignID}/materials/{materialID}", c.h.Unlink)
	return c
}

func (c *cenarioVinculo) chama(t *testing.T, metodo, alvo string, corpo any) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if corpo != nil {
		var err error
		b, err = json.Marshal(corpo)
		require.NoError(t, err)
	}
	rec := httptest.NewRecorder()
	c.r.ServeHTTP(rec, httptest.NewRequest(metodo, alvo, bytes.NewReader(b)))
	return rec
}

// vinculo lê as emissoras do vínculo; existe=false se não há linha.
func (c *cenarioVinculo) vinculo(t *testing.T, material uuid.UUID) (emissoras []uuid.UUID, existe bool) {
	t.Helper()
	var n int
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT count(*) FROM campaign_materials WHERE campaign_id=$1 AND material_id=$2`, c.campanha, material).Scan(&n))
	if n == 0 {
		return nil, false
	}
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT target_stations FROM campaign_materials WHERE campaign_id=$1 AND material_id=$2`, c.campanha, material).Scan(&emissoras))
	return emissoras, true
}

func (c *cenarioVinculo) reloads() int { return c.sup.Contagem("Reload", c.campanha) }

func TestVinculoHTTP_Link_201GravaOVinculoERecarregaUmaVez(t *testing.T) {
	c := novoCenarioVinculo(t)

	rec := c.chama(t, http.MethodPost, "/campaigns/"+c.campanha.String()+"/materials",
		map[string]any{"material_id": c.outro.String(), "target_stations": []string{c.emissora.String()}})

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Empty(t, rec.Body.String())
	est, ok := c.vinculo(t, c.outro)
	require.True(t, ok)
	require.Equal(t, []uuid.UUID{c.emissora}, est)
	require.Equal(t, 1, c.reloads())
	require.Len(t, c.sup.Chamadas(), 1, "só o Reload: vincular não mexe em target_stations nem recategoriza")
}

func TestVinculoHTTP_Link_RepetirTrocaAsEmissorasESemListaFicaVazio(t *testing.T) {
	c := novoCenarioVinculo(t)
	alvo := "/campaigns/" + c.campanha.String() + "/materials"

	require.Equal(t, http.StatusCreated, c.chama(t, http.MethodPost, alvo,
		map[string]any{"material_id": c.outro.String(), "target_stations": []string{c.emissora.String()}}).Code)
	require.Equal(t, http.StatusCreated, c.chama(t, http.MethodPost, alvo,
		map[string]any{"material_id": c.outro.String()}).Code)

	est, ok := c.vinculo(t, c.outro)
	require.True(t, ok)
	require.Empty(t, est, "upsert: a segunda chamada (sem lista) substituiu as emissoras por vazio")
	require.Equal(t, 2, c.reloads())
}

func TestVinculoHTTP_Link_SemSupervisorSoGrava(t *testing.T) {
	c := novoCenarioVinculo(t)
	c.h.Supervisor = nil

	rec := c.chama(t, http.MethodPost, "/campaigns/"+c.campanha.String()+"/materials",
		map[string]any{"material_id": c.outro.String()})

	require.Equal(t, http.StatusCreated, rec.Code)
	_, ok := c.vinculo(t, c.outro)
	require.True(t, ok)
	require.Zero(t, c.reloads())
}

// O banco já foi mutado: falha do reload é só logada e a rota responde sucesso
// (o reconciler de 30 s é a rede de segurança).
func TestVinculoHTTP_FalhaDoReloadNaoFalhaARota(t *testing.T) {
	c := novoCenarioVinculo(t)
	c.sup.ErroReload = errors.New("nats caiu")
	base := "/campaigns/" + c.campanha.String() + "/materials"

	require.Equal(t, http.StatusCreated, c.chama(t, http.MethodPost, base, map[string]any{"material_id": c.outro.String()}).Code)
	require.Equal(t, http.StatusNoContent, c.chama(t, http.MethodPut, base+"/"+c.outro.String()+"/stations",
		map[string]any{"target_stations": []string{c.emissora.String()}}).Code)
	require.Equal(t, http.StatusNoContent, c.chama(t, http.MethodDelete, base+"/"+c.outro.String(), nil).Code)
	require.Equal(t, 3, c.reloads(), "tentou recarregar nas três")
}

func TestVinculoHTTP_Link_MaterialInexistenteDa500PorCausaDaFK(t *testing.T) {
	c := novoCenarioVinculo(t)

	rec := c.chama(t, http.MethodPost, "/campaigns/"+c.campanha.String()+"/materials",
		map[string]any{"material_id": uuid.NewString()})

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), "internal error")
	require.Zero(t, c.reloads(), "falhou antes de gravar: não recarrega")
}

func TestVinculoHTTP_UpdateStations_204TrocaEmissorasERecarrega(t *testing.T) {
	c := novoCenarioVinculo(t)
	mcptest.Vincular(t, c.ctx, c.pool, c.campanha, c.outro) // vínculo com lista vazia
	base := "/campaigns/" + c.campanha.String() + "/materials/" + c.outro.String() + "/stations"

	rec := c.chama(t, http.MethodPut, base, map[string]any{"target_stations": []string{c.emissora.String()}})

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	est, _ := c.vinculo(t, c.outro)
	require.Equal(t, []uuid.UUID{c.emissora}, est)
	require.Equal(t, 1, c.reloads())
}

func TestVinculoHTTP_UpdateStations_VinculoInexistenteTambemDa204(t *testing.T) {
	c := novoCenarioVinculo(t)

	rec := c.chama(t, http.MethodPut, "/campaigns/"+c.campanha.String()+"/materials/"+uuid.NewString()+"/stations",
		map[string]any{"target_stations": []string{}})

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, 1, c.reloads(), "a rota sempre recarregou, mesmo sem linha afetada")
}

func TestVinculoHTTP_Unlink_204ApagaOVinculoERecarrega(t *testing.T) {
	c := novoCenarioVinculo(t)
	mcptest.Vincular(t, c.ctx, c.pool, c.campanha, c.outro, c.emissora)

	rec := c.chama(t, http.MethodDelete, "/campaigns/"+c.campanha.String()+"/materials/"+c.outro.String(), nil)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	_, ok := c.vinculo(t, c.outro)
	require.False(t, ok)
	require.Equal(t, 1, c.reloads())
	// O passado NÃO muda: a tocada já gravada segue na campanha com a categoria dela.
	p, b := c.categoria(t)
	require.Equal(t, "bonus", p)
	require.Equal(t, "bonus", b)

	// Desvincular o que não existe mais também é 204.
	require.Equal(t, http.StatusNoContent,
		c.chama(t, http.MethodDelete, "/campaigns/"+c.campanha.String()+"/materials/"+c.outro.String(), nil).Code)
}

// ─────────────────────────── troca de tipo do material ───────────────────────────

type cenarioTipo struct {
	*cenarioRegra
	h *MaterialsHandler
	r chi.Router
}

func novoCenarioTipo(t *testing.T) *cenarioTipo {
	t.Helper()
	c := &cenarioTipo{cenarioRegra: novoCenarioRegra(t)}
	c.h = &MaterialsHandler{Repo: catalog.NewMaterials(c.pool), DistRules: catalog.NewDistributionRules(c.pool)}
	c.r = chi.NewRouter()
	c.r.Patch("/materials/{id}/type", c.h.UpdateType)
	return c
}

func (c *cenarioTipo) patchTipo(t *testing.T, material uuid.UUID, corpo string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c.r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/materials/"+material.String()+"/type", bytes.NewReader([]byte(corpo))))
	return rec
}

func tipoDoMaterial(t *testing.T, ctx context.Context, pool *pgxpool.Pool, material uuid.UUID) *uuid.UUID {
	t.Helper()
	var tipo *uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT type_id FROM materials WHERE id = $1`, material).Scan(&tipo))
	return tipo
}

func TestMaterialTipoHTTP_UpdateType_204TrocaOTipoERecategorizaEmSegundoPlano(t *testing.T) {
	c := novoCenarioTipo(t)
	// A regra é do tipo B; o material (e a tocada) é do tipo A → a tocada é bonus.
	mcptest.Regra(t, c.ctx, c.pool, c.campanha, c.tipoB, c.emissora, "2026-06-01", "2026-06-30", 127, "08:00", "12:00", 1)
	c.esperaCategoria(t, "bonus")

	rec := c.patchTipo(t, c.material, `{"type_id":"`+c.tipoB.String()+`"}`)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Empty(t, rec.Body.String())
	got := tipoDoMaterial(t, c.ctx, c.pool, c.material)
	require.NotNil(t, got)
	require.Equal(t, c.tipoB, *got)
	// Agora o material casa a regra do tipo B e a tocada das 10:30 cabe na faixa.
	c.esperaCategoria(t, "in_slot")
}

func TestMaterialTipoHTTP_UpdateType_NullLimpaOTipo(t *testing.T) {
	c := novoCenarioTipo(t)

	rec := c.patchTipo(t, c.material, `{"type_id":null}`)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Nil(t, tipoDoMaterial(t, c.ctx, c.pool, c.material))
}

func TestMaterialTipoHTTP_UpdateType_SemDistRulesSoGrava(t *testing.T) {
	c := novoCenarioTipo(t)
	c.h.DistRules = nil
	mcptest.Regra(t, c.ctx, c.pool, c.campanha, c.tipoB, c.emissora, "2026-06-01", "2026-06-30", 127, "08:00", "12:00", 1)

	rec := c.patchTipo(t, c.material, `{"type_id":"`+c.tipoB.String()+`"}`)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, c.tipoB, *tipoDoMaterial(t, c.ctx, c.pool, c.material))
	time.Sleep(300 * time.Millisecond) // quem recategorizaria roda em goroutine
	p, _ := c.categoria(t)
	require.Equal(t, "bonus", p, "sem recategorizador a categoria fica como estava")
}

func TestMaterialTipoHTTP_UpdateType_MaterialInexistenteTambemDa204(t *testing.T) {
	c := novoCenarioTipo(t)

	rec := c.patchTipo(t, uuid.New(), `{"type_id":"`+c.tipoB.String()+`"}`)

	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestMaterialTipoHTTP_UpdateType_Recusas400(t *testing.T) {
	c := novoCenarioTipo(t)

	require.Equal(t, http.StatusBadRequest, c.patchTipo(t, c.material, `não é json`).Code)
	rec := httptest.NewRecorder()
	c.r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/materials/xx/type", bytes.NewReader([]byte(`{}`))))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid id")
}
