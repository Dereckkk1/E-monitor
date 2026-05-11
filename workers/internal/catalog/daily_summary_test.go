package catalog

import (
	"testing"
	"time"
)

func TestDailySummary_EmptyCampaign(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
	})

	repo := NewDailySummary(pool)
	rows, err := repo.ListByCampaign(ctx, cmp.ID,
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Empty campaign (no rules, no detections) — view returns nothing for this campaign
	if len(rows) != 0 {
		t.Errorf("empty campaign: got %d rows, want 0", len(rows))
	}
}
