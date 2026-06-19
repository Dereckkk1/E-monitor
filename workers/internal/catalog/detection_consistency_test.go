package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// insSeedDetectionExcluded insere uma detection que NÃO pode contar em nenhum
// número de veiculação aprovado (catalog.ApprovedDetectionsFilter): retratada
// (§18.2.2), ignorada (admin "Desconsiderar") ou rejeitada pelo audit §9.9.
// `state` ∈ {"retracted","ignored","audit_rejected"}. Categoria 'in_slot' pra
// caírem no mesmo balde das aprovadas — assim, se um caminho esquecer o filtro,
// a contagem dele sobe e o teste pega.
func insSeedDetectionExcluded(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	campaignID, materialID, stationID uuid.UUID, dateISO, state string) {
	t.Helper()
	ts := parseDate(dateISO).Add(12 * time.Hour)
	var (
		retractedAt    *time.Time
		ignoredAt      *time.Time
		evidenceStatus = "available"
	)
	switch state {
	case "retracted":
		retractedAt = &ts
	case "ignored":
		ignoredAt = &ts
	case "audit_rejected":
		evidenceStatus = "audit_rejected"
	default:
		t.Fatalf("insSeedDetectionExcluded: estado desconhecido %q", state)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used,
		                        category, retracted_at, ignored_at, evidence_status)
		VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, 'in_slot', $5, $6, $7)
	`, stationID, materialID, campaignID, ts, retractedAt, ignoredAt, evidenceStatus)
	if err != nil {
		t.Fatalf("seed excluded detection (%s): %v", state, err)
	}
}

// TestDetectionCounts_ApprovedSetConsistency trava o que o operador reportou:
// modal de /detections, /insights e a grid (daily_play_summary) tinham que
// mostrar o MESMO número de veiculações, mas divergiam porque cada caminho
// filtrava diferente. Seedamos 3 veiculações aprovadas + 1 retratada + 1
// ignorada + 1 rejeitada-pelo-audit (todas 'in_slot', mesma emissora/material/
// dia) e exigimos que os três caminhos retornem 3.
func TestDetectionCounts_ApprovedSetConsistency(t *testing.T) {
	ctx, pool := newTestDB(t)

	client := insSeedClient(t, ctx, pool, "Consistency")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	_ = typeID
	st := insSeedStation(t, ctx, pool, "RadioConsist", 1000, 60, 40, 20, 50, 30, 30, 50, 20)

	const day = "2026-06-10"
	// 3 aprovadas
	for i := 0; i < 3; i++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", day)
	}
	// 3 que NÃO podem contar — uma por motivo de exclusão
	insSeedDetectionExcluded(t, ctx, pool, camp, mat, st, day, "retracted")
	insSeedDetectionExcluded(t, ctx, pool, camp, mat, st, day, "ignored")
	insSeedDetectionExcluded(t, ctx, pool, camp, mat, st, day, "audit_rejected")

	from := parseDate("2026-06-01")
	to := parseDate("2026-06-30")

	// ── Caminho 1: modal de /detections (Detections.List) ──
	list, err := NewDetections(pool).List(ctx, ListFilter{
		CampaignID: &camp, StationID: &st,
		StartDate: &from, EndDate: &to, Limit: 200,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("modal (Detections.List) = %d veiculações, want 3 (retratada/ignorada/audit_rejected não contam)", len(list))
	}

	// ── Caminho 2: /insights (aggregateCore) ──
	core, err := NewInsights(pool).aggregateCore(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: from, To: to, StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateCore: %v", err)
	}
	if core.VeiculacoesTotal != 3 {
		t.Errorf("/insights VeiculacoesTotal = %d, want 3", core.VeiculacoesTotal)
	}
	if core.Breakdown.InSlot != 3 {
		t.Errorf("/insights Breakdown.InSlot = %d, want 3", core.Breakdown.InSlot)
	}

	// ── Caminho 3: grid de /detections (view daily_play_summary) ──
	rows, err := NewDailySummary(pool).ListByCampaign(ctx, camp, from, to)
	if err != nil {
		t.Fatalf("ListByCampaign: %v", err)
	}
	var gridInSlot int32
	for _, r := range rows {
		if r.StationID == st {
			gridInSlot += r.InSlot
		}
	}
	if gridInSlot != 3 {
		t.Errorf("grid (daily_play_summary) in_slot = %d, want 3", gridInSlot)
	}
}
