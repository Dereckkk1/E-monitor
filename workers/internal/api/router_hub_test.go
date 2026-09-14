package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/auth"
)

type hubChaveFake struct{ boa string }

func (c hubChaveFake) ChaveConfere(a string) bool { return a == c.boa }

type hubResolverFake struct{ local uuid.UUID }

func (r hubResolverFake) LocalPorHubID(_ context.Context, hubClientID string) (uuid.UUID, error) {
	if hubClientID != "hub-ok" {
		return uuid.Nil, auth.ErrHubClientNaoLigado
	}
	return r.local, nil
}

// routerComHub monta o router com as rotas /hub REGISTRADAS.
//
// ⚠️ Os handlers precisam existir de verdade: o chi NÃO roda as middlewares de
// um `Route` quando nenhuma rota interna casa — ele vai direto ao 404. Com
// `Insights`/`Campaigns`/`Detections` nil as rotas não são registradas e todo
// teste de 401/403 passaria a medir o NotFound, não o middleware. Medido em
// 2026-09-02, e é o motivo de este arquivo montar structs vazios em vez de
// deixar as Deps zeradas.
//
// Os repos ficam nil de propósito: em tudo o que este arquivo exercita, ou o
// middleware responde antes do handler, ou o handler valida o parâmetro e
// responde 400 antes de tocar o repo. Nenhum caminho aqui chega ao banco.
func routerComHub(t *testing.T) http.Handler {
	t.Helper()
	return NewRouter(Deps{
		HubClient:  hubChaveFake{boa: "chave-boa"},
		HubClients: hubResolverFake{local: uuid.New()},
		Insights:   &handlers.InsightsHandler{},
		Campaigns:  &handlers.CampaignsHandler{},
		Detections: &handlers.DetectionsHandler{},
		// Os do módulo Checking (spec do hub 2026-09-14 §4). Precisam ser
		// não-nil para as rotas serem REGISTRADAS — com nil elas não existem e
		// todo teste de 401/403 passaria a medir o NotFound, não o middleware.
		CampaignMaterials: &handlers.CampaignMaterialsHandler{},
		DistributionRules: &handlers.DistributionRulesHandler{},
		Pricing:           &handlers.PricingHandler{},
		Materials:         &handlers.MaterialsHandler{},
		ClientTargetPmm:   &handlers.ClientTargetPmmHandler{},
		Stations:          &handlers.StationsHandler{},
		MaterialTypes:     &handlers.MaterialTypesHandler{},
		Reports:           &handlers.ReportsHandler{},
	})
}

func pede(t *testing.T, h http.Handler, caminho, chave, cliente string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, caminho, nil)
	if chave != "" {
		r.Header.Set("X-Hub-Platform-Key", chave)
	}
	if cliente != "" {
		r.Header.Set("X-Hub-Client-Id", cliente)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestRotasHub_ExistemEExigemAChave(t *testing.T) {
	h := routerComHub(t)
	id := uuid.NewString()
	caminhos := []string{
		"/v1/internal/hub/insights?campaigns=" + id + "&from=2026-08-01&to=2026-08-31",
		"/v1/internal/hub/campaigns/" + id,
		"/v1/internal/hub/campaigns/" + id + "/daily-summary?from=2026-08-01&to=2026-08-31",
	}
	for _, c := range caminhos {
		// Sem chave: 401, e NUNCA 404 — 404 aqui diria "esta rota não existe",
		// que é informação para quem sonda.
		require.Equal(t, http.StatusUnauthorized, pede(t, h, c, "", "hub-ok").Code, c)
		// Chave boa, cliente não ligado: 403.
		require.Equal(t, http.StatusForbidden, pede(t, h, c, "chave-boa", "hub-alheio").Code, c)
		// Chave boa, sem header de cliente: 400.
		require.Equal(t, http.StatusBadRequest, pede(t, h, c, "chave-boa", "").Code, c)
	}
}

func TestRotasHub_ODailySummaryUsaOParamCampaignID(t *testing.T) {
	// O handler lê `chi.URLParam(r, "campaignID")`. Se a rota fosse registrada
	// com `{id}` — como a spec §5.3 escreveu —, o param sairia VAZIO e a rota
	// devolveria 400 "invalid campaignID" para todo mundo, para sempre. Aqui
	// um uuid válido tem que PASSAR da validação de param (e morrer depois, no
	// from/to, que é a validação seguinte).
	h := routerComHub(t)
	rec := pede(t, h,
		"/v1/internal/hub/campaigns/"+uuid.NewString()+"/daily-summary",
		"chave-boa", "hub-ok")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "from must be YYYY-MM-DD",
		"chegou na validação de from/to, logo o campaignID foi lido")
	require.NotContains(t, rec.Body.String(), "invalid campaignID")
}

