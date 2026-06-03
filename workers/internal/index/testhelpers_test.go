package index

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"radiocheck/internal/db"
)

// newTestDB connects to TEST_DATABASE_URL, skipping when unset. Mirrors
// catalog/testhelpers_test.go.
func newTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
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

// seedCampaign inserts a campaign with explicit status/dates and returns its ID.
func seedCampaign(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	clientID uuid.UUID, status string, start, end string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'reuse-camp-'||$2, $3::date, $4::date, $2)
		RETURNING id`, clientID, status, start, end).Scan(&id)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, id) })
	return id
}

// seedBackfillReuse builds the dead-zone scenario directly via SQL so that the
// commercial and material share one UUID (what migration 0016's backfill
// produced). Returns the shared entity UUID and its short_id.
//
//   - commercial row: campaign_id = concludedCampaign (status 'concluida')
//   - material row:   same UUID, fingerprint_status 'ready'
//   - campaign_materials: links the material to activeCampaign targeting station
//   - fingerprint_hashes: nHashes rows under the shared UUID
//
// All rows are removed on t.Cleanup.
func seedBackfillReuse(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	concludedCampaign, activeCampaign, station uuid.UUID, nHashes int) (uuid.UUID, int32) {
	t.Helper()
	id := uuid.New()
	sha := "sha-" + id.String()
	var shortID int32
	err := pool.QueryRow(ctx, `
		INSERT INTO commercials (id, campaign_id, title, duration_seconds,
		                         master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, 'reuse-spot', 30, '/tmp/r.mp3', $3, 'ready')
		RETURNING short_id`, id, concludedCampaign, sha).Scan(&shortID)
	require.NoError(t, err)

	// Material shares the UUID and short_id (the 0016 backfill behavior).
	_, err = pool.Exec(ctx, `
		INSERT INTO materials (id, short_id, client_id, title, duration_seconds,
		                       master_storage_path, master_sha256, fingerprint_status)
		SELECT $1, $2, ca.client_id, 'reuse-spot', 30, '/tmp/r.mp3', $4, 'ready'
		FROM campaigns ca WHERE ca.id = $3`, id, shortID, activeCampaign, sha)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, ARRAY[$3]::uuid[])`, activeCampaign, id, station)
	require.NoError(t, err)

	for i := 0; i < nHashes; i++ {
		_, err = pool.Exec(ctx, `
			INSERT INTO fingerprint_hashes (commercial_id, variant_id, rate_id, hash_value, time_frame, is_shared)
			VALUES ($1, 0, 0, $2, $3, false)`, id, int64(1000+i), i)
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM fingerprint_hashes WHERE commercial_id = $1`, id)
		pool.Exec(ctx, `DELETE FROM campaign_materials WHERE material_id = $1`, id)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, id)
	})
	return id, shortID
}
