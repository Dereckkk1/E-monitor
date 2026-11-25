package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// covCInsightsFixture: um cliente, uma campanha de junho/2026, uma emissora com
// PMM 2000 e dois materiais do mesmo tipo (30s). Preço por inserção R$100.
type covCInsightsFixture struct {
	client, camp, typeID, matA, matB, station uuid.UUID
}

func covCInsightsSetup(t *testing.T, label string) (*covCInsightsFixture, *Insights, *covCWorld) {
	t.Helper()
	ctx, pool := newTestDB(t)
	f := &covCInsightsFixture{}
	f.client = insSeedClient(t, ctx, pool, "Cliente Teste Insights "+label)
	f.camp = insSeedCampaign(t, ctx, pool, f.client, "2026-06-01", "2026-06-30")
	f.typeID, f.matA = insSeedTypeAndMaterial(t, ctx, pool, f.client, "Spot A "+label)
	_, f.matB = insSeedTypeAndMaterial(t, ctx, pool, f.client, "Spot B "+label)
	// matB usa o mesmo tipo de matA (a regra é por tipo; o rateio é por material).
	_, err := pool.Exec(ctx, `UPDATE materials SET type_id = $2 WHERE id = $1`, f.matB, f.typeID)
	require.NoError(t, err)
	f.station = insSeedStation(t, ctx, pool, "Rádio Teste FM Insights "+label, 2000, 50, 50, 30, 40, 30, 30, 40, 30)
	insSeedStationPricing(t, ctx, pool, f.camp, f.station, "per_insertion", 0)
	insSeedTypePricing(t, ctx, pool, f.camp, f.station, f.typeID, 100)
	insSeedDistributionRule(t, ctx, pool, f.camp, f.typeID, f.station,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 4)
	w := &covCWorld{ctx: ctx, pool: pool, client: f.client, camp: f.camp, typeID: f.typeID}
	return f, NewInsights(pool), w
}

