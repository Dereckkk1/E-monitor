package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func covCStationIDs(out ListOutput) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(out.Data))
	for _, s := range out.Data {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestCovC_Stations_ListFiltros(t *testing.T) {
	w := covCSeedWorld(t, "st-list")
	repo := NewStations(w.pool)
	tok := "zzcovclist" + uuid.NewString()[:8]
	mk := func(name, band, city, state string, freq float64) uuid.UUID {
		st, err := repo.Create(w.ctx, CreateStationInput{
			Name: name + " " + tok, Band: band, City: &city, State: &state,
			FrequencyMHz: &freq, StreamURL: "http://example.com/covc/" + uuid.NewString(),
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			w.pool.Exec(w.ctx, `DELETE FROM station_thresholds WHERE station_id = $1`, st.ID)
			w.pool.Exec(w.ctx, `DELETE FROM stations WHERE id = $1`, st.ID)
		})
		return st.ID
	}
	a := mk("Alfa", "FM", "Campinas", "SP", 98.1)
	b := mk("Beta", "AM", "Niterói", "RJ", 710)
	c := mk("Gama", "FM", "Campinas", "RJ", 101.3)

	all, err := repo.List(w.ctx, ListInput{Q: tok, Limit: 50})
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{a, b, c}, covCStationIDs(all))
	assert.EqualValues(t, 3, all.Total)
	assert.Equal(t, 1, all.Pages)
	assert.Equal(t, 1, all.Page)

	byBand, err := repo.List(w.ctx, ListInput{Q: tok, Band: "AM"})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{b}, covCStationIDs(byBand))

	byState, err := repo.List(w.ctx, ListInput{Q: tok, State: "SP"})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{a}, covCStationIDs(byState))

	// Cidade exata, sem acento (accent-insensitive).
	byCity, err := repo.List(w.ctx, ListInput{Q: tok, City: "niteroi"})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{b}, covCStationIDs(byCity))

	byID, err := repo.List(w.ctx, ListInput{StationID: &c})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{c}, covCStationIDs(byID))

	// Paginação: limit 2 → 2 páginas.
	p1, err := repo.List(w.ctx, ListInput{Q: tok, Limit: 2, Page: 1})
	require.NoError(t, err)
	assert.Len(t, p1.Data, 2)
	assert.Equal(t, 2, p1.Pages)
	p2, err := repo.List(w.ctx, ListInput{Q: tok, Limit: 2, Page: 2})
	require.NoError(t, err)
	assert.Len(t, p2.Data, 1)

	// IDs ignora a paginação: devolve o conjunto fechado inteiro.
	closed, err := repo.List(w.ctx, ListInput{IDs: []uuid.UUID{a, b, c}, Limit: 1, Page: 7})
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{a, b, c}, covCStationIDs(closed))
	assert.Equal(t, 1, closed.Page)

	// Nenhum resultado: lista vazia (não-nil) e 1 página.
	none, err := repo.List(w.ctx, ListInput{Q: tok + "inexistente"})
	require.NoError(t, err)
	assert.NotNil(t, none.Data)
	assert.Empty(t, none.Data)
	assert.Equal(t, 1, none.Pages)
}

