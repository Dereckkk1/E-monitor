package supervisor

import (
	"context"
	"os"
	"testing"
	"time"

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
	pool, err := db.New(ctx, url, zap.NewNop())
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

// TestRetireStalledWorker_NoOverlappingDowns trava o incidente 2026-11-08
// (Positiva 99.1: 38 downs sobrepostos, 37,8 h "fora do ar" num dia). O
// watchdog registrava o down numa goroutine e, no mesmo instante, apagava o
// entry do worker; quando a goroutine rodava, o entry (com o lastDownID
// adotado) já não existia → abria um 2º down com o 1º ainda aberto, e o
// startup recovery do respawn fechava o antigo com duração "até agora" —
// dois intervalos sobrepostos por ciclo de stall.
//
// Dois ciclos stall → respawn → stall têm que deixar UM down só, gravado
// antes de retireStalledWorker retornar.
func TestRetireStalledWorker_NoOverlappingDowns(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })

	stationID := uuid.New()
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM stream_health_events WHERE station_id = $1`, stationID)
	})

	he := catalog.NewHealthEvents(pool)
	s := &Supervisor{
		healthEvents: he,
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
	// respawn imita o startup recovery de startStationWorker: fecha zumbis e
	// adota o down aberto num entry novo.
	respawn := func() {
		_, err := he.CloseOrphanedOpenDowns(ctx, stationID)
		require.NoError(t, err)
		e := &workerEntry{}
		if ev, err := he.GetLastOpenDown(ctx, stationID); err == nil {
			e.lastDownID, e.lastDownAt = &ev.ID, &ev.EventAt
		}
		s.mu.Lock()
		s.workers[stationID] = e
		s.mu.Unlock()
	}

	// Ciclo 1: stall sem down aberto (never-connected, com backoff).
	s.retireStalledWorker(stationID, func() {}, time.Minute)
	require.Equal(t, 1, countDowns(), "o down tem que estar gravado quando retireStalledWorker retorna")

	// Ciclo 2: respawn adota o down aberto; novo stall não pode abrir outro.
	respawn()
	s.retireStalledWorker(stationID, func() {}, time.Minute)
	time.Sleep(300 * time.Millisecond) // deixa qualquer goroutine perdida aparecer
	require.Equal(t, 1, countDowns(), "stall com down já aberto (adotado no respawn) não pode abrir um 2º down")

	var open int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM stream_health_events
		  WHERE station_id=$1 AND event_type='down' AND duration_seconds IS NULL`,
		stationID).Scan(&open))
	require.Equal(t, 1, open, "a queda continua aberta, uma só")
}
