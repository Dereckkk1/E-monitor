package handlers

// cov_a_campaigns_test.go — campanhas (listas, detalhe, financeiro, recusas e
// erros internos), vínculo material×campanha, regras e ajustes de distribuição:
// os caminhos de leitura, escopo de viewer e erro que as travas existentes não
// exercitam. Banco real.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

type covACampCen struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	cliA     uuid.UUID
	cliB     uuid.UUID
	emissora uuid.UUID
	tipo     uuid.UUID
	material uuid.UUID
	campA1   uuid.UUID // "Campanha Alfa", jun/2026
	campA2   uuid.UUID // "Campanha Beta", jul/2026
	campB    uuid.UUID // do outro cliente, jun/2026
	sup      *mcptest.SupervisorFalso
	h        *CampaignsHandler
}

func covANewCampCen(t *testing.T) *covACampCen {
	t.Helper()
	ctx, pool := covADB(t)
	c := &covACampCen{ctx: ctx, pool: pool, sup: &mcptest.SupervisorFalso{}}
	c.cliA = mcptest.Cliente(t, ctx, pool, "Cliente Alfa")
	c.cliB = mcptest.Cliente(t, ctx, pool, "Cliente Beta")
	c.emissora = mcptest.Emissora(t, ctx, pool, "Rádio Teste FM", 1000)
	c.tipo = mcptest.TipoMaterial(t, ctx, pool, "Spot")
	c.material = mcptest.Material(t, ctx, pool, c.cliA, c.tipo, "Spot Alfa")
	c.campA1 = mcptest.Campanha(t, ctx, pool, c.cliA, "Campanha Alfa", "2026-06-01", "2026-06-30", c.emissora)
	c.campA2 = mcptest.Campanha(t, ctx, pool, c.cliA, "Campanha Beta", "2026-07-01", "2026-07-31", c.emissora)
	c.campB = mcptest.Campanha(t, ctx, pool, c.cliB, "Campanha Gama", "2026-06-01", "2026-06-30", c.emissora)
	mcptest.EstadoCampanha(t, ctx, pool, c.campA1, "ativa")
	mcptest.EstadoCampanha(t, ctx, pool, c.campA2, "programada")
	mcptest.EstadoCampanha(t, ctx, pool, c.campB, "concluida")
	c.h = &CampaignsHandler{Repo: catalog.NewCampaigns(pool), Supervisor: c.sup}
	return c
}

func (c *covACampCen) router(claims *auth.Claims) chi.Router {
	return covARouter(claims, func(r chi.Router) {
		r.Get("/campaigns", c.h.List)
		r.Post("/campaigns", c.h.Create)
		r.Get("/campaigns/financials", c.h.Financials)
		r.Get("/campaigns/{id}", c.h.Get)
		r.Put("/campaigns/{id}", c.h.Update)
		r.Delete("/campaigns/{id}", c.h.Delete)
		r.Put("/campaigns/{id}/fixed-cpm", c.h.UpdateFixedCPM)
		r.Put("/campaigns/{id}/stations", c.h.UpdateStations)
		r.Post("/campaigns/{id}/cancel", c.h.Cancel)
		r.Post("/campaigns/{id}/start", c.h.Start)
	})
}

func (c *covACampCen) get(r http.Handler, alvo string) *httptest.ResponseRecorder {
	return covAServe(r, httptest.NewRequest(http.MethodGet, alvo, nil))
}

func covANomes(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out []string
	itens, _ := covADecode(t, rec)["data"].([]any)
	for _, it := range itens {
		out = append(out, it.(map[string]any)["name"].(string))
	}
	return out
}

