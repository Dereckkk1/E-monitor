package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// covCGeocode põe coordenada na emissora (o mapa só plota emissora geocodada).
func covCGeocode(t *testing.T, w *covCWorld, station uuid.UUID, lat, lng float64, state string) {
	t.Helper()
	_, err := w.pool.Exec(w.ctx,
		`UPDATE stations SET latitude=$2, longitude=$3, state=$4, city='Cidade Teste' WHERE id=$1`,
		station, lat, lng, state)
	require.NoError(t, err)
}

func covCTarget(t *testing.T, w *covCWorld, stations ...uuid.UUID) {
	t.Helper()
	_, err := w.pool.Exec(w.ctx, `UPDATE campaigns SET target_stations = $2 WHERE id = $1`, w.camp, stations)
	require.NoError(t, err)
}

func TestCovC_LiveMap_Get_EscopoEAntiOracle(t *testing.T) {
	w := covCSeedWorld(t, "live-scope")
	lm := NewLiveMap(w.pool)
	stNoGeo := covCSeedStation(t, w, "Rádio Teste FM Sem Coordenada")
	covCGeocode(t, w, w.station, -23.5, -46.6, "SP")
	covCTarget(t, w, w.station, stNoGeo)

	// Lista vazia de campanhas → não encontrado (sem vazar nada).
	_, err := lm.Get(w.ctx, nil, nil, LiveMapOpts{})
	require.ErrorIs(t, err, ErrCampaignNotFound)

	// Admin (scopes nil): vê a campanha; só a emissora geocodada é plotada.
	res, err := lm.Get(w.ctx, []uuid.UUID{w.camp, w.camp}, nil, LiveMapOpts{}) // id duplicado é deduplicado
	require.NoError(t, err)
	require.Len(t, res.Stations, 1)
	assert.Equal(t, w.station, res.Stations[0].ID)
	assert.InDelta(t, -23.5, res.Stations[0].Latitude, 0.0001)
	assert.Nil(t, res.Stations[0].AnatelCoverageKm, "sem cruzamento Anatel = indeterminado, nunca zero")

	// Viewer com a carteira certa vê; carteira de outro cliente ou vazia não (404 anti-oracle).
	_, err = lm.Get(w.ctx, []uuid.UUID{w.camp}, []uuid.UUID{w.client}, LiveMapOpts{})
	require.NoError(t, err)
	_, err = lm.Get(w.ctx, []uuid.UUID{w.camp}, []uuid.UUID{uuid.New()}, LiveMapOpts{})
	require.ErrorIs(t, err, ErrCampaignNotFound)
	_, err = lm.Get(w.ctx, []uuid.UUID{w.camp}, []uuid.UUID{}, LiveMapOpts{})
	require.ErrorIs(t, err, ErrCampaignNotFound, "carteira vazia falha fechada")

	// Uma campanha inexistente no pedido derruba tudo (tudo-ou-nada).
	_, err = lm.Get(w.ctx, []uuid.UUID{w.camp, uuid.New()}, nil, LiveMapOpts{})
	require.ErrorIs(t, err, ErrCampaignNotFound)

	// Cancelada: 404 na tela ao vivo, mas o pós-venda (IncludeTerminal) fotografa.
	_, _, err = NewCampaigns(w.pool).CancelCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	_, err = lm.Get(w.ctx, []uuid.UUID{w.camp}, nil, LiveMapOpts{})
	require.ErrorIs(t, err, ErrCampaignNotFound)
	hist, err := lm.Get(w.ctx, []uuid.UUID{w.camp}, nil, LiveMapOpts{IncludeTerminal: true})
	require.NoError(t, err)
	assert.Len(t, hist.Stations, 1)
}

