package handlers

// campaigns_acoes_lock_test.go — TRAVA o que as rotas de cancelar/apagar
// campanha, CPM fixo e preço por emissora fazem hoje (status, corpo, linha no
// banco, chamadas ao supervisor), ANTES de a lógica sair do handler para
// internal/acoes (Fatia 4, Task 10). Passa no código antigo e no novo.

import (
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
)

func roteadorCicloCampanha(h *CampaignsHandler, p *PricingHandler) chi.Router {
	r := chi.NewRouter()
	r.Post("/campaigns/{id}/cancel", h.Cancel)
	r.Delete("/campaigns/{id}", h.Delete)
	r.Put("/campaigns/{id}/fixed-cpm", h.UpdateFixedCPM)
	r.Put("/campaigns/{campaignID}/pricing/{stationID}", p.Upsert)
	return r
}

func chamar(t *testing.T, r http.Handler, metodo, alvo string, corpo any) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	switch v := corpo.(type) {
	case nil:
		rd = strings.NewReader("")
	case string:
		rd = strings.NewReader(v)
	default:
		b, err := json.Marshal(v)
		require.NoError(t, err)
		rd = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(metodo, alvo, rd)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestCancelarHTTP_204MarcaCanceladaEParaOsWorkers(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := criaCampanhaDireto(t, ctx, pool, seedClienteLocal(t, ctx, pool), "")
	sup := &fakeSupervisor{}
	r := roteadorCicloCampanha(&CampaignsHandler{Repo: catalog.NewCampaigns(pool), Supervisor: sup}, nil)

	rec := chamar(t, r, http.MethodPost, "/campaigns/"+c.ID.String()+"/cancel", nil)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Empty(t, rec.Body.String())
	var status string
	var cancelada *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT status, cancelled_at FROM campaigns WHERE id=$1`, c.ID).Scan(&status, &cancelada))
	require.Equal(t, "cancelada", status)
	require.NotNil(t, cancelada)
	require.EqualValues(t, 1, sup.stopCalls.Load())
	require.Equal(t, c.ID, sup.lastCancelArg)
}

func TestCancelarHTTP_SegundaVezDa409ComOEstadoAtual(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := criaCampanhaDireto(t, ctx, pool, seedClienteLocal(t, ctx, pool), "")
	sup := &fakeSupervisor{}
	r := roteadorCicloCampanha(&CampaignsHandler{Repo: catalog.NewCampaigns(pool), Supervisor: sup}, nil)
	alvo := "/campaigns/" + c.ID.String() + "/cancel"
	require.Equal(t, http.StatusNoContent, chamar(t, r, http.MethodPost, alvo, nil).Code)

	rec := chamar(t, r, http.MethodPost, alvo, nil)

	require.Equal(t, http.StatusConflict, rec.Code)
	var corpo map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
	require.Equal(t, map[string]any{"error": "campaign already in terminal state", "current_status": "cancelada"}, corpo)
	require.EqualValues(t, 1, sup.stopCalls.Load(), "a segunda tentativa não para worker de novo")
}

func TestCancelarHTTP_Inexistente404EIdInvalido400(t *testing.T) {
	_, pool := poolHubCode(t)
	r := roteadorCicloCampanha(&CampaignsHandler{Repo: catalog.NewCampaigns(pool)}, nil)

	rec := chamar(t, r, http.MethodPost, "/campaigns/"+uuid.NewString()+"/cancel", nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "not found\n", rec.Body.String())

	rec = chamar(t, r, http.MethodPost, "/campaigns/xyz/cancel", nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid id\n", rec.Body.String())
}

func TestApagarHTTP_204ApagaALinhaEPausaOsWorkers(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := criaCampanhaDireto(t, ctx, pool, seedClienteLocal(t, ctx, pool), "")
	sup := &fakeSupervisor{}
	r := roteadorCicloCampanha(&CampaignsHandler{Repo: catalog.NewCampaigns(pool), Supervisor: sup}, nil)

	rec := chamar(t, r, http.MethodDelete, "/campaigns/"+c.ID.String(), nil)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM campaigns WHERE id=$1`, c.ID).Scan(&n))
	require.Zero(t, n)
	require.EqualValues(t, 1, sup.pauseCalls.Load())
}

