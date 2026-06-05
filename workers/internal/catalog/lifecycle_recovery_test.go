package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// seedCampaignRaw inserts a campaign with an explicit status + dates (which
// Campaigns.Create can't set — it always starts 'programada') and returns its id.
func seedCampaignRaw(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	clientID uuid.UUID, status string, start, end time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	name := "lc-" + status + "-" + uuid.NewString()
	err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, $2, $3::date, $4::date, $5)
		RETURNING id`, clientID, name, start, end, status).Scan(&id)
	require.NoError(t, err)
	return id
}

// TestPromoteScheduledLifecycle_RecoversStuckConcluida: a campaign wrongly left
// in 'concluida' while its window is still open (end_date >= today) must be
// recovered — to 'ativa' if it has started, 'programada' if not — and the
// recovered-to-ativa id must come back in `activated` so the scheduler starts
// its workers. Reproduces the TINTAS RENNER prod incident (end_date extended
// after the campaign concluded; PromoteScheduledLifecycle had no path back).
func TestPromoteScheduledLifecycle_RecoversStuckConcluida(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewCampaigns(pool)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "lc-recovery-" + uuid.NewString()})
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id=$1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id=$1`, cli.ID)
	})

	today := time.Now()
	// concluida but window open and already started → must recover to ativa
	stuckActive := seedCampaignRaw(t, ctx, pool, cli.ID, "concluida", today.AddDate(0, 0, -10), today.AddDate(0, 0, 20))
	// concluida but starts in the future → must recover to programada
	stuckFuture := seedCampaignRaw(t, ctx, pool, cli.ID, "concluida", today.AddDate(0, 0, 5), today.AddDate(0, 0, 20))
	// legitimately concluded (end_date passed) → must STAY concluida
	legitDone := seedCampaignRaw(t, ctx, pool, cli.ID, "concluida", today.AddDate(0, 0, -30), today.AddDate(0, 0, -5))
	// programada whose start arrived → must still activate (existing behavior)
	dueProg := seedCampaignRaw(t, ctx, pool, cli.ID, "programada", today.AddDate(0, 0, -1), today.AddDate(0, 0, 20))

	activated, _, err := repo.PromoteScheduledLifecycle(ctx)
	require.NoError(t, err)

	statusOf := func(id uuid.UUID) string {
		var s string
		require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id=$1`, id).Scan(&s))
		return s
	}
	require.Equal(t, "ativa", statusOf(stuckActive), "stuck concluida within window → ativa")
	require.Equal(t, "programada", statusOf(stuckFuture), "stuck concluida not yet started → programada")
	require.Equal(t, "concluida", statusOf(legitDone), "legitimately concluded (end_date passed) must stay concluida")
	require.Equal(t, "ativa", statusOf(dueProg), "programada whose start arrived → ativa (existing behavior intact)")

	require.Contains(t, activated, stuckActive, "recovered-to-ativa must be in activated so the scheduler starts workers")
	require.Contains(t, activated, dueProg)
	require.NotContains(t, activated, stuckFuture, "recovered-to-programada needs no worker start")
}
