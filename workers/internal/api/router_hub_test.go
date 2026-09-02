package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
