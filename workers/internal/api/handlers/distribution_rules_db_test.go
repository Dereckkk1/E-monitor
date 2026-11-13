package handlers

// distribution_rules_db_test.go — TRAVA o comportamento das rotas de regra e de
// override de distribuição (status, corpo, linha no banco e categoria
// recalculada) ANTES de a lógica sair do handler para `internal/acoes` (plano da
// Fatia 3 do MCP, Task 7). Estes testes rodam no código antigo e no novo: o que
// eles afirmam é o que a rota faz hoje.
//
// ⚠️ A recategorização de REGRA é uma goroutine (best-effort, 30 s) — o teste
// espera por ela com Eventually. A de OVERRIDE é SÍNCRONA: a categoria já tem de
// estar certa quando o 204 volta, e é isso que `TestOverride_*` afirma sem
// esperar.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// cenarioRegra: uma campanha de junho/2026 com UMA emissora, um material do tipo
// A e UMA tocada às 10:30 (SP) de quarta, 10/06, gravada como bonus.
type cenarioRegra struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	cliente  uuid.UUID
	emissora uuid.UUID
	tipoA    uuid.UUID
	tipoB    uuid.UUID
	material uuid.UUID
	campanha uuid.UUID
	tocada   uuid.UUID
	quando   time.Time
}

func novoCenarioRegra(t *testing.T) *cenarioRegra {
	t.Helper()
	ctx, pool := mcptest.DBCatalogo(t)
	c := &cenarioRegra{ctx: ctx, pool: pool}
	c.cliente = mcptest.Cliente(t, ctx, pool, "Cliente Regra")
	c.emissora = mcptest.Emissora(t, ctx, pool, "Rádio Regra", 1000)
	c.tipoA = mcptest.TipoMaterial(t, ctx, pool, "Spot A")
	c.tipoB = mcptest.TipoMaterial(t, ctx, pool, "Spot B")
	c.material = mcptest.Material(t, ctx, pool, c.cliente, c.tipoA, "Spot Regra")
	c.campanha = mcptest.Campanha(t, ctx, pool, c.cliente, "Campanha Regra", "2026-06-01", "2026-06-30", c.emissora)
	mcptest.Vincular(t, ctx, pool, c.campanha, c.material, c.emissora)
	c.quando = mcptest.Quando("2026-06-10", "10:30")
	c.tocada = mcptest.Veiculacao(t, ctx, pool, c.campanha, c.material, c.emissora, "bonus", c.quando)
	return c
}

// categoria lê a categoria da projeção canônica e a da tocada-base (têm de
// andar juntas: a base é espelhada quando a projeção é a canônica).
func (c *cenarioRegra) categoria(t *testing.T) (projecao, base string) {
	t.Helper()
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT dc.category, d.category
		   FROM detection_campaigns dc
		   JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
		  WHERE dc.detection_id = $1`, c.tocada).Scan(&projecao, &base))
	return projecao, base
}

func (c *cenarioRegra) esperaCategoria(t *testing.T, esperada string) {
	t.Helper()
	require.Eventuallyf(t, func() bool {
		p, b := c.categoria(t)
		return p == esperada && b == esperada
	}, 10*time.Second, 50*time.Millisecond, "a categoria não virou %q", esperada)
}

func (c *cenarioRegra) contaRegras(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT count(*) FROM distribution_rules WHERE campaign_id = $1`, c.campanha).Scan(&n))
	return n
}

func (c *cenarioRegra) corpoRegra(tipo uuid.UUID, ini, fim string, porDia int) map[string]any {
	return map[string]any{
		"type_id": tipo.String(), "station_ids": []string{c.emissora.String()},
		"material_ids": []string{}, "name": "Regra de teste",
		"start_date": "2026-06-01", "end_date": "2026-06-30", "weekday_mask": 127,
		"time_start": ini, "time_end": fim, "plays_per_day": porDia,
	}
}

func roteadorRegras(h *DistributionRulesHandler) chi.Router {
	r := chi.NewRouter()
	r.Post("/campaigns/{campaignID}/distribution-rules", h.Create)
	r.Put("/campaigns/{campaignID}/distribution-rules/{ruleID}", h.Update)
	r.Delete("/campaigns/{campaignID}/distribution-rules/{ruleID}", h.Delete)
	return r
}