func TestCovC_LiveMap_Get_FeedEUniaoDeCampanhas(t *testing.T) {
	w := covCSeedWorld(t, "live-feed")
	lm := NewLiveMap(w.pool)
	covCGeocode(t, w, w.station, -22.9, -43.2, "RJ")
	covCTarget(t, w, w.station)

	// Segunda campanha do mesmo cliente, que reaproveita a MESMA emissora.
	camp2, err := NewCampaigns(w.pool).Create(w.ctx, CreateCampaignInput{
		Name: "Campanha Dois Live", ClientID: w.client,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC), TargetStations: []uuid.UUID{w.station},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		w.pool.Exec(w.ctx, `DELETE FROM detection_campaigns WHERE campaign_id = $1`, camp2.ID)
		w.pool.Exec(w.ctx, `DELETE FROM campaigns WHERE id = $1`, camp2.ID)
	})

	now := time.Now()
	recent := covCInsertDet(t, w, w.mat, w.station, now.Add(-time.Hour), "in_slot")
	covCInsertDet(t, w, w.mat, w.station, now.Add(-48*time.Hour), "in_slot") // fora da janela de 24h
	retr := covCInsertDet(t, w, w.mat, w.station, now.Add(-30*time.Minute), "in_slot")
	_, err = w.pool.Exec(w.ctx, `UPDATE detections SET retracted_at = now() WHERE id = $1`, retr)
	require.NoError(t, err)

	// Uma campanha: o feed não repete o nome da campanha na linha.
	one, err := lm.Get(w.ctx, []uuid.UUID{w.camp}, nil, LiveMapOpts{})
	require.NoError(t, err)
	require.Len(t, one.RecentDetections, 1, "só a de <24h, não retratada")
	assert.Equal(t, recent, one.RecentDetections[0].ID)
	assert.Nil(t, one.RecentDetections[0].CampaignName)
	require.NotNil(t, one.Stations[0].LastDetectionAt)

	// Várias campanhas: a emissora compartilhada sai UMA vez e a linha identifica a campanha.
	both, err := lm.Get(w.ctx, []uuid.UUID{w.camp, camp2.ID}, nil, LiveMapOpts{})
	require.NoError(t, err)
	assert.Len(t, both.Stations, 1)
	require.Len(t, both.RecentDetections, 1)
	require.NotNil(t, both.RecentDetections[0].CampaignName)
	assert.Equal(t, "Campanha live-feed", *both.RecentDetections[0].CampaignName)
	require.NotNil(t, both.RecentDetections[0].CampaignID)
	assert.Equal(t, w.camp, *both.RecentDetections[0].CampaignID)
}

func TestCovC_LiveMap_Coverage(t *testing.T) {
	w := covCSeedWorld(t, "live-cov")
	lm := NewLiveMap(w.pool)
	covCTarget(t, w, w.station)

	// Sem cidades cadastradas: resultado vazio (e totais zerados).
	empty, err := lm.Coverage(w.ctx, []uuid.UUID{w.camp}, nil, LiveMapOpts{})
	require.NoError(t, err)
	assert.Empty(t, empty.Cities)
	assert.Equal(t, CoverageTotals{}, empty.Totals())

	for _, c := range []struct {
		ibge int
		name string
		dist float64
		home bool
	}{{3550308, "Cidade Teste A", 12.5, true}, {3509502, "Cidade Teste B", 3.0, false}, {3548708, "Cidade Teste C", 3.0, false}} {
		_, err := w.pool.Exec(w.ctx, `
			INSERT INTO station_coverage_cities (station_id, ibge_code, city, state, distance_km, is_home, latitude, longitude)
			VALUES ($1, $2, $3, 'SP', $4, $5, -23.0, -46.0)`, w.station, c.ibge, c.name, c.dist, c.home)
		require.NoError(t, err)
	}
	t.Cleanup(func() { w.pool.Exec(w.ctx, `DELETE FROM station_coverage_cities WHERE station_id = $1`, w.station) })

	res, err := lm.Coverage(w.ctx, []uuid.UUID{w.camp}, []uuid.UUID{w.client}, LiveMapOpts{})
	require.NoError(t, err)
	require.Len(t, res.Cities, 3)
	// Ordena por distância e desempata por ibge_code.
	assert.Equal(t, 3509502, res.Cities[0].IBGECode)
	assert.Equal(t, 3548708, res.Cities[1].IBGECode)
	assert.Equal(t, 3550308, res.Cities[2].IBGECode)
	assert.True(t, res.Cities[2].IsHome)
	assert.Equal(t, CoverageTotals{DistinctCities: 3, StationCityPairs: 3}, res.Totals())

	// Mesmo portão anti-oracle do Get.
	_, err = lm.Coverage(w.ctx, nil, nil, LiveMapOpts{})
	require.ErrorIs(t, err, ErrCampaignNotFound)
	_, err = lm.Coverage(w.ctx, []uuid.UUID{w.camp}, []uuid.UUID{uuid.New()}, LiveMapOpts{})
	require.ErrorIs(t, err, ErrCampaignNotFound)
	_, err = lm.Coverage(covCCancelled(), []uuid.UUID{w.camp}, nil, LiveMapOpts{})
	require.Error(t, err)
}

