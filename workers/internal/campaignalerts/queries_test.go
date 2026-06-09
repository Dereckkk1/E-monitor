package campaignalerts

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"radiocheck/internal/db"
)

func newTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url)
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return ctx, pool
}

func TestStartingNoMaterial(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := New(pool)

	cli := mustClient(t, ctx, pool, "Alerts Co")
	// Anchor: hoje = uma segunda fixa. Campanha programada que começa em 1 dia,
	// sem material → deve aparecer.
	today := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC) // Monday
	soon := mustCampaign(t, ctx, pool, cli, "programada", "2026-06-09", "2026-06-30")
	// Campanha programada com material → NÃO aparece no disparo 1.
	withMat := mustCampaign(t, ctx, pool, cli, "programada", "2026-06-09", "2026-06-30")
	mustMaterialOn(t, ctx, pool, cli, withMat)
	// Campanha longe → não aparece.
	mustCampaign(t, ctx, pool, cli, "programada", "2026-06-30", "2026-07-30")

	got, err := repo.StartingNoMaterial(ctx, today)
	if err != nil {
		t.Fatalf("StartingNoMaterial: %v", err)
	}
	if !containsID(got, soon) {
		t.Errorf("esperava a campanha sem material na janela")
	}
	if containsID(got, withMat) {
		t.Errorf("campanha COM material não deveria aparecer no disparo 1")
	}
}

func TestStarting_IncludesWithMaterial(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := New(pool)
	cli := mustClient(t, ctx, pool, "Alerts Co 2")
	today := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	withMat := mustCampaign(t, ctx, pool, cli, "programada", "2026-06-09", "2026-06-30")
	mustMaterialOn(t, ctx, pool, cli, withMat)

	got, err := repo.Starting(ctx, today)
	if err != nil {
		t.Fatalf("Starting: %v", err)
	}
	if !containsID(got, withMat) {
		t.Errorf("disparo 2 deve incluir campanha com material")
	}
}

func TestEnding_OnlyActive(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := New(pool)
	cli := mustClient(t, ctx, pool, "Alerts Co 3")
	today := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	ending := mustCampaign(t, ctx, pool, cli, "ativa", "2026-05-01", "2026-06-09")
	// programada terminando na janela NÃO entra (disparo 3 = só ativa).
	prog := mustCampaign(t, ctx, pool, cli, "programada", "2026-06-08", "2026-06-09")

	got, err := repo.Ending(ctx, today)
	if err != nil {
		t.Fatalf("Ending: %v", err)
	}
	if !containsID(got, ending) {
		t.Errorf("disparo 3 deve incluir campanha ativa terminando")
	}
	if containsID(got, prog) {
		t.Errorf("disparo 3 não deve incluir programada")
	}
}

// ── helpers de fixture ───────────────────────────────────────────────
func containsID(rows []CampaignAlert, id uuid.UUID) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

func mustClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaign_materials WHERE campaign_id IN (SELECT id FROM campaigns WHERE client_id=$1)`, id) //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM materials WHERE client_id=$1`, id) //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id=$1`, id) //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM clients WHERE id=$1`, id)          //nolint:errcheck
	})
	return id
}

func mustCampaign(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cli uuid.UUID, status, start, end string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		 VALUES ($1, 'C', $2, $3, $4) RETURNING id`, cli, start, end, status).Scan(&id); err != nil {
		t.Fatalf("insert campaign: %v", err)
	}
	return id
}

func mustMaterialOn(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cli, camp uuid.UUID) {
	t.Helper()
	var matID uuid.UUID
	// materials tem NOT NULL em duration_seconds, master_storage_path e
	// master_sha256 (ver migrations/0016_material_library.up.sql).
	if err := pool.QueryRow(ctx,
		`INSERT INTO materials (client_id, title, duration_seconds, master_storage_path, master_sha256)
		 VALUES ($1, 'M', 30, 'test/path.mp3', 'deadbeef') RETURNING id`, cli).Scan(&matID); err != nil {
		t.Fatalf("insert material: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO campaign_materials (campaign_id, material_id) VALUES ($1, $2)`, camp, matID); err != nil {
		t.Fatalf("insert campaign_material: %v", err)
	}
}
