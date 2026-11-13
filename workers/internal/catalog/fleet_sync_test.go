package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestFleetSync_CoveredStationsAndMonitoringStatus cobre as duas consultas do
// reconciler da frota: quais emissoras uma campanha 'ativa' cobre, e o acerto
// de stations.monitoring_status contra isso. Reproduz o incidente 2026-10-02:
// 80 emissoras ficaram 'active' sem campanha nenhuma (sobra do alívio manual
// da FSJ + edição da lista com a campanha 'programada'), inflando o "Streams
// ao ar" e o "Atenção agora" com falso "drift do reconciler".
func TestFleetSync_CoveredStationsAndMonitoringStatus(t *testing.T) {
	ctx, pool := newTestDB(t)
	stations := NewStations(pool)
	campaigns := NewCampaigns(pool)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "fleet-sync-" + uuid.NewString()})
	require.NoError(t, err)

	newStation := func(status string) uuid.UUID {
		st, err := stations.Create(ctx, CreateStationInput{
			Name: "fleet-sync-" + uuid.NewString(), Band: "FM", StreamURL: "http://fleet-sync.invalid/s",
		})
		require.NoError(t, err)
		require.NoError(t, stations.UpdateMonitoringStatus(ctx, st.ID, status))
		return st.ID
	}
	covered := newStation("paused")          // em campanha ativa, marcada paused → vira active
	progOnly := newStation("active")         // só em campanha programada → vira paused
	stale := newStation("active")            // sem campanha nenhuma → vira paused
	calibrating := newStation("calibrating") // estado manual: não se mexe

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id=$1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id=$1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = ANY($1)`, []uuid.UUID{covered, progOnly, stale, calibrating})
	})

	_, err = pool.Exec(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status, target_stations) VALUES
		  ($1, $2, CURRENT_DATE - 1, CURRENT_DATE + 20, 'ativa',      ARRAY[$4::uuid]),
		  ($1, $3, CURRENT_DATE + 5, CURRENT_DATE + 20, 'programada', ARRAY[$5::uuid])`,
		cli.ID, "fleet-ativa-"+uuid.NewString(), "fleet-prog-"+uuid.NewString(), covered, progOnly)
	require.NoError(t, err)

	got, err := campaigns.StationsCoveredByActive(ctx)
	require.NoError(t, err)
	require.Contains(t, got, covered)
	require.NotContains(t, got, progOnly, "programada não cobre")
	require.NotContains(t, got, stale)

	paused, activated, err := stations.SyncMonitoringStatus(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, paused, int64(2), "progOnly + stale")
	require.GreaterOrEqual(t, activated, int64(1), "covered")

	statusOf := func(id uuid.UUID) string {
		var s string
		require.NoError(t, pool.QueryRow(ctx, `SELECT monitoring_status FROM stations WHERE id=$1`, id).Scan(&s))
		return s
	}
	require.Equal(t, "active", statusOf(covered))
	require.Equal(t, "paused", statusOf(progOnly))
	require.Equal(t, "paused", statusOf(stale))
	require.Equal(t, "calibrating", statusOf(calibrating), "estado manual não é sobrescrito")

	// Idempotente: a 2ª passada não acha nada destas emissoras pra mexer.
	_, _, err = stations.SyncMonitoringStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, "active", statusOf(covered))
	require.Equal(t, "paused", statusOf(stale))
}