func chamaRegra(t *testing.T, r http.Handler, metodo, alvo string, corpo any) *httptest.ResponseRecorder {
	t.Helper()
	var body *bytes.Reader
	if corpo == nil {
		body = bytes.NewReader(nil)
	} else {
		b, err := json.Marshal(corpo)
		require.NoError(t, err)
		body = bytes.NewReader(b)
	}
	req := httptest.NewRequest(metodo, alvo, body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func (c *cenarioRegra) handlerRegras() *DistributionRulesHandler {
	return &DistributionRulesHandler{Repo: catalog.NewDistributionRules(c.pool), CampaignRepo: catalog.NewCampaigns(c.pool)}
}

func TestRegraHTTP_Create_201GravaALinhaERecategoriza(t *testing.T) {
	c := novoCenarioRegra(t)
	r := roteadorRegras(c.handlerRegras())

	rec := chamaRegra(t, r, http.MethodPost, "/campaigns/"+c.campanha.String()+"/distribution-rules",
		c.corpoRegra(c.tipoA, "08:00", "12:00", 1))

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out catalog.DistributionRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.NotEqual(t, uuid.Nil, out.ID)
	require.Equal(t, c.campanha, out.CampaignID)
	require.Equal(t, c.tipoA, out.TypeID)
	require.Equal(t, []uuid.UUID{c.emissora}, out.StationIDs)
	require.Equal(t, "Regra de teste", out.Name)
	require.Equal(t, "08:00", out.TimeStart)
	require.Equal(t, "12:00", out.TimeEnd)
	require.EqualValues(t, 1, out.PlaysPerDay)
	require.EqualValues(t, 127, out.WeekdayMask)
	require.Equal(t, 1, c.contaRegras(t))

	// A recategorização é assíncrona: a tocada das 10:30 cabe na faixa e vira in_slot.
	c.esperaCategoria(t, "in_slot")
}

func TestRegraHTTP_Create_NomeApenasEspacoViraVazio(t *testing.T) {
	c := novoCenarioRegra(t)
	r := roteadorRegras(c.handlerRegras())
	corpo := c.corpoRegra(c.tipoA, "08:00", "12:00", 1)
	corpo["name"] = "   conjunto   "

	rec := chamaRegra(t, r, http.MethodPost, "/campaigns/"+c.campanha.String()+"/distribution-rules", corpo)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out catalog.DistributionRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "conjunto", out.Name, "o nome é aparado")
}

func TestRegraHTTP_Create_DataInvalidaDa400ComOTextoDeSempre(t *testing.T) {
	c := novoCenarioRegra(t)
	r := roteadorRegras(c.handlerRegras())
	corpo := c.corpoRegra(c.tipoA, "08:00", "12:00", 1)
	corpo["end_date"] = "30/06/2026"

	rec := chamaRegra(t, r, http.MethodPost, "/campaigns/"+c.campanha.String()+"/distribution-rules", corpo)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid dates: ")
	require.Zero(t, c.contaRegras(t))
}

func TestRegraHTTP_Update_204SubstituiTudoERecategoriza(t *testing.T) {
	c := novoCenarioRegra(t)
	regra := mcptest.Regra(t, c.ctx, c.pool, c.campanha, c.tipoA, c.emissora, "2026-06-01", "2026-06-30", 127, "08:00", "12:00", 1)
	// Com a regra 08–12 a tocada das 10:30 é in_slot.
	h := c.handlerRegras()
	require.NoError(t, h.Repo.RecategorizeForRule(c.ctx, regra))
	c.esperaCategoria(t, "in_slot")
	r := roteadorRegras(h)

	corpo := c.corpoRegra(c.tipoA, "14:00", "18:00", 3)
	corpo["name"] = "Tarde"
	rec := chamaRegra(t, r, http.MethodPut, "/campaigns/"+c.campanha.String()+"/distribution-rules/"+regra.String(), corpo)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Empty(t, rec.Body.String())
	atual, err := h.Repo.Get(c.ctx, regra)
	require.NoError(t, err)
	require.Equal(t, "14:00", atual.TimeStart)
	require.Equal(t, "18:00", atual.TimeEnd)
	require.EqualValues(t, 3, atual.PlaysPerDay)
	require.Equal(t, "Tarde", atual.Name)
	// A tocada das 10:30 saiu da faixa: cota em aberto (in_slot < N) → out_slot.
	c.esperaCategoria(t, "out_slot")
}

func TestRegraHTTP_Delete_204ApagaARegraERecategorizaACampanha(t *testing.T) {
	c := novoCenarioRegra(t)
	regra := mcptest.Regra(t, c.ctx, c.pool, c.campanha, c.tipoA, c.emissora, "2026-06-01", "2026-06-30", 127, "08:00", "12:00", 1)
	h := c.handlerRegras()
	require.NoError(t, h.Repo.RecategorizeForRule(c.ctx, regra))
	c.esperaCategoria(t, "in_slot")
	r := roteadorRegras(h)

	rec := chamaRegra(t, r, http.MethodDelete, "/campaigns/"+c.campanha.String()+"/distribution-rules/"+regra.String(), nil)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Zero(t, c.contaRegras(t))
	// Sem regra nenhuma não há meta: a tocada passa a ser bonificação.
	c.esperaCategoria(t, "bonus")
}

func TestRegraHTTP_IdsInvalidosDao400(t *testing.T) {
	c := novoCenarioRegra(t)
	r := roteadorRegras(c.handlerRegras())
	corpo := c.corpoRegra(c.tipoA, "08:00", "12:00", 1)

	for _, caso := range []struct{ metodo, alvo, texto string }{
		{http.MethodPut, "/campaigns/" + c.campanha.String() + "/distribution-rules/xx", "invalid ruleID"},
		{http.MethodDelete, "/campaigns/" + c.campanha.String() + "/distribution-rules/xx", "invalid ruleID"},
		{http.MethodPut, "/campaigns/xx/distribution-rules/" + uuid.NewString(), "invalid campaignID"},
	} {
		rec := chamaRegra(t, r, caso.metodo, caso.alvo, corpo)
		require.Equal(t, http.StatusBadRequest, rec.Code, caso.alvo)
		require.Contains(t, rec.Body.String(), caso.texto)
	}
}

// ─────────────────────────────── overrides ───────────────────────────────

func (c *cenarioRegra) handlerAjustes() *DistributionOverridesHandler {
	return &DistributionOverridesHandler{
		Repo:  catalog.NewDistributionOverrides(c.pool),
		Recat: catalog.NewDistributionRules(c.pool),
	}
}

func roteadorAjustes(h *DistributionOverridesHandler) chi.Router {
	r := chi.NewRouter()
	r.Put("/campaigns/{campaignID}/distribution-overrides", h.Upsert)
	r.Delete("/campaigns/{campaignID}/distribution-overrides", h.Delete)
	return r
}

func (c *cenarioRegra) corpoAjuste(esperadas int, ini, fim string) map[string]any {
	return map[string]any{
		"type_id": c.tipoA.String(), "station_id": c.emissora.String(), "for_date": "2026-06-10",
		"plays_expected": esperadas, "time_start": ini, "time_end": fim,
	}
}

type linhaAjuste struct {
	esperadas  int
	ini, fim   string
	motivo     *string
	criadoPor  *uuid.UUID
	existe     bool
	quantidade int
}

func (c *cenarioRegra) ajuste(t *testing.T) linhaAjuste {
	t.Helper()
	var l linhaAjuste
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT count(*) FROM distribution_overrides
		  WHERE campaign_id=$1 AND type_id=$2 AND station_id=$3 AND for_date='2026-06-10'`,
		c.campanha, c.tipoA, c.emissora).Scan(&l.quantidade))
	l.existe = l.quantidade > 0
	if !l.existe {
		return l
	}
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT plays_expected, to_char(time_start,'HH24:MI'), to_char(time_end,'HH24:MI'), reason, created_by
		   FROM distribution_overrides
		  WHERE campaign_id=$1 AND type_id=$2 AND station_id=$3 AND for_date='2026-06-10'`,
		c.campanha, c.tipoA, c.emissora).Scan(&l.esperadas, &l.ini, &l.fim, &l.motivo, &l.criadoPor))
	return l
}

