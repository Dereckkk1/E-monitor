package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCovC_ParseHHMM(t *testing.T) {
	tests := []struct {
		in       string
		h, m     int
		wantOK   bool
		describe string
	}{
		{"08:30", 8, 30, true, "horário comum"},
		{"00:00", 0, 0, true, "meia-noite"},
		{"23:59", 23, 59, true, "último minuto"},
		{"24:00", 0, 0, false, "hora fora do intervalo"},
		{"12:60", 0, 0, false, "minuto fora do intervalo"},
		{"-1:10", 0, 0, false, "hora negativa"},
		{"abc", 0, 0, false, "lixo"},
		{"", 0, 0, false, "vazio"},
	}
	for _, tc := range tests {
		t.Run(tc.describe, func(t *testing.T) {
			h, m, ok := parseHHMM(tc.in)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.h, h)
				assert.Equal(t, tc.m, m)
			}
		})
	}
}

func TestCovC_CrossesAny(t *testing.T) {
	day := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	rule := TimeWindow{Start: "10:00", End: "12:00"}
	tests := []struct {
		name  string
		rule  TimeWindow
		downs []TimeRange
		want  bool
	}{
		{"sem quedas", rule, nil, false},
		{"queda dentro da faixa", rule, []TimeRange{{From: at(10, 30), Duration: 10 * time.Minute}}, true},
		{"queda que começa antes e entra na faixa", rule, []TimeRange{{From: at(9, 30), Duration: time.Hour}}, true},
		{"queda que cobre a faixa inteira", rule, []TimeRange{{From: at(8, 0), Duration: 8 * time.Hour}}, true},
		{"queda termina exatamente quando a faixa começa (encosta, não cruza)", rule, []TimeRange{{From: at(9, 0), Duration: time.Hour}}, false},
		{"queda começa exatamente quando a faixa termina (encosta, não cruza)", rule, []TimeRange{{From: at(12, 0), Duration: time.Hour}}, false},
		{"queda totalmente fora", rule, []TimeRange{{From: at(15, 0), Duration: time.Hour}}, false},
		{"uma das quedas cruza", rule, []TimeRange{{From: at(15, 0), Duration: time.Hour}, {From: at(11, 0), Duration: time.Minute}}, true},
		{"faixa com início inválido nunca cruza", TimeWindow{Start: "xx", End: "12:00"}, []TimeRange{{From: at(10, 30), Duration: time.Hour}}, false},
		{"faixa com fim inválido nunca cruza", TimeWindow{Start: "10:00", End: "25:00"}, []TimeRange{{From: at(10, 30), Duration: time.Hour}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, crossesAny(day, tc.rule, tc.downs))
		})
	}
}

func TestCovC_SortStationsByDeficit(t *testing.T) {
	mk := func(name string, deficit int) CampaignFailureStation {
		return CampaignFailureStation{Station: StationFailureInfo{Name: name}, Deficit: deficit}
	}
	in := []CampaignFailureStation{mk("Zeta", 1), mk("Beta", 5), mk("Alfa", 5), mk("Gama", 3), mk("Alfa2", 0)}
	sortStationsByDeficit(in)
	var names []string
	for _, s := range in {
		names = append(names, s.Station.Name)
	}
	// déficit DESC; empate por nome ASC.
	assert.Equal(t, []string{"Alfa", "Beta", "Gama", "Zeta", "Alfa2"}, names)

	// Entradas triviais não quebram.
	sortStationsByDeficit(nil)
	one := []CampaignFailureStation{mk("Solo", 2)}
	sortStationsByDeficit(one)
	assert.Equal(t, "Solo", one[0].Station.Name)
}

func TestCovC_SortCampaignsByImpact(t *testing.T) {
	mk := func(client string, nStations int) CampaignDailyFailure {
		return CampaignDailyFailure{
			Campaign: CampaignInfo{ClientName: client},
			Stations: make([]CampaignFailureStation, nStations),
		}
	}
	in := []CampaignDailyFailure{mk("Cliente Z", 1), mk("Cliente B", 3), mk("Cliente A", 3), mk("Cliente M", 2)}
	sortCampaignsByImpact(in)
	var got []string
	for _, c := range in {
		got = append(got, c.Campaign.ClientName)
	}
	// nº de emissoras afetadas DESC; empate por cliente ASC.
	assert.Equal(t, []string{"Cliente A", "Cliente B", "Cliente M", "Cliente Z"}, got)
	sortCampaignsByImpact(nil)
}