func TestApagarHTTP_PausaComErroSegueEApaga(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := criaCampanhaDireto(t, ctx, pool, seedClienteLocal(t, ctx, pool), "")
	sup := &fakeSupervisor{pauseErr: context.DeadlineExceeded}
	r := roteadorCicloCampanha(&CampaignsHandler{Repo: catalog.NewCampaigns(pool), Supervisor: sup}, nil)

	rec := chamar(t, r, http.MethodDelete, "/campaigns/"+c.ID.String(), nil)

	require.Equal(t, http.StatusNoContent, rec.Code)
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM campaigns WHERE id=$1`, c.ID).Scan(&n))
	require.Zero(t, n)
}

// Apagar o que não existe é idempotente: o repositório não acha linha e não erra, a rota responde 204.
func TestApagarHTTP_InexistenteDa204EIdInvalido400(t *testing.T) {
	_, pool := poolHubCode(t)
	r := roteadorCicloCampanha(&CampaignsHandler{Repo: catalog.NewCampaigns(pool)}, nil)

	rec := chamar(t, r, http.MethodDelete, "/campaigns/"+uuid.NewString(), nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	require.Equal(t, http.StatusBadRequest, chamar(t, r, http.MethodDelete, "/campaigns/xyz", nil).Code)
}

// semeiaProjecaoAlheia: a tocada física é DONA da campanha A e carrega a
// projeção fan-out da B — apagar A destruiria o histórico da B (audit C1).
func semeiaProjecaoAlheia(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (a, b *catalog.Campaign) {
	t.Helper()
	cli, err := catalog.NewClients(pool).Create(ctx, catalog.CreateClientInput{Name: "lock-del-" + uuid.NewString()[:6]})
	require.NoError(t, err)
	camp := catalog.NewCampaigns(pool)
	mk := func(nome string) *catalog.Campaign {
		c, err := camp.Create(ctx, catalog.CreateCampaignInput{
			Name: nome, ClientID: cli.ID,
			StartDate: time.Now().AddDate(0, 0, -7), EndDate: time.Now().AddDate(0, 0, 30),
		})
		require.NoError(t, err)
		return c
	}
	a, b = mk("lock-A"), mk("lock-B")
	mat := func(titulo string) *catalog.Material {
		m, err := catalog.NewMaterials(pool).Create(ctx, catalog.CreateMaterialInput{
			ClientID: cli.ID, Title: titulo, DurationSeconds: 30,
			MasterStoragePath: "/tmp/" + titulo, MasterSHA256: titulo + uuid.NewString(),
		})
		require.NoError(t, err)
		return m
	}
	matA, matB := mat("lockA"), mat("lockB")
	est, err := catalog.NewStations(pool).Create(ctx, catalog.CreateStationInput{
		Name: "Lock FM " + uuid.NewString()[:6], Band: "FM", StreamURL: "http://example.com/" + uuid.NewString(),
	})
	require.NoError(t, err)
	det, err := catalog.NewDetections(pool).Create(ctx, catalog.CreateDetectionInput{
		StationID: est.ID, CommercialID: matA.ID, CampaignID: a.ID,
		DetectedAt: time.Now(), MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)
	require.NoError(t, catalog.NewDetectionCampaigns(pool).InsertProjections(ctx, det.ID, det.DetectedAt,
		[]catalog.Projection{{CampaignID: b.ID, CommercialID: matB.ID, Category: "bonus"}}))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM detection_campaigns WHERE campaign_id IN ($1,$2)`, a.ID, b.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id IN ($1,$2)`, a.ID, b.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, matA.ID, matB.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, a.ID, b.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, est.ID)
	})
	return a, b
}

func TestApagarHTTP_ProjecaoDeOutraCampanha409SemPausarNemApagar(t *testing.T) {
	ctx, pool := poolHubCode(t)
	a, _ := semeiaProjecaoAlheia(t, ctx, pool)
	sup := &fakeSupervisor{}
	r := roteadorCicloCampanha(&CampaignsHandler{Repo: catalog.NewCampaigns(pool), Supervisor: sup}, nil)

	rec := chamar(t, r, http.MethodDelete, "/campaigns/"+a.ID.String(), nil)

	require.Equal(t, http.StatusConflict, rec.Code)
	var corpo map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
	require.Equal(t, map[string]any{"error": "campaign_has_foreign_projections", "foreign_projections": float64(1)}, corpo)
	require.Zero(t, sup.pauseCalls.Load(), "recusa ANTES de pausar")
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM campaigns WHERE id=$1`, a.ID).Scan(&n))
	require.Equal(t, 1, n)
}