func TestCovC_LiveMap_ErroDeBancoPropaga(t *testing.T) {
	w := covCSeedWorld(t, "live-dberr")
	lm := NewLiveMap(w.pool)
	ctx := covCCancelled()
	_, err := lm.Get(ctx, []uuid.UUID{w.camp}, nil, LiveMapOpts{})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrCampaignNotFound, "erro de banco não é confundido com 404")
	_, err = lm.queryStations(ctx, []uuid.UUID{w.camp})
	require.Error(t, err)
	_, err = lm.queryRecentDetections(ctx, []uuid.UUID{w.camp}, true)
	require.Error(t, err)
}

func TestCovC_DedupUUIDs(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	assert.Nil(t, dedupUUIDs(nil))
	assert.Equal(t, []uuid.UUID{a, b}, dedupUUIDs([]uuid.UUID{a, b, a, b, a}), "preserva a ordem da 1a ocorrência")
}

// ─── Visão Gerencial ────────────────────────────────────────────────────────

func TestCovC_ManagementOverview_Get(t *testing.T) {
	w := covCSeedWorld(t, "mgmt")
	mo := NewManagementOverview(w.pool)
	stNoGeo := covCSeedStation(t, w, "Rádio Teste FM Mgmt Sem Geo")
	covCGeocode(t, w, w.station, -23.5, -46.6, "SP")
	_, err := w.pool.Exec(w.ctx, `UPDATE stations SET state='MG' WHERE id=$1`, stNoGeo)
	require.NoError(t, err)
	covCTarget(t, w, w.station, stNoGeo)
	require.NoError(t, NewCampaignMaterials(w.pool).Link(w.ctx, w.camp, w.mat, []uuid.UUID{w.station}))

	// Uma tocada hoje e outra em 2026-06-10 (dentro da vigência).
	hoje := time.Now().Add(-time.Hour)
	covCInsertDet(t, w, w.mat, w.station, covCDay(0), "in_slot")
	covCInsertDet(t, w, w.mat, w.station, hoje, "bonus")
	// Tocada ignorada não conta.
	ign := covCInsertDet(t, w, w.mat, w.station, covCDay(1), "in_slot")
	user := covCUser(t, w)
	_, err = w.pool.Exec(w.ctx, `UPDATE detections SET ignored_at=now(), ignored_by=$2 WHERE id=$1`, ign, user)
	require.NoError(t, err)

	p := ManagementParams{
		ClientID: &w.client,
		From:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		To:       time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	}
	res, err := mo.Get(w.ctx, p)
	require.NoError(t, err)
	assert.Equal(t, 2, res.KPIs.StationsMonitored)
	assert.ElementsMatch(t, []uuid.UUID{w.station, stNoGeo}, res.MonitoredStationIDs)
	assert.Equal(t, 1, res.KPIs.CampaignsCount)
	assert.Equal(t, 2, res.KPIs.StatesCount, "SP e MG")
	assert.Equal(t, 1, res.KPIs.MaterialsMonitored)
	assert.EqualValues(t, 1, res.KPIs.AiringsTotal, "só a de 10/06 está no período; ignorada fora")
	assert.Zero(t, res.KPIs.StationsLive, "o handler preenche cruzando com os workers")
	require.Len(t, res.Stations, 1, "só emissora geocodada vai pro mapa")
	assert.Equal(t, w.station, res.Stations[0].ID)

	// Período que inclui hoje: a tocada de hoje conta no total e em airings_today.
	p2 := p
	p2.From = time.Now().AddDate(0, 0, -400)
	p2.To = time.Now().AddDate(0, 0, 1)
	// A vigência da campanha (jun/2026) precisa cruzar o período — cruza (From < fim da campanha).
	res2, err := mo.Get(w.ctx, p2)
	require.NoError(t, err)
	assert.EqualValues(t, 2, res2.KPIs.AiringsTotal)

	// Feed: as mais recentes primeiro, sem a ignorada.
	require.GreaterOrEqual(t, len(res2.RecentDetections), 2)
	assert.True(t, res2.RecentDetections[0].DetectedAt.After(res2.RecentDetections[1].DetectedAt))

	// Filtro por status: a campanha é 'programada'; pedir 'ativa' esvazia o escopo.
	p3 := p
	st := "ativa"
	p3.Status = st
	res3, err := mo.Get(w.ctx, p3)
	require.NoError(t, err)
	assert.Zero(t, res3.KPIs.CampaignsCount)
	assert.Empty(t, res3.MonitoredStationIDs)
	assert.Empty(t, res3.RecentDetections)

	// Cliente diferente → nada.
	other := uuid.New()
	p4 := p
	p4.ClientID = &other
	res4, err := mo.Get(w.ctx, p4)
	require.NoError(t, err)
	assert.Zero(t, res4.KPIs.CampaignsCount)

	// Por ids de campanha.
	p5 := p
	p5.CampaignIDs = []uuid.UUID{w.camp}
	res5, err := mo.Get(w.ctx, p5)
	require.NoError(t, err)
	assert.Equal(t, 1, res5.KPIs.CampaignsCount)
}

