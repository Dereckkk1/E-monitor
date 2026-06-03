package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestWorkerLoad_ReusedBackfillMaterial: a backfilled material (UUID in both
// tables; commercial.campaign_id = concluded) reused via campaign_materials in
// an active campaign that targets the station must be returned by the materials
// worker-load path, and the commercials path must NOT double-count it.
func TestWorkerLoad_ReusedBackfillMaterial(t *testing.T) {
	ctx, pool := newTestDB(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('reuse-wl-client') RETURNING id`).Scan(&clientID))
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID) })

	var concluded, active uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'wl-concl', '2026-05-01', '2026-05-31', 'concluida') RETURNING id`,
		clientID).Scan(&concluded))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'wl-active', '2026-06-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&active))
	station := uuid.New()
	id := uuid.New()
	sha := "sha-" + id.String()
	var shortID int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO commercials (id, campaign_id, title, duration_seconds,
		                         master_storage_path, master_sha256, fingerprint_status, target_stations)
		VALUES ($1, $2, 'wl-spot', 30, '/tmp/w.mp3', $4, 'ready', ARRAY[$3]::uuid[])
		RETURNING short_id`, id, concluded, station, sha).Scan(&shortID))
	_, err := pool.Exec(ctx, `
		INSERT INTO materials (id, short_id, client_id, title, duration_seconds,
		                       master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, $3, 'wl-spot', 30, '/tmp/w.mp3', $4, 'ready')`,
		id, shortID, clientID, sha)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, ARRAY[$3]::uuid[])`, active, id, station)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaign_materials WHERE material_id = $1`, id)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, concluded, active)
	})

	activeIDs := []uuid.UUID{active}

	mats, err := NewMaterials(pool).ListReadyByCampaignsForStation(ctx, activeIDs, station)
	require.NoError(t, err)
	require.Len(t, mats, 1, "materials path must return the reused backfill material")
	require.Equal(t, shortID, mats[0].ShortID)

	// commercials path is gated on the ACTIVE campaign id; the backfill's
	// commercial belongs to the concluded campaign, so it returns nothing here.
	coms, err := NewCommercials(pool).ListReadyByCampaignsForStation(ctx, activeIDs, station)
	require.NoError(t, err)
	require.Len(t, coms, 0, "commercials path must not return a backfill (now id NOT IN materials, and wrong campaign)")
}