func covCParams(f *covCInsightsFixture) InsightsParams {
	return InsightsParams{
		ClientID: f.client, CampaignIDs: []uuid.UUID{f.camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"), StationIDs: []uuid.UUID{},
	}
}

func TestCovC_Insights_Compute_RateioPorMaterial(t *testing.T) {
	f, repo, w := covCInsightsSetup(t, "rateio")
	// Dia 15: A tocou 3x, B tocou 1x (todas in_slot, meta 4).
	for i := 0; i < 3; i++ {
		insSeedDetection(t, w.ctx, w.pool, f.camp, f.matA, f.station, "in_slot", "2026-06-15")
	}
	insSeedDetection(t, w.ctx, w.pool, f.camp, f.matB, f.station, "in_slot", "2026-06-15")

	full, err := repo.Compute(w.ctx, covCParams(f))
	require.NoError(t, err)
	assert.False(t, full.MaterialProrated)
	assert.Zero(t, full.MaterialShare)
	assert.EqualValues(t, 8000, full.KPIs.Impactos, "4 tocadas × PMM 2000")
	assert.InDelta(t, 400, full.KPIs.Investido.Executado, 0.01, "4 in_slot × R$100")
	assert.InDelta(t, 50, full.KPIs.CPM, 0.01, "400 ÷ 8000 × 1000")

	p := covCParams(f)
	p.MaterialIDs = []uuid.UUID{f.matA}
	part, err := repo.Compute(w.ctx, p)
	require.NoError(t, err)
	assert.True(t, part.MaterialProrated)
	assert.InDelta(t, 0.75, part.MaterialShare, 0.0001, "3 de 4 tocadas (in_slot + bonus)")
	assert.EqualValues(t, 6000, part.KPIs.Impactos, "impactos são EXATOS por material: 3 × 2000")
	assert.EqualValues(t, 3, part.KPIs.VeiculacoesTotal)
	assert.InDelta(t, 300, part.KPIs.Investido.Executado, 0.01, "R$ em reais é rateado: 400 × 0,75")
	assert.InDelta(t, 50, part.KPIs.CPM, 0.01, "numerador e denominador rateados pelo mesmo critério: CPM não muda")
	// A série temporal programada também é rateada e o déficit recalculado (meta 4 × 0,75 = 3; in_slot do material = 3).
	require.NotEmpty(t, part.Buckets)
	for _, b := range part.Buckets {
		assert.GreaterOrEqual(t, b.Deficit, 0)
		if b.Bucket == "2026-06-15" {
			assert.Equal(t, 3, b.Programado)
			assert.Equal(t, 3, b.InSlot)
			assert.Zero(t, b.Deficit)
		}
	}

	// Material sem nenhuma tocada: participação 0 e nada de R$ rateado vira NaN.
	semTocada := covCSeedMaterial(t, w, "Spot C Sem Tocada", 30)
	p.MaterialIDs = []uuid.UUID{semTocada}
	none, err := repo.Compute(w.ctx, p)
	require.NoError(t, err)
	assert.Zero(t, none.MaterialShare)
	assert.Zero(t, none.KPIs.Impactos)
	assert.Zero(t, none.KPIs.CPM, "sem impactos o CPM é 0 (nunca NaN/Inf)")
	assert.Zero(t, none.KPIs.Investido.Executado)
}

func TestCovC_Insights_MaterialShares_PorCampanha(t *testing.T) {
	f, repo, w := covCInsightsSetup(t, "shares")
	// Sem filtro de material: resultado vazio (e nenhuma query).
	empty, err := repo.materialShares(w.ctx, covCParams(f))
	require.NoError(t, err)
	assert.Zero(t, empty.Global)
	assert.Empty(t, empty.ByCampaign)

	insSeedDetection(t, w.ctx, w.pool, f.camp, f.matA, f.station, "in_slot", "2026-06-10")
	insSeedDetection(t, w.ctx, w.pool, f.camp, f.matA, f.station, "bonus", "2026-06-10")
	insSeedDetection(t, w.ctx, w.pool, f.camp, f.matB, f.station, "in_slot", "2026-06-10")
	// out_slot e out_date não entram na base de impactos (não valem nada).
	insSeedDetection(t, w.ctx, w.pool, f.camp, f.matB, f.station, "out_slot", "2026-06-10")
	insSeedDetection(t, w.ctx, w.pool, f.camp, f.matB, f.station, "out_date", "2026-06-10")

	p := covCParams(f)
	p.MaterialIDs = []uuid.UUID{f.matA}
	got, err := repo.materialShares(w.ctx, p)
	require.NoError(t, err)
	assert.InDelta(t, 2.0/3.0, got.Global, 0.0001, "A: 2 de 3 (in_slot + bonus)")
	assert.InDelta(t, 2.0/3.0, got.ByCampaign[f.camp], 0.0001)

	// Fora do período: nada → share 0 e mapa sem a campanha.
	p.From, p.To = parseDate("2026-07-01"), parseDate("2026-07-31")
	got, err = repo.materialShares(w.ctx, p)
	require.NoError(t, err)
	assert.Zero(t, got.Global)
	assert.Empty(t, got.ByCampaign)

	_, err = repo.materialShares(covCCancelled(), InsightsParams{MaterialIDs: []uuid.UUID{f.matA}})
	require.Error(t, err)
}

func TestCovC_Insights_Compute_CPMFixoEPublicoAlvo(t *testing.T) {
	f, repo, w := covCInsightsSetup(t, "fixo")
	for i := 0; i < 2; i++ {
		insSeedDetection(t, w.ctx, w.pool, f.camp, f.matA, f.station, "in_slot", "2026-06-12")
	}
	// Rótulo de público-alvo do cliente + PMM no target da emissora.
	label := "Mulheres 25-49"
	_, err := NewClients(w.pool).Update(w.ctx, f.client, UpdateClientInput{Name: "Cliente Teste Insights fixo", TargetLabel: &label})
	require.NoError(t, err)
	v := 500
	_, _, err = NewClientStationPMM(w.pool).BulkUpsert(w.ctx, f.client, []TargetPMMEntry{{StationID: f.station, PMMTarget: &v}})
	require.NoError(t, err)
	t.Cleanup(func() { w.pool.Exec(w.ctx, `DELETE FROM client_station_pmm WHERE client_id = $1`, f.client) })

	dyn, err := repo.Compute(w.ctx, covCParams(f))
	require.NoError(t, err)
	require.NotNil(t, dyn.TargetLabel)
	assert.Equal(t, label, *dyn.TargetLabel)
	assert.EqualValues(t, 4000, dyn.KPIs.Impactos)
	assert.EqualValues(t, 1000, dyn.KPIs.ImpactosTarget, "2 tocadas × PMM target 500")
	assert.Equal(t, 1, dyn.KPIs.StationsWithTarget)
	assert.InDelta(t, 50, dyn.KPIs.CPM, 0.01, "200 ÷ 4000 × 1000")
	assert.InDelta(t, 200, dyn.KPIs.CPMTarget, 0.01, "200 ÷ 1000 × 1000")

	// CPM fixo da campanha sobrescreve o CPM geral, mas NÃO o CPM no target (sempre dinâmico).
	_, err = NewCampaigns(w.pool).UpdateFixedCPM(w.ctx, f.camp, covCF(25))
	require.NoError(t, err)
	fixed, err := repo.Compute(w.ctx, covCParams(f))
	require.NoError(t, err)
	assert.InDelta(t, 25, fixed.KPIs.CPM, 0.01)
	assert.InDelta(t, 200, fixed.KPIs.CPMTarget, 0.01)
	assert.InDelta(t, 200, fixed.KPIs.Investido.Executado, 0.01, "Investido segue o pricing real, não o CPM fixo")

	// Com CPM fixo e filtro de material, o peso da média ponderada usa o mesmo recorte.
	p := covCParams(f)
	p.MaterialIDs = []uuid.UUID{f.matB}
	semB, err := repo.Compute(w.ctx, p)
	require.NoError(t, err)
	assert.Zero(t, semB.KPIs.Impactos)
}

func TestCovC_Insights_TargetLabel(t *testing.T) {
	f, repo, w := covCInsightsSetup(t, "label")
	got, err := repo.targetLabel(w.ctx, nil)
	require.NoError(t, err)
	assert.Nil(t, got, "seleção vazia → sem rótulo")

	got, err = repo.targetLabel(w.ctx, []uuid.UUID{f.camp})
	require.NoError(t, err)
	assert.Nil(t, got, "cliente sem rótulo")

	label := "Homens 18-34"
	_, err = NewClients(w.pool).Update(w.ctx, f.client, UpdateClientInput{Name: "Cliente Teste Insights label", TargetLabel: &label})
	require.NoError(t, err)
	got, err = repo.targetLabel(w.ctx, []uuid.UUID{f.camp})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, label, *got)

	// Dois clientes na seleção: rotular seria mentira → nil.
	cli2 := insSeedClient(t, w.ctx, w.pool, "Cliente Teste Insights label 2")
	camp2 := insSeedCampaign(t, w.ctx, w.pool, cli2, "2026-06-01", "2026-06-30")
	got, err = repo.targetLabel(w.ctx, []uuid.UUID{f.camp, camp2})
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestCovC_Insights_ErroDeBancoPropaga(t *testing.T) {
	f, repo, _ := covCInsightsSetup(t, "dberr")
	ctx := covCCancelled()
	p := covCParams(f)
	_, err := repo.Compute(ctx, p)
	require.Error(t, err)
	_, err = repo.targetLabel(ctx, []uuid.UUID{f.camp})
	require.Error(t, err)
	_, err = repo.computeCPM(ctx, p, 1, 1, materialShareResult{})
	require.Error(t, err)
	_, err = repo.fetchCampaigns(ctx, f.client, []uuid.UUID{f.camp})
	require.Error(t, err)
}

func TestCovC_OrMaxDate(t *testing.T) {
	assert.True(t, orMaxDate(parseDate("2026-06-01")).Equal(parseDate("2026-06-01")), "data informada passa direto")
	assert.Greater(t, orMaxDate(parseDate("0001-01-01")).Year(), 2100, "zero value vira data no futuro distante")
}