func TestCPMFixoHTTP_GravaLimpaERecusa(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := criaCampanhaDireto(t, ctx, pool, seedClienteLocal(t, ctx, pool), "")
	r := roteadorCicloCampanha(&CampaignsHandler{Repo: catalog.NewCampaigns(pool)}, nil)
	alvo := "/campaigns/" + c.ID.String() + "/fixed-cpm"

	rec := chamar(t, r, http.MethodPut, alvo, `{"fixed_cpm": 12.5}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out catalog.Campaign
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, c.ID, out.ID)
	require.NotNil(t, out.FixedCPM)
	require.InDelta(t, 12.5, *out.FixedCPM, 0.001)

	rec = chamar(t, r, http.MethodPut, alvo, `{"fixed_cpm": null}`)
	require.Equal(t, http.StatusOK, rec.Code)
	var cpm *float64
	require.NoError(t, pool.QueryRow(ctx, `SELECT fixed_cpm FROM campaigns WHERE id=$1`, c.ID).Scan(&cpm))
	require.Nil(t, cpm)

	rec = chamar(t, r, http.MethodPut, alvo, `{"fixed_cpm": -1}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "fixed_cpm must be >= 0\n", rec.Body.String())

	rec = chamar(t, r, http.MethodPut, alvo, `{`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid request\n", rec.Body.String())

	rec = chamar(t, r, http.MethodPut, "/campaigns/xyz/fixed-cpm", `{}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid id\n", rec.Body.String())

	rec = chamar(t, r, http.MethodPut, "/campaigns/"+uuid.NewString()+"/fixed-cpm", `{"fixed_cpm": 1}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "not found\n", rec.Body.String())
}

func novoTipoMaterial(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO material_types (id, name, color) VALUES ($1, $2, '#3b82f6')`, id, "lock-tipo-"+id.String()[:6])
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM campaign_station_type_pricing WHERE type_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM material_types WHERE id = $1`, id)
	})
	return id
}

