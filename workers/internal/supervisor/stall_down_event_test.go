package supervisor

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/db"
)

// TestRecordStreamDown_RecordsAndIdempotent cobre o core que o stall watchdog
// passa a chamar no hang/ban path (audit F1): abre 1 down event quando não há
// outage aberto, e é no-op quando o worker entry já tem lastDownID (idempotente
// por outage). Sem isso, um stream pendurado/banido não deixava down event
// (uptime 100%).
func TestRecordStreamDown_RecordsAndIdempotent(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })

	stationID := uuid.New()
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM stream_health_events WHERE station_id = $1`, stationID)
	})

	s := &Supervisor{
		healthEvents: catalog.NewHealthEvents(pool),
		log:          zap.NewNop(),
		workers:      map[uuid.UUID]*workerEntry{stationID: {}},
	}

	countDowns := func() int {
		var n int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT count(*) FROM stream_health_events WHERE station_id=$1 AND event_type='down'`,
			stationID).Scan(&n))
		return n
	}

	// 1) Sem down aberto → registra 1 e carimba lastDownID no entry.
	s.recordStreamDownSync(stationID)
	require.Equal(t, 1, countDowns(), "primeira chamada abre 1 down event")
	s.mu.Lock()
	require.NotNil(t, s.workers[stationID].lastDownID, "lastDownID deve ser carimbado")
	s.mu.Unlock()

	// 2) lastDownID já setado → no-op (idempotente por outage), sem 2º event.
	s.recordStreamDownSync(stationID)
	require.Equal(t, 1, countDowns(), "2ª chamada com down aberto deve ser no-op")
}