func TestOverrideHTTP_Upsert_204GravaESincronizaACategoriaAntesDeResponder(t *testing.T) {
	c := novoCenarioRegra(t)
	regra := mcptest.Regra(t, c.ctx, c.pool, c.campanha, c.tipoA, c.emissora, "2026-06-01", "2026-06-30", 127, "08:00", "12:00", 1)
	require.NoError(t, catalog.NewDistributionRules(c.pool).RecategorizeForRule(c.ctx, regra))
	c.esperaCategoria(t, "in_slot")
	r := roteadorAjustes(c.handlerAjustes())

	corpo := c.corpoAjuste(0, "00:00", "23:59")
	corpo["reason"] = "feriado: sem comercial"
	rec := chamaRegra(t, r, http.MethodPut, "/campaigns/"+c.campanha.String()+"/distribution-overrides", corpo)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	l := c.ajuste(t)
	require.True(t, l.existe)
	require.Equal(t, 0, l.esperadas)
	require.Equal(t, "00:00", l.ini)
	require.Equal(t, "23:59", l.fim)
	require.NotNil(t, l.motivo)
	require.Equal(t, "feriado: sem comercial", *l.motivo)
	require.Nil(t, l.criadoPor, "a rota nunca preenche created_by")
	// SÍNCRONO: sem Eventually. Meta 0 → toda tocada é bonificação.
	p, b := c.categoria(t)
	require.Equal(t, "bonus", p)
	require.Equal(t, "bonus", b)
}

