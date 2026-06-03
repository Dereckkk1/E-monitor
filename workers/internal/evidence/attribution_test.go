package evidence

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"radiocheck/internal/db"
)

func attrTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
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

// TestResolveAttribution_ReusedBackfillPrefersActiveCampaign: the legacy
// commercial points to a concluded campaign, but the live campaign_materials
// link points to an active one. Attribution must pick the ACTIVE campaign.
func TestResolveAttribution_ReusedBackfillPrefersActiveCampaign(t *testing.T) {
	ctx, pool := attrTestDB(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('attr-client') RETURNING id`).Scan(&clientID))
	var concluded, active uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'attr-concl', '2026-05-01', '2026-05-31', 'concluida') RETURNING id`,
		clientID).Scan(&concluded))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'attr-active', '2026-06-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&active))
	station := uuid.New()
	id := uuid.New()
	sha := "sha-" + id.String()
	var shortID int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO commercials (id, campaign_id, title, duration_seconds,
		                         master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, 'attr-spot', 30, '/tmp/a.mp3', $3, 'ready')
		RETURNING short_id`, id, concluded, sha).Scan(&shortID))
	_, err := pool.Exec(ctx, `
		INSERT INTO materials (id, short_id, client_id, title, duration_seconds,
		                       master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, $3, 'attr-spot', 30, '/tmp/a.mp3', $4, 'ready')`,
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
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID)
	})

	detectedAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	gotCom, gotCamp, err := resolveAttribution(ctx, pool, shortID, station, detectedAt)
	require.NoError(t, err)
	require.Equal(t, id, gotCom)
	require.Equal(t, active, gotCamp, "must attribute to the active campaign, not the concluded one")
}

// TestResolveAttribution_PureLegacyCommercialFallback: a commercial with no
// material row resolves via the legacy commercials fallback.
func TestResolveAttribution_PureLegacyCommercialFallback(t *testing.T) {
	ctx, pool := attrTestDB(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('attr-legacy-client') RETURNING id`).Scan(&clientID))
	var camp uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'attr-legacy', '2026-06-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&camp))
	id := uuid.New()
	sha := "sha-" + id.String()
	var shortID int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO commercials (id, campaign_id, title, duration_seconds,
		                         master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, 'legacy-spot', 30, '/tmp/l.mp3', $3, 'ready')
		RETURNING short_id`, id, camp, sha).Scan(&shortID))

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, camp)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID)
	})

	gotCom, gotCamp, err := resolveAttribution(ctx, pool, shortID, uuid.New(),
		time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, id, gotCom)
	require.Equal(t, camp, gotCamp)
}