func TestCovC_ManagementOverview_CanceladaFicaForaDoEscopoPadrao(t *testing.T) {
	w := covCSeedWorld(t, "mgmt-cancel")
	mo := NewManagementOverview(w.pool)
	covCGeocode(t, w, w.station, -23.5, -46.6, "SP")
	covCTarget(t, w, w.station)
	_, _, err := NewCampaigns(w.pool).CancelCampaign(w.ctx, w.camp)
	require.NoError(t, err)

	p := ManagementParams{ClientID: &w.client,
		From: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)}
	res, err := mo.Get(w.ctx, p)
	require.NoError(t, err)
	assert.Zero(t, res.KPIs.CampaignsCount, "painel operacional esconde cancelada por padrão")

	// Pedida explicitamente, aparece.
	p.Status = "cancelada"
	res, err = mo.Get(w.ctx, p)
	require.NoError(t, err)
	assert.Equal(t, 1, res.KPIs.CampaignsCount)
	assert.Equal(t, 1, res.KPIs.StationsMonitored)
}

func TestCovC_ManagementOverview_ErroDeBancoPropaga(t *testing.T) {
	w := covCSeedWorld(t, "mgmt-dberr")
	mo := NewManagementOverview(w.pool)
	_, err := mo.Get(covCCancelled(), ManagementParams{})
	require.Error(t, err)
	_, err = mo.queryKPIs(covCCancelled(), ManagementParams{})
	require.Error(t, err)
	_, err = mo.queryStations(covCCancelled(), ManagementParams{})
	require.Error(t, err)
	_, err = mo.queryRecentDetections(covCCancelled(), ManagementParams{})
	require.Error(t, err)
}