func TestCovC_Stations_ListContratadasPorCliente(t *testing.T) {
	w := covCSeedWorld(t, "st-contract")
	repo := NewStations(w.pool)
	camps := NewCampaigns(w.pool)
	st2 := covCSeedStation(t, w, "Rádio Teste FM Contratada Dois")

	// Campanha do mundo (programada por padrão) cobre w.station + st2.
	_, err := w.pool.Exec(w.ctx, `UPDATE campaigns SET target_stations = $2 WHERE id = $1`,
		w.camp, []uuid.UUID{w.station, st2})
	require.NoError(t, err)
	// Segunda campanha ATIVA do mesmo cliente cobre só st2.
	ativa, err := camps.Create(w.ctx, CreateCampaignInput{
		Name: "Campanha Ativa Contrato", ClientID: w.client,
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2027, 12, 31, 0, 0, 0, 0, time.UTC), TargetStations: []uuid.UUID{st2},
	})
	require.NoError(t, err)
	t.Cleanup(func() { w.pool.Exec(w.ctx, `DELETE FROM campaigns WHERE id = $1`, ativa.ID) })
	require.NoError(t, camps.UpdateStatus(w.ctx, ativa.ID, "ativa"))
	require.NoError(t, camps.UpdateStatus(w.ctx, w.camp, "programada"))

	out, err := repo.List(w.ctx, ListInput{ContractedBy: []uuid.UUID{w.client}, Limit: 50})
	require.NoError(t, err)
	require.Len(t, out.Data, 2, "só as emissoras contratadas pelo cliente")
	byID := map[uuid.UUID]Station{}
	for _, s := range out.Data {
		byID[s.ID] = s
	}
	// st2: 2 campanhas, uma ativa → no ar e sem "a partir de".
	c2 := byID[st2].Contract
	require.NotNil(t, c2)
	assert.Equal(t, 2, c2.Campaigns)
	assert.True(t, c2.OnAir)
	assert.Nil(t, c2.StartsAt)
	// w.station: só a programada → não está no ar e traz a data de início.
	c1 := byID[w.station].Contract
	require.NotNil(t, c1)
	assert.Equal(t, 1, c1.Campaigns)
	assert.False(t, c1.OnAir)
	require.NotNil(t, c1.StartsAt)
	assert.Equal(t, "2026-06-01", c1.StartsAt.UTC().Format("2006-01-02"))

	// Campanha cancelada não conta como contrato.
	_, _, err = camps.CancelCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	out, err = repo.List(w.ctx, ListInput{ContractedBy: []uuid.UUID{w.client}, Limit: 50})
	require.NoError(t, err)
	require.Len(t, out.Data, 1)
	assert.Equal(t, st2, out.Data[0].ID)

	// Cliente sem campanhas: lista vazia.
	vazio, err := repo.List(w.ctx, ListInput{ContractedBy: []uuid.UUID{uuid.New()}})
	require.NoError(t, err)
	assert.Empty(t, vazio.Data)
}

