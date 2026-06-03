package index

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestLoadAll_ReusedBackfillMaterial reproduces the prod dead zone: a backfilled
// material (UUID also in commercials, commercial.campaign_id = a CONCLUDED
// campaign) reused via campaign_materials in an ACTIVE campaign. Before the fix
// its hashes land in neither index path; after, they load exactly once.
func TestLoadAll_ReusedBackfillMaterial(t *testing.T) {
	ctx, pool := newTestDB(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('reuse-client') RETURNING id`).Scan(&clientID))
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID) })

	concluded := seedCampaign(t, ctx, pool, clientID, "concluida", "2026-05-01", "2026-05-31")
	active := seedCampaign(t, ctx, pool, clientID, "ativa", "2026-06-01", "2026-07-31")
	station := uuid.New()

	_, shortID := seedBackfillReuse(t, ctx, pool, concluded, active, station, 5)

	store := New()
	loader := NewLoader(store, pool, nil, zap.NewNop())
	require.NoError(t, loader.LoadAll(ctx))

	// Count entries carrying our short_id across the whole index.
	got := 0
	for _, entries := range store.Load() {
		for _, e := range entries {
			if e.CommercialShortID == shortID {
				got++
			}
		}
	}
	require.Equal(t, 5, got, "expected the 5 reused-material hashes in the index exactly once each")
}
