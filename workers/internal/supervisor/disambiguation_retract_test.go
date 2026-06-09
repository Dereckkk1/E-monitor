package supervisor

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/db"
)

// retractTestDB connects to TEST_DATABASE_URL, skipping when unset (same gate as
// the other DB-backed integration tests). Run with:
//
//	TEST_DATABASE_URL=postgres://... go test ./internal/supervisor/ -run Retract
func retractTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	return ctx, pool
}

// TestMarkDetectionRetracted_MaterialCut is the regression test for the prod
// double-count of 2026-06-09: a 30s material cut (#78) retracting its 15s subset
// (#77). detections.commercial_id is a MATERIAL uuid, but the old retract JOINed
// `commercials` only -> 0 rows -> the 15s stayed `available` and was counted
// twice. The fix resolves the short id via commercials UNION materials.
//
// The short id is minted via a commercial and the commercial row is then
// deleted, leaving the short id ONLY in materials — exactly the material-pure
// catalog state where the old query found nothing.
func TestMarkDetectionRetracted_MaterialCut(t *testing.T) {
	ctx, pool := retractTestDB(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('retract-mat-client') RETURNING id`).Scan(&clientID))
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID) })

	var campID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'retract-mat-camp', '2026-06-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&campID))
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, campID) })

	// Mint a short_id via a commercial, then delete the commercial so the short
	// id lives ONLY in materials.
	matID := uuid.New()
	commID := uuid.New()
	sha := "sha-retract-" + matID.String()
	var shortID int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO commercials (id, campaign_id, title, duration_seconds,
		                         master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, 'retract-mint', 15, '/tmp/m.mp3', $3, 'ready')
		RETURNING short_id`, commID, campID, sha).Scan(&shortID))
	_, err := pool.Exec(ctx, `
		INSERT INTO materials (id, short_id, client_id, title, duration_seconds,
		                       master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, $3, 'retract-mat', 15, '/tmp/m.mp3', $4, 'ready')`,
		matID, shortID, clientID, sha)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, commID)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, matID) })

	station := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO stations (id, name, band, stream_url, monitoring_status)
		VALUES ($1, 'retract-station', 'FM', 'http://x', 'active')`, station)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, station) })

	detectedAt := time.Date(2026, 6, 9, 12, 2, 22, 0, time.UTC)
	_, err = pool.Exec(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used, category)
		VALUES ($1, $2, $3, $4, 0, 15000, 0.27, 50, 0.5, 0, 0, 'in_slot')`,
		station, matID, campID, detectedAt)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM detections WHERE station_id = $1`, station) })

	s := &Supervisor{db: pool, log: zap.NewNop()}
	rows, err := s.markDetectionRetracted(ctx, shortID, station, detectedAt, time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, int64(1), rows,
		"material detection must be retracted (was 0 rows with the commercials-only JOIN)")

	var retractedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT retracted_at FROM detections WHERE station_id = $1 AND detected_at = $2`,
		station, detectedAt).Scan(&retractedAt))
	require.NotNil(t, retractedAt, "retracted_at must be stamped on the material detection")
}
