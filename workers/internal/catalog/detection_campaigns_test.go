package catalog

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"radiocheck/internal/db"
)

func TestInsertProjections_Idempotent(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url)
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	defer pool.Close()

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "proj-cli"})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{Name: "Proj FM", Band: "FM", StreamURL: "http://x"})
	if err != nil {
		t.Fatalf("station: %v", err)
	}
	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "Proj C", ClientID: cli.ID,
		StartDate:      time.Now().AddDate(0, 0, -1),
		EndDate:        time.Now().AddDate(0, 0, 30),
		TargetStations: []uuid.UUID{stat.ID},
	})
	if err != nil {
		t.Fatalf("campaign: %v", err)
	}
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "Proj M", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "proj-sha-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("material: %v", err)
	}
	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt:         time.Now(),
		MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100,
		TemporalCoverage: 0.85, VariantUsed: 0, RateUsed: 0,
	})
	if err != nil {
		t.Fatalf("detection: %v", err)
	}

	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detection_campaigns WHERE detection_id = $1", det.ID)
		pool.Exec(ctx, "DELETE FROM detections WHERE id = $1", det.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
	})

	repo := NewDetectionCampaigns(pool)
	projs := []Projection{{CampaignID: cmp.ID, CommercialID: mat.ID, Category: "in_slot"}}
	if err := repo.InsertProjections(ctx, det.ID, det.DetectedAt, projs); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	// re-inserir não pode duplicar (ON CONFLICT DO NOTHING)
	if err := repo.InsertProjections(ctx, det.ID, det.DetectedAt, projs); err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM detection_campaigns WHERE detection_id = $1", det.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("projections=%d, want 1 (ON CONFLICT DO NOTHING)", n)
	}
}