func TestRotasHub_NaoExistemSemConfiguracao(t *testing.T) {
	// Sem HubClient/HubClients nas Deps as rotas não são registradas — mesmo
	// padrão do `if d.HubSync != nil`. Uma instalação sem hub não expõe porta.
	h := NewRouter(Deps{Insights: &handlers.InsightsHandler{}})
	require.Equal(t, http.StatusNotFound,
		pede(t, h, "/v1/internal/hub/insights", "chave-boa", "hub-ok").Code)
}

func TestRotasHub_NaoPassamPeloJWT(t *testing.T) {
	// A prova de que o grupo /hub está FORA do RequireJWTAtivo: com a chave e o
	// cliente certos, a requisição CHEGA no handler — e o 400 que ela ganha é o
	// da validação de parâmetro do próprio handler, não de autenticação.
	// Se este grupo caísse dentro do RequireJWTAtivo, viria 401 sem JWT.
	h := routerComHub(t)
	require.Equal(t, http.StatusUnauthorized,
		pede(t, h, "/v1/internal/hub/campaigns/nao-e-uuid", "", "hub-ok").Code)

	rec := pede(t, h, "/v1/internal/hub/campaigns/nao-e-uuid", "chave-boa", "hub-ok")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid id",
		"passou do middleware do hub e caiu na validação do CampaignsHandler")
}

// caminhosDeLeitura são TODAS as rotas de leitura do hub — as três originais
// (Central consolidada) mais as dez do módulo Checking (spec do hub
// 2026-09-14 §4).
//
// Cada uma aparece com um valor de parâmetro VÁLIDO: o que este teste mede é o
// middleware, e um uuid inválido faria o handler responder 400 por outro
// motivo, escondendo justamente o que se quer provar.
func caminhosDeLeitura(id string) []string {
	return []string{
		// as três que já existiam
		"/v1/internal/hub/insights?campaigns=" + id + "&from=2026-08-01&to=2026-08-31",
		"/v1/internal/hub/campaigns/" + id,
		"/v1/internal/hub/campaigns/" + id + "/daily-summary?from=2026-08-01&to=2026-08-31",
		// as dez do checking
		"/v1/internal/hub/campaigns/" + id + "/materials",
		"/v1/internal/hub/campaigns/" + id + "/distribution-rules",
		"/v1/internal/hub/campaigns/" + id + "/pricing",
		"/v1/internal/hub/clients/" + id + "/materials",
		"/v1/internal/hub/clients/" + id + "/target-pmm",
		"/v1/internal/hub/stations?ids=" + id,
		"/v1/internal/hub/material-types",
		"/v1/internal/hub/detections?campaign_id=" + id,
		"/v1/internal/hub/detections/" + id + "/evidence",
		"/v1/internal/hub/reports/campaigns/" + id + "/consolidated.csv",
	}
}

