package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Os testes deste arquivo descrevem o comportamento ESPERADO onde o código atual
// diverge dele. Cada um está com t.Skip("BUG: ...") — a correção é de quem toca
// o código de produção; ao corrigir, é só remover o Skip.

// AggregateFilter.ClientIDs está documentado como "restringe às detecções cujas
// campanhas pertencem a esses clientes", mas só AggregateByMaterial aplica o
// filtro. AggregateByStation e AggregateByMaterialStation o ignoram em silêncio:
// quem popular ClientIDs achando que escopou a leitura recebe os dados de TODOS.
// Hoje nenhum caller depende disso (os handlers checam a posse da campanha antes),
// mas é uma armadilha de vazamento entre clientes.
func TestCovC_BUG_AggregateFilter_ClientIDsIgnoradoEmAggregateByStationEMaterialStation(t *testing.T) {
	t.Skip("BUG: detections.go:1805 (AggregateByMaterialStation) e :1891 (AggregateByStation) ignoram AggregateFilter.ClientIDs; só AggregateByMaterial (:1609) aplica. Esperado: carteira de outro cliente = zero linhas (falha fechada), como em AggregateByMaterial.")

	w := covCSeedWorld(t, "bug-clientids")
	dets := NewDetections(w.pool)
	covCInsertDet(t, w, w.mat, w.station, covCDay(0), "in_slot")
	alheio := AggregateFilter{CampaignIDs: []uuid.UUID{w.camp}, ClientIDs: []uuid.UUID{uuid.New()}}

	byMat, err := dets.AggregateByMaterial(w.ctx, alheio)
	require.NoError(t, err)
	assert.Zero(t, byMat.TotalDetections, "referência: AggregateByMaterial já respeita a carteira")

	byStation, err := dets.AggregateByStation(w.ctx, alheio)
	require.NoError(t, err)
	assert.Empty(t, byStation, "AggregateByStation deveria respeitar ClientIDs")
	byMS, err := dets.AggregateByMaterialStation(w.ctx, alheio)
	require.NoError(t, err)
	assert.Empty(t, byMS, "AggregateByMaterialStation deveria respeitar ClientIDs")
}

// ListByCampaign anexa o detalhe por tipo a TODA emissora que tenha linha em
// campaign_station_type_pricing, mesmo em modo consolidado; GetByCampaignStation
// e relerLote só carregam o detalhe em per_insertion (a doc de ListByCampaign diz
// "quando aplicável"; o comentário do IterateForExport lembra que não há FK entre
// as duas tabelas e que uma linha órfã não pode virar cobrança). As leituras da
// MESMA emissora divergem quando sobra linha de tipo num preço consolidado.
func TestCovC_BUG_Pricing_ListByCampaignMostraDetalheEmModoConsolidado(t *testing.T) {
	t.Skip("BUG: pricing.go:117 (ListByCampaign) anexa campaign_station_type_pricing a emissora em modo consolidated; GetByCampaignStation (:340-367) e relerLote (:319) só em per_insertion. Esperado: PerType vazio quando Mode=consolidated, em qualquer leitura.")

	w := covCSeedWorld(t, "bug-pricing")
	repo := NewPricing(w.pool)
	_, err := repo.Upsert(w.ctx, UpsertInput{CampaignID: w.camp, StationID: w.station,
		Mode: PricingModeConsolidated, ConsolidatedValue: covCF(1000)})
	require.NoError(t, err)
	// Linha órfã de tipo (o banco não impede: não há FK cruzando as duas tabelas).
	_, err = w.pool.Exec(w.ctx, `
		INSERT INTO campaign_station_type_pricing (campaign_id, station_id, type_id, unit_value)
		VALUES ($1, $2, $3, 99)`, w.camp, w.station, w.typeID)
	require.NoError(t, err)

	one, err := repo.GetByCampaignStation(w.ctx, w.camp, w.station)
	require.NoError(t, err)
	assert.Empty(t, one.PerType, "referência: a leitura pontual esconde o detalhe em consolidated")

	list, err := repo.ListByCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Empty(t, list[0].PerType, "ListByCampaign deveria esconder o detalhe órfão em consolidated")
}

// computeSummary conta a queda ABERTA (sem duration_seconds) no uptime geral
// ("open outage: count elapsed time to now"), mas buildDailySummary a ignora: o
// dia corrente de uma emissora que está fora do ar agora aparece com 100% de
// uptime na barra diária, enquanto o KPI geral já a desconta.
func TestCovC_BUG_HealthEvents_DailySummaryIgnoraQuedaAberta(t *testing.T) {
	t.Skip("BUG: health_events.go:255 (buildDailySummary) só soma down com duration_seconds != nil; queda aberta fica de fora e o dia corrente mostra 100%, ao contrário de computeSummary (:216) que a desconta. Esperado: dia corrente < 100% enquanto a queda segue aberta.")

	sid := uuid.New()
	startedAt := time.Now().UTC().Add(-time.Minute)
	events := []HealthEvent{{StationID: sid, EventType: "down", EventAt: startedAt}} // aberta
	days := buildDailySummary(events, 3)
	require.Len(t, days, 3)
	today := days[len(days)-1]
	assert.Less(t, today.UptimePct, 100.0, "queda aberta deveria baixar o uptime do dia corrente")
}