func TestCovA_Campaigns_List_SemPaginacao(t *testing.T) {
	c := covANewCampCen(t)
	admin := c.router(covAClaims("admin", uuid.New()))

	t.Run("sem filtro devolve todas, ativa primeiro", func(t *testing.T) {
		got := covANomes(t, c.get(admin, "/campaigns"))
		require.Len(t, got, 3)
		assert.Equal(t, "Campanha Alfa", got[0], "ativa vem antes de programada e concluída")
		assert.Equal(t, "Campanha Beta", got[1])
		assert.Equal(t, "Campanha Gama", got[2])
	})

	t.Run("filtro de status em CSV", func(t *testing.T) {
		assert.Equal(t, []string{"Campanha Alfa"}, covANomes(t, c.get(admin, "/campaigns?status=ativa")))
		assert.ElementsMatch(t, []string{"Campanha Beta", "Campanha Gama"}, covANomes(t, c.get(admin, "/campaigns?status=programada,%20concluida,")))
	})

	t.Run("status inválido 400 cita o valor", func(t *testing.T) {
		rec := c.get(admin, "/campaigns?status=ativa,pausada")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid status filter: pausada")
	})

	t.Run("viewer só enxerga a própria carteira", func(t *testing.T) {
		vr := c.router(covAClaims("viewer", uuid.New(), c.cliB))
		assert.Equal(t, []string{"Campanha Gama"}, covANomes(t, c.get(vr, "/campaigns")))
	})

	t.Run("falha do banco 500", func(t *testing.T) {
		bad := &CampaignsHandler{Repo: catalog.NewCampaigns(covAClosedPool(t))}
		rt := covARouter(nil, func(r chi.Router) { r.Get("/campaigns", bad.List) })
		assert.Equal(t, http.StatusInternalServerError, c.get(rt, "/campaigns").Code)
		assert.Equal(t, http.StatusInternalServerError, c.get(rt, "/campaigns?page=1").Code)
	})
}

