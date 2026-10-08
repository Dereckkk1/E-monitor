package supervisor

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestLifecycleTick_DemotesFutureAtivaAndStopsWorkers: ponta a ponta do
// incidente 2026-10-01 ("FSJ - Pedido Jack"). Campanha 'ativa' com start_date
// no futuro → o tick a devolve pra 'programada', chama OnDemoted com o id dela,
// e o OnDemoted de produção (StopWorkersForCampaign) para o worker da emissora
// que só ela cobria.
func TestLifecycleTick_DemotesFutureAtivaAndStopsWorkers(t *testing.T) {
	f := newReconcileFixture(t)

	var campID uuid.UUID
	require.NoError(t, f.pool.QueryRow(f.ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status, target_stations)
		VALUES ($1, $2, CURRENT_DATE + 5, CURRENT_DATE + 20, 'ativa', ARRAY[$3::uuid])
		RETURNING id`,
		f.clientID, "demote-tick-"+uuid.NewString(), f.stationID).Scan(&campID))

	sched := NewLifecycleScheduler(nil, f.s.campaigns, &mockBus{}, zap.NewNop())
	var demoted []uuid.UUID
	sched.OnDemoted = func(_ context.Context, id uuid.UUID) {
		demoted = append(demoted, id)
		f.s.StopWorkersForCampaign(id)
	}

	sched.tick(f.ctx)

	require.Contains(t, demoted, campID, "OnDemoted must fire for the demoted campaign")
	var status string
	require.NoError(t, f.pool.QueryRow(f.ctx, `SELECT status FROM campaigns WHERE id=$1`, campID).Scan(&status))
	require.Equal(t, "programada", status)

	f.s.mu.Lock()
	_, stillThere := f.s.workers[f.stationID]
	f.s.mu.Unlock()
	require.False(t, stillThere, "worker of a station covered only by the demoted campaign must stop")
	require.Error(t, f.workerCtx.Err())
	require.Equal(t, "paused", f.monitoringStatus(t))
}
