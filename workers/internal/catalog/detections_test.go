package catalog

import (
	"testing"
	"time"
)

func TestDetections_Create_CategorizesOrphan(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Now().AddDate(0, 0, -1),
		EndDate:   time.Now().AddDate(0, 0, 30),
	})
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "abc-cat-orphan",
	})
	stat, _ := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Test FM", Band: "FM", StreamURL: "http://example.com",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})

	dets := NewDetections(pool)
	det, err := dets.Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt:         time.Now(),
		MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100,
		TemporalCoverage: 0.85, VariantUsed: 0, RateUsed: 0,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Sem regras criadas → deve ser orphan
	var category string
	err = pool.QueryRow(ctx,
		`SELECT category FROM detections WHERE id = $1 AND detected_at = $2`,
		det.ID, det.DetectedAt).Scan(&category)
	if err != nil {
		t.Fatalf("read category: %v", err)
	}
	if category != "orphan" {
		t.Errorf("category = %q, want orphan", category)
	}
}
