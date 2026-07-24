package catalog

import (
	"testing"

	"github.com/google/uuid"
)

// Base A per_insertion: plays = in_slot + bonus; invested = unit × (in_slot+bonus).
func TestFinancialBase_PerInsertion(t *testing.T) {
	ctx, pool := newTestDB(t)

	clientID := insSeedClient(t, ctx, pool, "FB PerIns")
	camp := insSeedCampaign(t, ctx, pool, clientID, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, clientID, "Spot FB")
	st := insSeedStation(t, ctx, pool, "FB St", 1000, 60, 40, 20, 50, 30, 30, 50, 20)

	insSeedStationPricing(t, ctx, pool, camp, st, "per_insertion", 0)
	insSeedTypePricing(t, ctx, pool, camp, st, typeID, 2.0)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)

	insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, st, "orphan", "2026-06-10")

	rows, err := NewCatalogFinancials(pool).FinancialBase(ctx, []uuid.UUID{camp}, nil, []uuid.UUID{},
		parseDate("2026-06-01"), parseDate("2026-06-30"), parseDate("2026-06-30"))
	if err != nil {
		t.Fatalf("FinancialBase: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	// in_slot=3, expected(dia)=1 → bonus = max(0,3-1)+orphan(1) = 3. plays = 3+3 = 6.
	if rows[0].Plays != 6 {
		t.Errorf("plays = %d, want 6", rows[0].Plays)
	}
	// invested = 2.0 × plays(6) = 12.0
	if !approxEq(rows[0].Invested, 12.0, 0.001) {
		t.Errorf("invested = %v, want 12.0", rows[0].Invested)
	}
}

// Consolidado: invested = value × meses_no_período. Campanha 3 meses × R$1000,
// hoje mês 2, janela = campanha inteira → 2000. Janela = só mês 1 → 1000.
func TestFinancialBase_Consolidated(t *testing.T) {
	ctx, pool := newTestDB(t)
	clientID := insSeedClient(t, ctx, pool, "FB Cons")
	camp := insSeedCampaign(t, ctx, pool, clientID, "2026-06-01", "2026-08-31")
	st := insSeedStation(t, ctx, pool, "FB Cons St", 1000, 50, 50, 30, 40, 30, 30, 40, 30)
	insSeedStationPricing(t, ctx, pool, camp, st, "consolidated", 1000)

	fin := NewCatalogFinancials(pool)

	full, err := fin.FinancialBase(ctx, []uuid.UUID{camp}, nil, []uuid.UUID{},
		parseDate("2026-06-01"), parseDate("2026-08-31"), parseDate("2026-07-15"))
	if err != nil {
		t.Fatalf("FinancialBase full: %v", err)
	}
	if len(full) != 1 || !approxEq(full[0].Invested, 2000, 1) {
		t.Fatalf("full invested = %v, want ~2000", full)
	}

	jun, err := fin.FinancialBase(ctx, []uuid.UUID{camp}, nil, []uuid.UUID{},
		parseDate("2026-06-01"), parseDate("2026-06-30"), parseDate("2026-07-15"))
	if err != nil {
		t.Fatalf("FinancialBase jun: %v", err)
	}
	if len(jun) != 1 || !approxEq(jun[0].Invested, 1000, 1) {
		t.Fatalf("jun invested = %v, want ~1000", jun)
	}
}