func TestOverrideHTTP_Upsert_SobrescreveOExistente(t *testing.T) {
	c := novoCenarioRegra(t)
	r := roteadorAjustes(c.handlerAjustes())
	alvo := "/campaigns/" + c.campanha.String() + "/distribution-overrides"

	require.Equal(t, http.StatusNoContent, chamaRegra(t, r, http.MethodPut, alvo, c.corpoAjuste(2, "08:00", "12:00")).Code)
	require.Equal(t, http.StatusNoContent, chamaRegra(t, r, http.MethodPut, alvo, c.corpoAjuste(5, "09:00", "11:00")).Code)

	l := c.ajuste(t)
	require.Equal(t, 1, l.quantidade, "a chave é (campanha, tipo, emissora, dia)")
	require.Equal(t, 5, l.esperadas)
	require.Equal(t, "09:00", l.ini)
	// A tocada das 10:30 cabe na faixa 09–11 do override: in_slot.
	p, _ := c.categoria(t)
	require.Equal(t, "in_slot", p)
}

func TestOverrideHTTP_Delete_204ApagaAChaveEReverteACelulaParaARegra(t *testing.T) {
	c := novoCenarioRegra(t)
	mcptest.Regra(t, c.ctx, c.pool, c.campanha, c.tipoA, c.emissora, "2026-06-01", "2026-06-30", 127, "08:00", "12:00", 1)
	r := roteadorAjustes(c.handlerAjustes())
	alvo := "/campaigns/" + c.campanha.String() + "/distribution-overrides"
	require.Equal(t, http.StatusNoContent, chamaRegra(t, r, http.MethodPut, alvo, c.corpoAjuste(0, "00:00", "23:59")).Code)
	p, _ := c.categoria(t)
	require.Equal(t, "bonus", p)

	rec := chamaRegra(t, r, http.MethodDelete, alvo, c.corpoAjuste(0, "00:00", "23:59"))

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.False(t, c.ajuste(t).existe)
	// SÍNCRONO: a célula volta a ser julgada pela regra 08–12 (N=1) → in_slot.
	p, b := c.categoria(t)
	require.Equal(t, "in_slot", p)
	require.Equal(t, "in_slot", b)
}

func TestOverrideHTTP_Delete_DeChaveInexistenteTambemDa204(t *testing.T) {
	c := novoCenarioRegra(t)
	r := roteadorAjustes(c.handlerAjustes())

	rec := chamaRegra(t, r, http.MethodDelete, "/campaigns/"+c.campanha.String()+"/distribution-overrides",
		c.corpoAjuste(0, "00:00", "23:59"))

	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestOverrideHTTP_Upsert_Recusas400ComOTextoDeSempre(t *testing.T) {
	c := novoCenarioRegra(t)
	r := roteadorAjustes(c.handlerAjustes())
	alvo := "/campaigns/" + c.campanha.String() + "/distribution-overrides"

	for nome, caso := range map[string]struct {
		corpo map[string]any
		texto string
	}{
		"inicio fora de HH:MM": {c.corpoAjuste(1, "8:00", "12:00"), "time_start must be HH:MM (00:00-23:59)"},
		"fim fora de HH:MM":    {c.corpoAjuste(1, "08:00", "24:00"), "time_end must be HH:MM (00:00-23:59)"},
		"fim igual ao inicio":  {c.corpoAjuste(1, "10:00", "10:00"), "time_end must be greater than time_start"},
		"fim antes do inicio":  {c.corpoAjuste(1, "12:00", "08:00"), "time_end must be greater than time_start"},
	} {
		t.Run(nome, func(t *testing.T) {
			rec := chamaRegra(t, r, http.MethodPut, alvo, caso.corpo)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, strings.TrimSpace(rec.Body.String()), caso.texto)
		})
	}
	require.False(t, c.ajuste(t).existe, "recusado não grava")

	corpo := c.corpoAjuste(1, "08:00", "12:00")
	corpo["for_date"] = "10/06/2026"
	rec := chamaRegra(t, r, http.MethodPut, alvo, corpo)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "for_date must be YYYY-MM-DD")
}

// DELETE de uma regra que não existe é idempotente: 204 (a rota sempre foi
// assim). A F3-8a só tornou 404 o DELETE de regra de OUTRA campanha.
func TestRegraHTTP_Delete_RegraInexistenteSegueDa204(t *testing.T) {
	c := novoCenarioRegra(t)
	mcptest.Regra(t, c.ctx, c.pool, c.campanha, c.tipoA, c.emissora, "2026-06-01", "2026-06-30", 127, "08:00", "12:00", 1)
	r := roteadorRegras(c.handlerRegras())

	rec := chamaRegra(t, r, http.MethodDelete,
		"/campaigns/"+c.campanha.String()+"/distribution-rules/"+uuid.NewString(), nil)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Empty(t, rec.Body.String())
	require.Equal(t, 1, c.contaRegras(t), "a regra existente da campanha não foi tocada")
}