func TestCovC_IsBonified(t *testing.T) {
	tests := []struct {
		deficit, extras int
		want            bool
	}{
		{0, 5, false},  // nada falhou: nada a compensar
		{3, 0, false},  // sem extras não há bonificação
		{3, 2, false},  // extras não cobrem o déficit
		{3, 3, true},   // cobre exatamente
		{3, 10, true},  // sobra
		{-1, 10, false}, // déficit negativo não existe, mas não pode virar true
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, IsBonified(tc.deficit, tc.extras), "deficit=%d extras=%d", tc.deficit, tc.extras)
	}
}

func TestCovC_CampaignFailures_GetNaoEncontradoECancelada(t *testing.T) {
	w := covCSeedWorld(t, "cf-get")
	repo := NewCampaignFailures(w.pool)

	_, err := repo.Get(w.ctx, uuid.New())
	require.ErrorIs(t, err, ErrCampaignNotFound)

	// Campanha sem nenhuma falha: existe, devolve o cabeçalho e lista vazia.
	got, err := repo.Get(w.ctx, w.camp)
	require.NoError(t, err)
	assert.Equal(t, w.camp, got.Campaign.ID)
	assert.Equal(t, "Cliente Teste cf-get", got.Campaign.ClientName)
	assert.Equal(t, "2026-06-01", got.Campaign.StartDate)
	assert.Equal(t, "2026-06-30", got.Campaign.EndDate)
	assert.NotNil(t, got.Stations)
	assert.Empty(t, got.Stations)
	assert.Zero(t, got.Summary.TotalDeficit)

	// Cancelada some do drill-in (política: fora de telas operacionais).
	_, _, err = NewCampaigns(w.pool).CancelCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	_, err = repo.Get(w.ctx, w.camp)
	require.ErrorIs(t, err, ErrCampaignNotFound)
}

func TestCovC_CampaignFailures_ListHistorical_Paginacao(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewCampaignFailures(pool)

	// page/pageSize inválidos são normalizados (1 / 50); pageSize > 200 é limitado a 200.
	res, err := repo.ListHistorical(ctx, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Page)
	assert.Equal(t, 50, res.PageSize)
	assert.Equal(t, "historical", res.Mode)
	assert.NotNil(t, res.Campaigns)
	big, err := repo.ListHistorical(ctx, 2, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, big.PageSize)
	assert.Equal(t, 2, big.Page)
}

func TestCovC_FailuresRepos_ErroDeBancoPropaga(t *testing.T) {
	w := covCSeedWorld(t, "fail-dberr")
	ctx := covCCancelled()
	day := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)

	_, err := NewCampaignFailures(w.pool).ListForDate(ctx, day)
	require.Error(t, err)
	_, err = NewCampaignFailures(w.pool).ListHistorical(ctx, 1, 10)
	require.Error(t, err)
	_, err = NewCampaignFailures(w.pool).Get(ctx, w.camp)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrCampaignNotFound, "erro de banco não vira 404")
	_, err = NewStationFailures(w.pool).ListForDate(ctx, day, 0)
	require.Error(t, err)
	_, err = NewFailuresDaily(w.pool).ListDaily(ctx, day, day.AddDate(0, 0, 3), 0)
	require.Error(t, err)
}

func TestCovC_FailuresDaily_SerieDensaSemFalha(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewFailuresDaily(pool)

	// Janela no passado distante (antes de qualquer fixture) → série densa, tudo zerado.
	from := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC)
	res, err := repo.ListDaily(ctx, from, to, 0)
	require.NoError(t, err)
	assert.Equal(t, "2026-01-05", res.From)
	assert.Equal(t, "2026-01-11", res.To)
	require.Len(t, res.Days, 7, "um ponto por dia, mesmo sem falha")
	for i, d := range res.Days {
		assert.Equal(t, from.AddDate(0, 0, i).Format("2006-01-02"), d.Date)
	}
	assert.Equal(t, 7, res.Summary.DaysTotal)
}
