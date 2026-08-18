package catalog

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"radiocheck/internal/db"
)

// TestMultiAttribution_OneAiringTwoCampaigns prova o invariante central do F-119:
// UMA tocada física (uma linha em detections) projetada em DUAS campanhas conta
// para cada uma de forma independente (grade lê detection_campaigns.category),
// o gate "aprovado" vem da tocada BASE (retrair a base esconde das duas), e as
// leituras swapadas (LiveMap.Get) rodam sobre a view.
func TestMultiAttribution_OneAiringTwoCampaigns(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	defer pool.Close()

	client := insSeedClient(t, ctx, pool, "FanOut")
	campA := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	campB := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	_ = typeID
	st := insSeedStation(t, ctx, pool, "FanRadio", 1000, 60, 40, 20, 50, 30, 30, 50, 20)

	// 1 tocada física (canônica campA) + 2 projeções: campA in_slot, campB bonus.
	// ('bonus' era 'orphan' até 0064/0065 — o categorizador de cota grava a
	// categoria explícita e a view só conta 'bonus'.)
	ts := parseDate("2026-06-10").Add(12 * time.Hour)
	var detID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used, category)
		VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, 'in_slot')
		RETURNING id`, st, mat, campA, ts).Scan(&detID); err != nil {
		t.Fatalf("seed detection: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'in_slot'), ($1, $2, $5, $4, 'bonus')`,
		detID, ts, campA, mat, campB); err != nil {
		t.Fatalf("seed projections: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detection_campaigns WHERE detection_id = $1`, detID)
		pool.Exec(ctx, `DELETE FROM detections WHERE id = $1`, detID)
	})

	from := parseDate("2026-06-01")
	to := parseDate("2026-06-30")
	ds := NewDailySummary(pool)

	inSlotFor := func(camp uuid.UUID) int32 {
		rows, err := ds.ListByCampaign(ctx, camp, from, to)
		if err != nil {
			t.Fatalf("ListByCampaign: %v", err)
		}
		var n int32
		for _, r := range rows {
			if r.StationID == st {
				n += r.InSlot
			}
		}
		return n
	}
	bonusFor := func(camp uuid.UUID) int32 {
		rows, err := ds.ListByCampaign(ctx, camp, from, to)
		if err != nil {
			t.Fatalf("ListByCampaign: %v", err)
		}
		var n int32
		for _, r := range rows {
			if r.StationID == st {
				n += r.Bonus
			}
		}
		return n
	}

	// campA vê a tocada como in_slot; campB como bônus.
	if got := inSlotFor(campA); got != 1 {
		t.Errorf("campA in_slot = %d, want 1", got)
	}
	if got := inSlotFor(campB); got != 0 {
		t.Errorf("campB in_slot = %d, want 0", got)
	}
	if got := bonusFor(campB); got < 1 {
		t.Errorf("campB bonus = %d, want >= 1 (a projeção de campB é bônus)", got)
	}

	// Leitura swapada roda sobre a view (smoke runtime) — uma campanha e as
	// duas juntas (o /live-map aceita seleção múltipla).
	if _, err := NewLiveMap(pool).Get(ctx, []uuid.UUID{campA}, nil, LiveMapOpts{}); err != nil {
		t.Fatalf("LiveMap.Get: %v", err)
	}
	if _, err := NewLiveMap(pool).Get(ctx, []uuid.UUID{campA, campB}, nil, LiveMapOpts{}); err != nil {
		t.Fatalf("LiveMap.Get multi: %v", err)
	}

	// Gate aprovado: retrair a tocada BASE esconde das DUAS campanhas.
	if _, err := pool.Exec(ctx, `UPDATE detections SET retracted_at = now() WHERE id = $1`, detID); err != nil {
		t.Fatalf("retract: %v", err)
	}
	if got := inSlotFor(campA); got != 0 {
		t.Errorf("após retração campA in_slot = %d, want 0", got)
	}
	if got := inSlotFor(campB); got != 0 || bonusFor(campB) != 0 {
		t.Errorf("após retração campB in_slot/bonus != 0")
	}
}
