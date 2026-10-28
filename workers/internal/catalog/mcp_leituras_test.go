package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestClients_HubIDs(t *testing.T) {
	ctx, pool := newTestDB(t)
	com := insSeedClient(t, ctx, pool, "Com ponte "+uuid.NewString()[:6])
	sem := insSeedClient(t, ctx, pool, "Sem ponte "+uuid.NewString()[:6])
	hubID := "hub-" + uuid.NewString()
	_, err := pool.Exec(ctx, `UPDATE clients SET hub_id = $1 WHERE id = $2`, hubID, com)
	require.NoError(t, err)

	got, err := NewClients(pool).HubIDs(ctx, []uuid.UUID{com, sem})
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]string{com: hubID}, got)

	vazio, err := NewClients(pool).HubIDs(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, vazio)
}

func TestCampaigns_ListByHubCodeECancelledAtFor(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Hub code "+uuid.NewString()[:6])
	a := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	b := insSeedCampaign(t, ctx, pool, cli, "2026-11-01", "2026-11-30")
	c := insSeedCampaign(t, ctx, pool, cli, "2026-12-01", "2026-12-31")
	_, err := pool.Exec(ctx, `UPDATE campaigns SET hub_code = 'EH-2346AB' WHERE id = ANY($1)`, []uuid.UUID{a, b})
	require.NoError(t, err)

	repo := NewCampaigns(pool)
	got, err := repo.ListByHubCode(ctx, "EH-2346AB")
	require.NoError(t, err)
	require.Len(t, got, 2, "o código não é único: dois PIs podem dividi-lo")
	require.Equal(t, b, got[0].ID, "mais nova primeiro")

	nenhuma, err := repo.ListByHubCode(ctx, "EH-ZZZZZZ")
	require.NoError(t, err)
	require.Empty(t, nenhuma)

	_, err = pool.Exec(ctx, `UPDATE campaigns SET status = 'cancelada', cancelled_at = now() WHERE id = $1`, c)
	require.NoError(t, err)
	canc, err := repo.CancelledAtFor(ctx, []uuid.UUID{a, c})
	require.NoError(t, err)
	require.Len(t, canc, 1)
	require.WithinDuration(t, time.Now(), canc[c], time.Minute)
}