func TestCovA_Campaigns_List_Paginada(t *testing.T) {
	c := covANewCampCen(t)
	admin := c.router(covAClaims("admin", uuid.New()))

	t.Run("envelope de paginação e limites", func(t *testing.T) {
		rec := c.get(admin, "/campaigns?page=2&page_size=2")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.EqualValues(t, 3, body["total"])
		assert.EqualValues(t, 2, body["total_pages"])
		assert.EqualValues(t, 2, body["page"])
		assert.EqualValues(t, 2, body["page_size"])
		assert.Len(t, body["data"].([]any), 1)

		body = covADecode(t, c.get(admin, "/campaigns?page=0&page_size=500"))
		assert.EqualValues(t, 1, body["page"], "page < 1 vira 1")
		assert.EqualValues(t, 200, body["page_size"], "page_size é limitado a 200")

		body = covADecode(t, c.get(admin, "/campaigns?page_size=-4"))
		assert.EqualValues(t, 20, body["page_size"], "page_size < 1 vira o padrão 20")
	})

	t.Run("busca por nome da campanha ou do cliente", func(t *testing.T) {
		assert.Equal(t, []string{"Campanha Gama"}, covANomes(t, c.get(admin, "/campaigns?q=gama")), "casa o nome da campanha")
		assert.ElementsMatch(t, []string{"Campanha Alfa", "Campanha Beta"}, covANomes(t, c.get(admin, "/campaigns?q=cliente%20alfa")), "casa o nome do cliente")
		assert.Equal(t, []string{"Campanha Gama"}, covANomes(t, c.get(admin, "/campaigns?q=GAMA")), "ignora maiúsculas")
		assert.Empty(t, covANomes(t, c.get(admin, "/campaigns?q=nada-parecido")))
	})

	t.Run("competência YYYY-MM usa sobreposição de mês", func(t *testing.T) {
		assert.ElementsMatch(t, []string{"Campanha Alfa", "Campanha Gama"}, covANomes(t, c.get(admin, "/campaigns?competence=2026-06")))
		assert.Equal(t, []string{"Campanha Beta"}, covANomes(t, c.get(admin, "/campaigns?competence=2026-07")))
		assert.Empty(t, covANomes(t, c.get(admin, "/campaigns?competence=2026-12")))
	})

	t.Run("id fixa a campanha", func(t *testing.T) {
		assert.Equal(t, []string{"Campanha Beta"}, covANomes(t, c.get(admin, "/campaigns?id="+c.campA2.String())))
		rec := c.get(admin, "/campaigns?id=nao-uuid")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid id")
	})

	t.Run("viewer: a carteira recorta a lista e o id alheio some", func(t *testing.T) {
		vr := c.router(covAClaims("viewer", uuid.New(), c.cliA))
		assert.ElementsMatch(t, []string{"Campanha Alfa", "Campanha Beta"}, covANomes(t, c.get(vr, "/campaigns?page=1")))
		assert.Empty(t, covANomes(t, c.get(vr, "/campaigns?id="+c.campB.String())), "campanha alheia não vaza nem pelo id")
	})

	t.Run("competência malformada vira 400", func(t *testing.T) {
		rec := c.get(admin, "/campaigns?competence=2026-13")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestCovA_Campaigns_Get(t *testing.T) {
	c := covANewCampCen(t)
	admin := c.router(covAClaims("admin", uuid.New()))

	assert.Equal(t, http.StatusNotFound, c.get(admin, "/campaigns/"+uuid.NewString()).Code)

	rec := c.get(admin, "/campaigns/"+c.campA1.String())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := covADecode(t, rec)
	assert.Equal(t, "Campanha Alfa", body["name"])
	assert.Equal(t, c.cliA.String(), body["client_id"])
	assert.Equal(t, "ativa", body["status"])

	t.Run("viewer dono 200, viewer alheio 404 (anti-oracle)", func(t *testing.T) {
		dono := c.router(covAClaims("viewer", uuid.New(), c.cliA))
		assert.Equal(t, http.StatusOK, c.get(dono, "/campaigns/"+c.campA1.String()).Code)
		alheio := c.router(covAClaims("viewer", uuid.New(), c.cliB))
		assert.Equal(t, http.StatusNotFound, c.get(alheio, "/campaigns/"+c.campA1.String()).Code)
	})

	t.Run("falha do banco 500", func(t *testing.T) {
		bad := &CampaignsHandler{Repo: catalog.NewCampaigns(covAClosedPool(t))}
		rt := covARouter(nil, func(r chi.Router) { r.Get("/campaigns/{id}", bad.Get) })
		assert.Equal(t, http.StatusInternalServerError, c.get(rt, "/campaigns/"+c.campA1.String()).Code)
	})
}

func TestCovA_Campaigns_Financials(t *testing.T) {
	c := covANewCampCen(t)
	admin := c.router(covAClaims("admin", uuid.New()))
	ids := func(rec *httptest.ResponseRecorder) []string {
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var linhas []map[string]any
		covADecodeInto(t, rec, &linhas)
		var out []string
		for _, l := range linhas {
			out = append(out, l["campaign_id"].(string))
		}
		return out
	}

	t.Run("recusas de ids", func(t *testing.T) {
		rec := c.get(admin, "/campaigns/financials?ids=nao-uuid")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid ids")

		muitos := make([]string, maxFinancialsIDs+1)
		for i := range muitos {
			muitos[i] = uuid.NewString()
		}
		rec = c.get(admin, "/campaigns/financials?ids="+covAJoin(muitos))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "too many ids")
	})

	t.Run("sem ids devolve todas do escopo", func(t *testing.T) {
		got := ids(c.get(admin, "/campaigns/financials"))
		assert.ElementsMatch(t, []string{c.campA1.String(), c.campA2.String(), c.campB.String()}, got)
	})

	t.Run("ids recorta a página pedida", func(t *testing.T) {
		got := ids(c.get(admin, "/campaigns/financials?ids="+c.campA1.String()+",%20"+c.campB.String()))
		assert.ElementsMatch(t, []string{c.campA1.String(), c.campB.String()}, got)
	})

	t.Run("ids só de vírgulas é 'nenhuma campanha', não 'todas'", func(t *testing.T) {
		assert.Empty(t, ids(c.get(admin, "/campaigns/financials?ids=,%20,")))
	})

	t.Run("viewer: ids é interseção com a carteira, nunca bypass", func(t *testing.T) {
		vr := c.router(covAClaims("viewer", uuid.New(), c.cliA))
		got := ids(c.get(vr, "/campaigns/financials?ids="+c.campA1.String()+","+c.campB.String()))
		assert.Equal(t, []string{c.campA1.String()}, got)
	})

	t.Run("falha do banco 500", func(t *testing.T) {
		bad := &CampaignsHandler{Repo: catalog.NewCampaigns(covAClosedPool(t))}
		rt := covARouter(nil, func(r chi.Router) { r.Get("/campaigns/financials", bad.Financials) })
		assert.Equal(t, http.StatusInternalServerError, c.get(rt, "/campaigns/financials").Code)
	})
}

func TestCovA_Campaigns_RecusasDeEntradaEErroInterno(t *testing.T) {
	c := covANewCampCen(t)
	admin := c.router(covAClaims("admin", uuid.New()))
	id := c.campA1.String()

	t.Run("entrada inválida", func(t *testing.T) {
		casos := []struct {
			nome, metodo, alvo string
			corpo              any
		}{
			{"update id", http.MethodPut, "/campaigns/xx", map[string]any{"name": "x"}},
			{"update json", http.MethodPut, "/campaigns/" + id, "{x"},
			{"cpm id", http.MethodPut, "/campaigns/xx/fixed-cpm", map[string]any{}},
			{"cpm json", http.MethodPut, "/campaigns/" + id + "/fixed-cpm", "{x"},
			{"stations id", http.MethodPut, "/campaigns/xx/stations", map[string]any{}},
			{"stations json", http.MethodPut, "/campaigns/" + id + "/stations", "{x"},
			{"start id", http.MethodPost, "/campaigns/xx/start", nil},
		}
		for _, cs := range casos {
			t.Run(cs.nome, func(t *testing.T) {
				rec := covAServe(admin, covAJSON(t, cs.metodo, cs.alvo, cs.corpo))
				assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			})
		}
	})

	t.Run("falha do banco vira 500 em todas as escritas", func(t *testing.T) {
		bad := &CampaignsHandler{Repo: catalog.NewCampaigns(covAClosedPool(t))} // sem supervisor: a troca de emissoras grava direto no banco
		rt := covARouter(nil, func(r chi.Router) {
			r.Post("/campaigns", bad.Create)
			r.Put("/campaigns/{id}", bad.Update)
			r.Delete("/campaigns/{id}", bad.Delete)
			r.Put("/campaigns/{id}/fixed-cpm", bad.UpdateFixedCPM)
			r.Put("/campaigns/{id}/stations", bad.UpdateStations)
			r.Post("/campaigns/{id}/cancel", bad.Cancel)
		})
		casos := []struct {
			metodo, alvo string
			corpo        any
		}{
			{http.MethodPost, "/campaigns", map[string]any{"name": "Campanha Teste", "client_id": c.cliA.String(), "start_date": "2026-06-01T00:00:00Z", "end_date": "2026-06-30T00:00:00Z"}},
			{http.MethodPut, "/campaigns/" + id, map[string]any{"name": "Novo Nome", "start_date": "2026-06-01T00:00:00Z", "end_date": "2026-06-30T00:00:00Z"}},
			{http.MethodDelete, "/campaigns/" + id, nil},
			{http.MethodPut, "/campaigns/" + id + "/fixed-cpm", map[string]any{"fixed_cpm": 10}},
			{http.MethodPut, "/campaigns/" + id + "/stations", map[string]any{"target_stations": []string{}}},
			{http.MethodPost, "/campaigns/" + id + "/cancel", nil},
		}
		for _, cs := range casos {
			rec := covAServe(rt, covAJSON(t, cs.metodo, cs.alvo, cs.corpo))
			assert.Equal(t, http.StatusInternalServerError, rec.Code, "%s %s: %s", cs.metodo, cs.alvo, rec.Body.String())
		}
	})
}

// ── Vínculo material × campanha ──────────────────────────────────────────────

func TestCovA_CampaignMaterials_LeituraEErros(t *testing.T) {
	c := covANewCampCen(t)
	mcptest.Vincular(t, c.ctx, c.pool, c.campA1, c.material, c.emissora)
	h := &CampaignMaterialsHandler{
		Repo: catalog.NewCampaignMaterials(c.pool), CampaignRepo: catalog.NewCampaigns(c.pool), Supervisor: c.sup,
	}
	rotas := func(claims *auth.Claims, hh *CampaignMaterialsHandler) chi.Router {
		return covARouter(claims, func(r chi.Router) {
			r.Post("/campaigns/{campaignID}/materials", hh.Link)
			r.Get("/campaigns/{campaignID}/materials", hh.ListByCampaign)
			r.Put("/campaigns/{campaignID}/materials/{materialID}/stations", hh.UpdateStations)
			r.Delete("/campaigns/{campaignID}/materials/{materialID}", hh.Unlink)
		})
	}
	admin := rotas(covAClaims("admin", uuid.New()), h)
	base := "/campaigns/" + c.campA1.String() + "/materials"

	t.Run("lista os vínculos da campanha", func(t *testing.T) {
		rec := c.get(admin, base)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var itens []map[string]any
		covADecodeInto(t, rec, &itens)
		require.Len(t, itens, 1)
		assert.Equal(t, c.material.String(), itens[0]["material_id"])
		assert.Equal(t, []any{c.emissora.String()}, itens[0]["target_stations"])
	})

	t.Run("campanha sem vínculos devolve [] e não null", func(t *testing.T) {
		rec := c.get(admin, "/campaigns/"+c.campA2.String()+"/materials")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "[]", covATrim(rec.Body.String()))
	})

	t.Run("id inválido 400", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, c.get(admin, "/campaigns/xx/materials").Code)
	})

	t.Run("viewer dono lê; viewer alheio e campanha inexistente dão 404", func(t *testing.T) {
		dono := rotas(covAClaims("viewer", uuid.New(), c.cliA), h)
		assert.Equal(t, http.StatusOK, c.get(dono, base).Code)
		alheio := rotas(covAClaims("viewer", uuid.New(), c.cliB), h)
		assert.Equal(t, http.StatusNotFound, c.get(alheio, base).Code)
		assert.Equal(t, http.StatusNotFound, c.get(dono, "/campaigns/"+uuid.NewString()+"/materials").Code)
	})

	t.Run("recusas de entrada nas escritas", func(t *testing.T) {
		mid := c.material.String()
		casos := []struct {
			nome, metodo, alvo string
			corpo              any
		}{
			{"link campanha inválida", http.MethodPost, "/campaigns/xx/materials", map[string]any{}},
			{"link json", http.MethodPost, base, "{x"},
			{"stations campanha inválida", http.MethodPut, "/campaigns/xx/materials/" + mid + "/stations", map[string]any{}},
			{"stations material inválido", http.MethodPut, base + "/xx/stations", map[string]any{}},
			{"stations json", http.MethodPut, base + "/" + mid + "/stations", "{x"},
			{"unlink campanha inválida", http.MethodDelete, "/campaigns/xx/materials/" + mid, nil},
			{"unlink material inválido", http.MethodDelete, base + "/xx", nil},
		}
		for _, cs := range casos {
			t.Run(cs.nome, func(t *testing.T) {
				rec := covAServe(admin, covAJSON(t, cs.metodo, cs.alvo, cs.corpo))
				assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			})
		}
	})

	t.Run("troca de emissoras e desvínculo de material inexistente são idempotentes (204)", func(t *testing.T) {
		desconhecido := uuid.NewString()
		rec := covAServe(admin, covAJSON(t, http.MethodPut, base+"/"+desconhecido+"/stations", map[string]any{"target_stations": []string{}}))
		assert.Equal(t, http.StatusNoContent, rec.Code)
		rec = covAServe(admin, covAJSON(t, http.MethodDelete, base+"/"+desconhecido, nil))
		assert.Equal(t, http.StatusNoContent, rec.Code)
	})

	t.Run("falha do banco 500", func(t *testing.T) {
		bad := &CampaignMaterialsHandler{Repo: catalog.NewCampaignMaterials(covAClosedPool(t)), Supervisor: c.sup}
		rt := rotas(nil, bad)
		assert.Equal(t, http.StatusInternalServerError, c.get(rt, base).Code)
		rec := covAServe(rt, covAJSON(t, http.MethodPost, base, map[string]any{"material_id": c.material.String()}))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		rec = covAServe(rt, covAJSON(t, http.MethodPut, base+"/"+c.material.String()+"/stations", map[string]any{}))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		rec = covAServe(rt, covAJSON(t, http.MethodDelete, base+"/"+c.material.String(), nil))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// ── Regras e ajustes de distribuição: leitura e recusas ──────────────────────

func TestCovA_DistributionRules_ListByCampaign(t *testing.T) {
	c := covANewCampCen(t)
	mcptest.Regra(t, c.ctx, c.pool, c.campA1, c.tipo, c.emissora, "2026-06-01", "2026-06-30", 62, "07:00", "19:00", 3)
	h := &DistributionRulesHandler{Repo: catalog.NewDistributionRules(c.pool), CampaignRepo: catalog.NewCampaigns(c.pool)}
	rotas := func(claims *auth.Claims, hh *DistributionRulesHandler) chi.Router {
		return covARouter(claims, func(r chi.Router) {
			r.Get("/campaigns/{campaignID}/distribution-rules", hh.ListByCampaign)
			r.Post("/campaigns/{campaignID}/distribution-rules", hh.Create)
			r.Put("/campaigns/{campaignID}/distribution-rules/{ruleID}", hh.Update)
			r.Delete("/campaigns/{campaignID}/distribution-rules/{ruleID}", hh.Delete)
		})
	}
	admin := rotas(covAClaims("admin", uuid.New()), h)
	base := "/campaigns/" + c.campA1.String() + "/distribution-rules"

	t.Run("lista as regras com o conteúdo gravado", func(t *testing.T) {
		rec := c.get(admin, base)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var regras []map[string]any
		covADecodeInto(t, rec, &regras)
		require.Len(t, regras, 1)
		assert.EqualValues(t, 62, regras[0]["weekday_mask"])
		assert.EqualValues(t, 3, regras[0]["plays_per_day"])
		assert.Equal(t, c.tipo.String(), regras[0]["type_id"])
	})

	t.Run("campanha sem regras devolve []", func(t *testing.T) {
		rec := c.get(admin, "/campaigns/"+c.campA2.String()+"/distribution-rules")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "[]", covATrim(rec.Body.String()))
	})

	t.Run("id inválido 400", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, c.get(admin, "/campaigns/xx/distribution-rules").Code)
	})

	t.Run("viewer dono lê; alheio/inexistente 404", func(t *testing.T) {
		dono := rotas(covAClaims("viewer", uuid.New(), c.cliA), h)
		assert.Equal(t, http.StatusOK, c.get(dono, base).Code)
		alheio := rotas(covAClaims("viewer", uuid.New(), c.cliB), h)
		assert.Equal(t, http.StatusNotFound, c.get(alheio, base).Code)
		assert.Equal(t, http.StatusNotFound, c.get(dono, "/campaigns/"+uuid.NewString()+"/distribution-rules").Code)
	})

	t.Run("recusas de entrada nas escritas", func(t *testing.T) {
		regra := uuid.NewString()
		casos := []struct {
			nome, metodo, alvo string
			corpo              any
		}{
			{"create campanha inválida", http.MethodPost, "/campaigns/xx/distribution-rules", map[string]any{}},
			{"create json", http.MethodPost, base, "{x"},
			{"create data inválida", http.MethodPost, base, map[string]any{"start_date": "01/06/2026", "end_date": "2026-06-30"}},
			{"update campanha inválida", http.MethodPut, "/campaigns/xx/distribution-rules/" + regra, map[string]any{}},
			{"update regra inválida", http.MethodPut, base + "/xx", map[string]any{}},
			{"update json", http.MethodPut, base + "/" + regra, "{x"},
			{"update data inválida", http.MethodPut, base + "/" + regra, map[string]any{"start_date": "2026-06-01", "end_date": "fim"}},
			{"delete campanha inválida", http.MethodDelete, "/campaigns/xx/distribution-rules/" + regra, nil},
			{"delete regra inválida", http.MethodDelete, base + "/xx", nil},
		}
		for _, cs := range casos {
			t.Run(cs.nome, func(t *testing.T) {
				rec := covAServe(admin, covAJSON(t, cs.metodo, cs.alvo, cs.corpo))
				assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			})
		}
	})

	t.Run("falha do banco 500", func(t *testing.T) {
		bad := &DistributionRulesHandler{Repo: catalog.NewDistributionRules(covAClosedPool(t)), EmSegundoPlano: mcptest.Sincrono}
		rt := rotas(nil, bad)
		assert.Equal(t, http.StatusInternalServerError, c.get(rt, base).Code)
		corpo := map[string]any{
			"type_id": c.tipo.String(), "station_ids": []string{c.emissora.String()}, "material_ids": []string{},
			"start_date": "2026-06-01", "end_date": "2026-06-30", "weekday_mask": 127,
			"time_start": "10:00", "time_end": "12:00", "plays_per_day": 1,
		}
		assert.Equal(t, http.StatusInternalServerError, covAServe(rt, covAJSON(t, http.MethodPost, base, corpo)).Code)
		assert.Equal(t, http.StatusInternalServerError, covAServe(rt, covAJSON(t, http.MethodPut, base+"/"+uuid.NewString(), corpo)).Code)
		assert.Equal(t, http.StatusInternalServerError, covAServe(rt, covAJSON(t, http.MethodDelete, base+"/"+uuid.NewString(), nil)).Code)
	})
}