func TestPrecoHTTP_Upsert200ConsolidadoEPorInsercao(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := criaCampanhaDireto(t, ctx, pool, seedClienteLocal(t, ctx, pool), "")
	est := novaEmissora(t, ctx, pool, "Preço")
	tipo := novoTipoMaterial(t, ctx, pool)
	r := roteadorCicloCampanha(nil, &PricingHandler{Repo: catalog.NewPricing(pool)})
	alvo := "/campaigns/" + c.ID.String() + "/pricing/" + est.String()

	rec := chamar(t, r, http.MethodPut, alvo, `{"mode":"consolidated","consolidated_value":1500.5}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var sp catalog.StationPricing
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sp))
	require.Equal(t, "consolidated", sp.Mode)
	require.NotNil(t, sp.ConsolidatedValue)
	require.InDelta(t, 1500.5, *sp.ConsolidatedValue, 0.001)
	require.Empty(t, sp.PerType)

	rec = chamar(t, r, http.MethodPut, alvo, map[string]any{
		"mode": "per_insertion", "per_type": []map[string]any{{"type_id": tipo.String(), "unit_value": 20}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	sp = catalog.StationPricing{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sp))
	require.Equal(t, "per_insertion", sp.Mode)
	require.Nil(t, sp.ConsolidatedValue)
	require.Equal(t, []catalog.TypePricing{{TypeID: tipo, UnitValue: 20}}, sp.PerType)
}

func TestPrecoHTTP_Upsert422PorCadaErroDeValidacao(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := criaCampanhaDireto(t, ctx, pool, seedClienteLocal(t, ctx, pool), "")
	est := novaEmissora(t, ctx, pool, "Preço422")
	tipo := novoTipoMaterial(t, ctx, pool)
	r := roteadorCicloCampanha(nil, &PricingHandler{Repo: catalog.NewPricing(pool)})
	alvo := "/campaigns/" + c.ID.String() + "/pricing/" + est.String()
	tp := `[{"type_id":"` + tipo.String() + `","unit_value":5}]`

	for nome, caso := range map[string]struct{ corpo, texto string }{
		"modo inválido":          {`{"mode":"xx"}`, catalog.ErrPricingInvalidMode.Error()},
		"consolidado sem valor":  {`{"mode":"consolidated"}`, catalog.ErrPricingConsolidatedRequiresValue.Error()},
		"consolidado com tipos":  {`{"mode":"consolidated","consolidated_value":1,"per_type":` + tp + `}`, catalog.ErrPricingMixedFields.Error()},
		"consolidado negativo":   {`{"mode":"consolidated","consolidated_value":-1}`, catalog.ErrPricingNegativeValue.Error()},
		"por inserção sem tipos": {`{"mode":"per_insertion"}`, catalog.ErrPricingPerInsertionRequiresTypes.Error()},
		"por inserção com valor": {`{"mode":"per_insertion","consolidated_value":1,"per_type":` + tp + `}`, catalog.ErrPricingMixedFields.Error()},
		"por inserção negativo":  {`{"mode":"per_insertion","per_type":[{"type_id":"` + tipo.String() + `","unit_value":-1}]}`, catalog.ErrPricingNegativeValue.Error()},
	} {
		t.Run(nome, func(t *testing.T) {
			rec := chamar(t, r, http.MethodPut, alvo, caso.corpo)
			require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			require.Equal(t, caso.texto+"\n", rec.Body.String())
		})
	}
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM campaign_station_pricing WHERE campaign_id=$1`, c.ID).Scan(&n))
	require.Zero(t, n, "nenhuma recusa grava")
}

func TestPrecoHTTP_Upsert400ParaIdEJsonInvalidos(t *testing.T) {
	_, pool := poolHubCode(t)
	r := roteadorCicloCampanha(nil, &PricingHandler{Repo: catalog.NewPricing(pool)})
	id := uuid.NewString()

	rec := chamar(t, r, http.MethodPut, "/campaigns/xyz/pricing/"+id, `{}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid campaignID\n", rec.Body.String())
	rec = chamar(t, r, http.MethodPut, "/campaigns/"+id+"/pricing/xyz", `{}`)
	require.Equal(t, "invalid stationID\n", rec.Body.String())
	rec = chamar(t, r, http.MethodPut, "/campaigns/"+id+"/pricing/"+id, `{`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid JSON\n", rec.Body.String())
}

// Referência inexistente (campanha, emissora, tipo) já foi 500 "internal error";
// agora é recusa 4xx com o motivo, e nada é gravado.
func TestPrecoHTTP_Upsert_ReferenciaInexistenteResponde4xx(t *testing.T) {
	ctx, pool := poolHubCode(t)
	c := criaCampanhaDireto(t, ctx, pool, seedClienteLocal(t, ctx, pool), "")
	est := novaEmissora(t, ctx, pool, "PreçoFK")
	r := roteadorCicloCampanha(nil, &PricingHandler{Repo: catalog.NewPricing(pool)})
	porTipo := `{"mode":"per_insertion","per_type":[{"type_id":"` + uuid.NewString() + `","unit_value":5}]}`
	consolidado := `{"mode":"consolidated","consolidated_value":10}`

	rec := chamar(t, r, http.MethodPut, "/campaigns/"+uuid.NewString()+"/pricing/"+est.String(), consolidado)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Equal(t, "not found\n", rec.Body.String())

	rec = chamar(t, r, http.MethodPut, "/campaigns/"+c.ID.String()+"/pricing/"+uuid.NewString(), consolidado)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "emissora inexistente\n", rec.Body.String())

	rec = chamar(t, r, http.MethodPut, "/campaigns/"+c.ID.String()+"/pricing/"+est.String(), porTipo)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "tipo de material inexistente\n", rec.Body.String())

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM campaign_station_pricing WHERE campaign_id=$1`, c.ID).Scan(&n))
	require.Zero(t, n)
}
