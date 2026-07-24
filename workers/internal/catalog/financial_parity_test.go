package catalog

import (
	"testing"

	"github.com/google/uuid"
)

// As duas telas partem do MESMO fin_base — este teste falha se alguém quebrar o
// compartilhamento (ex.: reintroduzir out_slot no investido de um lado só).
// Cenário misto: 1 emissora per_insertion + 1 consolidada, mesma campanha,
// mesmo cliente, mesma janela.
func TestFinancialParity_CampaignsVsInsights(t *testing.T) {
	ctx, pool := newTestDB(t)
	client := insSeedClient(t, ctx, pool, "Paridade")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot Par")

	stPI := insSeedStation(t, ctx, pool, "PI", 1000, 60, 40, 20, 50, 30, 30, 50, 20)
	stCons := insSeedStation(t, ctx, pool, "Cons", 2000, 60, 40, 20, 50, 30, 30, 50, 20)
	insSeedStationPricing(t, ctx, pool, camp, stPI, "per_insertion", 0)
	insSeedTypePricing(t, ctx, pool, camp, stPI, typeID, 3.0)
	insSeedStationPricing(t, ctx, pool, camp, stCons, "consolidated", 5000)
	// distribution rule para AMBAS as emissoras (uma chamada por emissora — ver nota da assinatura)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, stPI, "2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, stCons, "2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)

	// target pmm nas duas
	v300, v900 := 300, 900
	if _, _, err := NewClientStationPMM(pool).BulkUpsert(ctx, client, []TargetPMMEntry{
		{StationID: stPI, PMMTarget: &v300}, {StationID: stCons, PMMTarget: &v900},
	}); err != nil {
		t.Fatalf("target: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM client_station_pmm WHERE client_id=$1", client) })

	insSeedDetection(t, ctx, pool, camp, mat, stPI, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, stPI, "in_slot", "2026-06-11")
	insSeedDetection(t, ctx, pool, camp, mat, stCons, "in_slot", "2026-06-10")
	// out_slot + out_date na stPI: base A (in_slot+bonus) EXCLUI ambas, então
	// os valores esperados NÃO mudam. Se algum lado passar a contá-las, os dois
	// deixam de bater E divergem do absoluto — o teste pega (regressão do header:
	// "reintroduzir out_slot no investido de um lado só").
	insSeedDetection(t, ctx, pool, camp, mat, stPI, "out_slot", "2026-06-12")
	insSeedDetection(t, ctx, pool, camp, mat, stPI, "out_date", "2026-06-13")

	from, to, today := parseDate("2026-06-01"), parseDate("2026-06-30"), parseDate("2026-06-30")

	// /campaigns
	fins, err := NewCampaigns(pool).FinancialsByCampaign(ctx, &client, from, to, today)
	if err != nil {
		t.Fatalf("campaigns: %v", err)
	}
	var camF CampaignFinancials
	found := false
	for _, f := range fins {
		if f.CampaignID == camp {
			camF = f
			found = true
		}
	}
	if !found {
		t.Fatalf("campanha %s não veio em FinancialsByCampaign — teste seria vacuoso (camF zero-value)", camp)
	}

	// /insights (mesma campanha, mesma janela)
	core, err := NewInsights(pool).aggregateCore(ctx, InsightsParams{
		CampaignIDs: []uuid.UUID{camp}, From: from, To: to, Today: today, StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("insights: %v", err)
	}

	// Magnitudes absolutas: garante que o teste não é vacuoso se AMBOS os
	// caminhos quebrarem juntos (ex.: os dois passarem a contar out_slot). Com
	// out_slot+out_date semeados, base A ainda deve dar exatamente estes valores.
	if int64(camF.TotalAudience) != 4000 {
		t.Errorf("impactos absoluto = %v, want 4000", camF.TotalAudience)
	}
	if int64(camF.TotalAudienceTarget) != 1500 {
		t.Errorf("impactos_target absoluto = %v, want 1500", camF.TotalAudienceTarget)
	}
	if !approxEq(camF.TotalInvested, 5006, 0.01) {
		t.Errorf("investido absoluto = %v, want 5006", camF.TotalInvested)
	}
	if camF.StationsWithTarget != 2 {
		t.Errorf("stations_with_target absoluto = %d, want 2", camF.StationsWithTarget)
	}

	// Igualdade campaigns == insights: as duas telas partem do MESMO fin_base.
	if int64(camF.TotalAudience) != core.Impactos {
		t.Errorf("impactos divergem: campaigns=%v insights=%v", camF.TotalAudience, core.Impactos)
	}
	if int64(camF.TotalAudienceTarget) != core.ImpactosTarget {
		t.Errorf("impactos_target divergem: campaigns=%v insights=%v", camF.TotalAudienceTarget, core.ImpactosTarget)
	}
	if !approxEq(camF.TotalInvested, core.Executado, 0.01) {
		t.Errorf("investido diverge: campaigns=%v insights=%v", camF.TotalInvested, core.Executado)
	}
	if camF.StationsWithTarget != core.StationsWithTarget {
		t.Errorf("stations_with_target divergem: campaigns=%d insights=%d", camF.StationsWithTarget, core.StationsWithTarget)
	}
}