// TestRotasHub_AsRotasDeLeituraEstaoRegistradas enumera o que o chi REALMENTE
// registrou, e é o único teste deste arquivo que prova EXISTÊNCIA.
//
// ⚠️ Nenhum código de status prova isso. Um caminho inventado sob `/hub/`
// responde **401**, não 404, porque o `RequireHubKeyScoped` é middleware do
// grupo e roda antes do roteamento interno dele. Medido em produção em
// 2026-09-14: `/v1/internal/hub/detections` respondia 401 numa versão que não
// tinha essa rota. Um teste de 401 para uma rota que não existe passa —
// exatamente o falso verde que este aqui fecha.
//
// De quebra, ele prova o NOME do parâmetro: o padrão registrado aparece
// literal, então `{campaignID}` trocado por `{id}` falha aqui em vez de virar
// um 400 "invalid campaignID" eterno em produção.
func TestRotasHub_AsRotasDeLeituraEstaoRegistradas(t *testing.T) {
	h := routerComHub(t)
	r, ok := h.(Router)
	require.True(t, ok, "NewRouter deve devolver api.Router para o mux ser enumerável")

	registradas := map[string]bool{}
	require.NoError(t, chi.Walk(r.Mux, func(metodo, rota string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registradas[metodo+" "+rota] = true
		return nil
	}))

	esperadas := []string{
		// as três da Central consolidada
		"GET /v1/internal/hub/insights",
		"GET /v1/internal/hub/campaigns/{id}",
		"GET /v1/internal/hub/campaigns/{campaignID}/daily-summary",
		// as dez do módulo Checking
		"GET /v1/internal/hub/campaigns/{campaignID}/materials",
		"GET /v1/internal/hub/campaigns/{campaignID}/distribution-rules",
		"GET /v1/internal/hub/campaigns/{campaignID}/pricing",
		"GET /v1/internal/hub/clients/{clientID}/materials",
		"GET /v1/internal/hub/clients/{clientID}/target-pmm",
		"GET /v1/internal/hub/stations",
		"GET /v1/internal/hub/material-types",
		"GET /v1/internal/hub/detections",
		"GET /v1/internal/hub/detections/{id}/evidence",
		"GET /v1/internal/hub/reports/campaigns/{id}/consolidated.csv",
	}
	for _, e := range esperadas {
		require.True(t, registradas[e], "rota não registrada no grupo /hub: %s", e)
	}
}

// TestRotasHub_TodaLeituraExigeChaveEClienteLigado é o teste que impede uma
// rota nova de entrar no grupo sem a guarda.
//
// ⚠️ Ele NÃO prova que a rota existe — ver o teste acima. Prova que ninguém
// sem a chave, e ninguém de outro cliente, atravessa. As duas coisas juntas é
// que fecham a porta.
//
// Nenhum caso aqui chega ao handler: o middleware responde antes. É de
// propósito — os handlers deste arquivo são structs vazios, com repositório
// nil, e MaterialTypes.List vai direto ao repo sem validar nada. Uma
// requisição bem-sucedida aqui seria panic, não asserção.
func TestRotasHub_TodaLeituraExigeChaveEClienteLigado(t *testing.T) {
	h := routerComHub(t)
	id := uuid.NewString()
	for _, c := range caminhosDeLeitura(id) {
		// Sem chave: 401, e NUNCA 404 — 404 aqui diria "esta rota não existe",
		// que é informação para quem sonda.
		require.Equal(t, http.StatusUnauthorized, pede(t, h, c, "", "hub-ok").Code, c)
		// Chave boa, cliente não ligado a nenhum tenant daqui: 403.
		require.Equal(t, http.StatusForbidden, pede(t, h, c, "chave-boa", "hub-alheio").Code, c)
		// Chave boa, sem o header de cliente: 400.
		require.Equal(t, http.StatusBadRequest, pede(t, h, c, "chave-boa", "").Code, c)
	}
}

func TestRotasHub_OInsightsForcaOClienteDoEscopo(t *testing.T) {
	// Com UM cliente no escopo, o /insights NÃO exige `client_id` na query
	// (insights.go: `if len(scopes) == 1 { clientID = scopes[0] }`). O 400 que
	// vem é o de `campaigns required` — a validação SEGUINTE. É isso que
	// garante que o hub não precisa (nem consegue) escolher o cliente pela URL.
	h := routerComHub(t)
	rec := pede(t, h, "/v1/internal/hub/insights", "chave-boa", "hub-ok")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "campaigns required")
	require.NotContains(t, rec.Body.String(), "client_id required")
}