func TestCovA_DistributionOverrides_ListERecusas(t *testing.T) {
	c := covANewCampCen(t)
	mcptest.Ajuste(t, c.ctx, c.pool, c.campA1, c.tipo, c.emissora, "2026-06-10", 5, "08:00", "18:00")
	mcptest.Ajuste(t, c.ctx, c.pool, c.campA1, c.tipo, c.emissora, "2026-06-20", 2, "09:00", "10:00")
	h := &DistributionOverridesHandler{Repo: catalog.NewDistributionOverrides(c.pool), Recat: catalog.NewDistributionRules(c.pool)}
	rotas := func(hh *DistributionOverridesHandler) chi.Router {
		return covARouter(nil, func(r chi.Router) {
			r.Get("/campaigns/{campaignID}/overrides", hh.ListByDateRange)
			r.Put("/campaigns/{campaignID}/overrides", hh.Upsert)
			r.Delete("/campaigns/{campaignID}/overrides", hh.Delete)
		})
	}
	r := rotas(h)
	base := "/campaigns/" + c.campA1.String() + "/overrides"

	t.Run("lista só os ajustes da janela", func(t *testing.T) {
		rec := c.get(r, base+"?from=2026-06-01&to=2026-06-15")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var itens []map[string]any
		covADecodeInto(t, rec, &itens)
		require.Len(t, itens, 1)
		assert.EqualValues(t, 5, itens[0]["plays_expected"])
		assert.Equal(t, "08:00", itens[0]["time_start"])
		assert.Equal(t, "18:00", itens[0]["time_end"])

		rec = c.get(r, base+"?from=2026-06-01&to=2026-06-30")
		covADecodeInto(t, rec, &itens)
		assert.Len(t, itens, 2)
	})

	t.Run("janela sem ajustes devolve []", func(t *testing.T) {
		rec := c.get(r, base+"?from=2026-07-01&to=2026-07-31")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "[]", covATrim(rec.Body.String()))
	})

	t.Run("recusas", func(t *testing.T) {
		casos := []struct {
			nome, metodo, alvo string
			corpo              any
			trecho             string
		}{
			{"list campanha inválida", http.MethodGet, "/campaigns/xx/overrides?from=2026-06-01&to=2026-06-02", nil, "invalid campaignID"},
			{"list from inválido", http.MethodGet, base + "?from=x&to=2026-06-02", nil, "from must be YYYY-MM-DD"},
			{"list to inválido", http.MethodGet, base + "?from=2026-06-01&to=x", nil, "to must be YYYY-MM-DD"},
			{"upsert campanha inválida", http.MethodPut, "/campaigns/xx/overrides", map[string]any{}, "invalid campaignID"},
			{"upsert json", http.MethodPut, base, "{x", "invalid JSON"},
			{"upsert data", http.MethodPut, base, map[string]any{"for_date": "10/06/2026"}, "for_date must be YYYY-MM-DD"},
			{"delete campanha inválida", http.MethodDelete, "/campaigns/xx/overrides", map[string]any{}, "invalid campaignID"},
			{"delete json", http.MethodDelete, base, "{x", "invalid JSON"},
			{"delete data", http.MethodDelete, base, map[string]any{"for_date": ""}, "for_date must be YYYY-MM-DD"},
		}
		for _, cs := range casos {
			t.Run(cs.nome, func(t *testing.T) {
				rec := covAServe(r, covAJSON(t, cs.metodo, cs.alvo, cs.corpo))
				assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				assert.Contains(t, rec.Body.String(), cs.trecho)
			})
		}
	})

	t.Run("sem recategorizador o ajuste é gravado do mesmo jeito", func(t *testing.T) {
		semRecat := rotas(&DistributionOverridesHandler{Repo: catalog.NewDistributionOverrides(c.pool)})
		corpo := map[string]any{
			"type_id": c.tipo.String(), "station_id": c.emissora.String(), "for_date": "2026-06-25",
			"plays_expected": 4, "time_start": "07:00", "time_end": "08:00", "reason": "teste",
		}
		rec := covAServe(semRecat, covAJSON(t, http.MethodPut, base, corpo))
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		var n int16
		require.NoError(t, c.pool.QueryRow(c.ctx,
			`SELECT plays_expected FROM distribution_overrides WHERE campaign_id=$1 AND for_date='2026-06-25'`, c.campA1).Scan(&n))
		assert.EqualValues(t, 4, n)
	})

	t.Run("falha do banco 500", func(t *testing.T) {
		bad := rotas(&DistributionOverridesHandler{Repo: catalog.NewDistributionOverrides(covAClosedPool(t))})
		assert.Equal(t, http.StatusInternalServerError, c.get(bad, base+"?from=2026-06-01&to=2026-06-30").Code)
		corpo := map[string]any{"type_id": c.tipo.String(), "station_id": c.emissora.String(), "for_date": "2026-06-25",
			"plays_expected": 1, "time_start": "07:00", "time_end": "08:00"}
		assert.Equal(t, http.StatusInternalServerError, covAServe(bad, covAJSON(t, http.MethodPut, base, corpo)).Code)
		assert.Equal(t, http.StatusInternalServerError, covAServe(bad, covAJSON(t, http.MethodDelete, base, corpo)).Code)
	})
}