func TestCovC_Stations_UpdateEGetNaoEncontrado(t *testing.T) {
	w := covCSeedWorld(t, "st-update")
	repo := NewStations(w.pool)

	_, err := repo.Get(w.ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = repo.Update(w.ctx, uuid.New(), UpdateStationInput{Name: "x", Band: "FM", StreamURL: "http://example.com/x"})
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = repo.UpdateStreamURL(w.ctx, uuid.New(), "http://example.com/y")
	require.ErrorIs(t, err, pgx.ErrNoRows)

	city, uf := "São Paulo", "SP"
	updated, err := repo.Update(w.ctx, w.station, UpdateStationInput{
		Name: "Rádio Teste FM Atualizada", Band: "AM", FrequencyMHz: covCF(620),
		City: &city, State: &uf, StreamURL: "http://example.com/covc/novo",
		PMM: covCF(4321),
		Meta: &StationMeta{
			Categories:      []string{"jovem"},
			AudienceProfile: &AudienceProfile{Gender: &Gender{Male: 60, Female: 40}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "Rádio Teste FM Atualizada", updated.Name)
	assert.Equal(t, "AM", updated.Band)
	assert.InDelta(t, 4321, *updated.PMM, 0.001)
	require.NotNil(t, updated.Meta)
	assert.Equal(t, []string{"jovem"}, updated.Meta.Categories)
	assert.InDelta(t, 60, updated.Meta.AudienceProfile.Gender.Male, 0.001)
	require.NotNil(t, updated.Latitude, "cidade conhecida → coordenada derivada")

	// Update sem Meta grava '{}' e não derruba a coordenada (COALESCE).
	again, err := repo.Update(w.ctx, w.station, UpdateStationInput{
		Name: "Rádio Teste FM Atualizada", Band: "AM", StreamURL: "http://example.com/covc/novo",
	})
	require.NoError(t, err)
	assert.Nil(t, again.PMM)
	assert.Equal(t, updated.Latitude, again.Latitude, "lookup sem cidade não apaga a coordenada")

	// UpdateStreamURL mexe SÓ na URL: banda e nome sobrevivem.
	only, err := repo.UpdateStreamURL(w.ctx, w.station, "http://example.com/covc/so-url")
	require.NoError(t, err)
	assert.Equal(t, "http://example.com/covc/so-url", only.StreamURL)
	assert.Equal(t, "AM", only.Band)
	assert.Equal(t, "Rádio Teste FM Atualizada", only.Name)
}

func TestCovC_Stations_StatusESincronizacao(t *testing.T) {
	w := covCSeedWorld(t, "st-status")
	repo := NewStations(w.pool)
	camps := NewCampaigns(w.pool)

	got, err := repo.Get(w.ctx, w.station)
	require.NoError(t, err)
	assert.Equal(t, "paused", got.MonitoringStatus, "emissora nova nasce pausada")

	inList := func(list []Station, id uuid.UUID) bool {
		for _, s := range list {
			if s.ID == id {
				return true
			}
		}
		return false
	}
	active, err := repo.ListActive(w.ctx)
	require.NoError(t, err)
	assert.False(t, inList(active, w.station))

	require.NoError(t, repo.UpdateMonitoringStatus(w.ctx, w.station, "active"))
	active, err = repo.ListActive(w.ctx)
	require.NoError(t, err)
	assert.True(t, inList(active, w.station))

	// 'active' sem campanha ativa cobrindo é pausada pelo reconciler.
	paused, _, err := repo.SyncMonitoringStatus(w.ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, paused, int64(1))
	got, err = repo.Get(w.ctx, w.station)
	require.NoError(t, err)
	assert.Equal(t, "paused", got.MonitoringStatus)

	// Campanha ativa cobrindo a emissora a reativa; 'calibrating' não é tocada.
	_, err = w.pool.Exec(w.ctx, `UPDATE campaigns SET target_stations = $2 WHERE id = $1`, w.camp, []uuid.UUID{w.station})
	require.NoError(t, err)
	require.NoError(t, camps.UpdateStatus(w.ctx, w.camp, "ativa"))
	_, activated, err := repo.SyncMonitoringStatus(w.ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, activated, int64(1))
	got, err = repo.Get(w.ctx, w.station)
	require.NoError(t, err)
	assert.Equal(t, "active", got.MonitoringStatus)

	require.NoError(t, repo.UpdateMonitoringStatus(w.ctx, w.station, "calibrating"))
	_, _, err = repo.SyncMonitoringStatus(w.ctx)
	require.NoError(t, err)
	got, err = repo.Get(w.ctx, w.station)
	require.NoError(t, err)
	assert.Equal(t, "calibrating", got.MonitoringStatus, "estado manual não é tocado")

	// Health check carimba last_health_check.
	assert.Nil(t, got.LastHealthCheck)
	require.NoError(t, repo.UpdateHealthCheck(w.ctx, w.station))
	got, err = repo.Get(w.ctx, w.station)
	require.NoError(t, err)
	require.NotNil(t, got.LastHealthCheck)
	assert.WithinDuration(t, time.Now(), *got.LastHealthCheck, time.Minute)
}

func TestCovC_Stations_ThresholdECalibracao(t *testing.T) {
	w := covCSeedWorld(t, "st-calib")
	repo := NewStations(w.pool)

	// Emissora nova tem linha semeada: calibração ligada, min_hashes padrão 5.
	n, err := repo.GetThreshold(w.ctx, w.station)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	cs, err := repo.GetCalibrationStatus(w.ctx, w.station)
	require.NoError(t, err)
	assert.True(t, cs.CalibrationMode)
	assert.Equal(t, 5, cs.MinHashes)
	assert.Equal(t, 0, cs.DaysElapsed)

	// Valores calibrados + início há 3 dias.
	_, err = w.pool.Exec(w.ctx, `
		UPDATE station_thresholds
		   SET min_hashes = 9, calibration_mode = false,
		       calibration_started_at = now() - interval '3 days 2 hours'
		 WHERE station_id = $1`, w.station)
	require.NoError(t, err)
	n, err = repo.GetThreshold(w.ctx, w.station)
	require.NoError(t, err)
	assert.Equal(t, 9, n)
	cs, err = repo.GetCalibrationStatus(w.ctx, w.station)
	require.NoError(t, err)
	assert.False(t, cs.CalibrationMode)
	assert.Equal(t, 9, cs.MinHashes)
	assert.Equal(t, 3, cs.DaysElapsed)

	// Sem linha de threshold: padrões seguros (default 5 / ainda calibrando).
	_, err = w.pool.Exec(w.ctx, `DELETE FROM station_thresholds WHERE station_id = $1`, w.station)
	require.NoError(t, err)
	n, err = repo.GetThreshold(w.ctx, w.station)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	cs, err = repo.GetCalibrationStatus(w.ctx, w.station)
	require.NoError(t, err)
	assert.Equal(t, CalibrationStatus{CalibrationMode: true, MinHashes: 5, DaysElapsed: 0}, cs)
}

func TestCovC_Stations_ErroDeBancoPropaga(t *testing.T) {
	w := covCSeedWorld(t, "st-dberr")
	repo := NewStations(w.pool)
	ctx := covCCancelled()
	id := w.station

	_, err := repo.List(ctx, ListInput{})
	require.Error(t, err)
	_, err = repo.Get(ctx, id)
	require.Error(t, err)
	_, err = repo.Create(ctx, CreateStationInput{Name: "x", Band: "FM", StreamURL: "http://example.com/x"})
	require.Error(t, err)
	_, err = repo.Update(ctx, id, UpdateStationInput{Name: "x", Band: "FM", StreamURL: "http://example.com/x"})
	require.Error(t, err)
	_, err = repo.UpdateStreamURL(ctx, id, "http://example.com/x")
	require.Error(t, err)
	require.Error(t, repo.UpdateMonitoringStatus(ctx, id, "active"))
	require.Error(t, repo.UpdateHealthCheck(ctx, id))
	_, _, err = repo.SyncMonitoringStatus(ctx)
	require.Error(t, err)
	_, err = repo.AllForWorkers(ctx)
	require.Error(t, err)
	_, err = repo.ListAll(ctx)
	require.Error(t, err)
	_, err = repo.ListActive(ctx)
	require.Error(t, err)
	_, err = repo.GetThreshold(ctx, id)
	require.Error(t, err)
	_, err = repo.GetCalibrationStatus(ctx, id)
	require.Error(t, err)
	_, _, err = repo.UpdateCadastro(ctx, id, UpdateCadastroInput{Name: strPtr("x")})
	require.Error(t, err)
	_, _, err = repo.UpdateCadastro(ctx, id, UpdateCadastroInput{City: strPtr("x")})
	require.Error(t, err)
}

func TestCovC_Stations_AllForWorkersEListAll(t *testing.T) {
	w := covCSeedWorld(t, "st-all")
	repo := NewStations(w.pool)
	contem := func(list []Station) bool {
		for _, s := range list {
			if s.ID == w.station {
				return true
			}
		}
		return false
	}
	all, err := repo.AllForWorkers(w.ctx)
	require.NoError(t, err)
	assert.True(t, contem(all))
	legacy, err := repo.ListAll(w.ctx)
	require.NoError(t, err)
	assert.True(t, contem(legacy))
}
