package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func covCHealthCleanup(t *testing.T, w *covCWorld) {
	t.Helper()
	t.Cleanup(func() {
		w.pool.Exec(w.ctx, `DELETE FROM stream_health_events WHERE station_id = $1`, w.station)
	})
}

func TestCovC_HealthEvents_DownUpEDuracao(t *testing.T) {
	w := covCSeedWorld(t, "health-flow")
	covCHealthCleanup(t, w)
	h := NewHealthEvents(w.pool)

	// Sem evento aberto → ErrNoRows.
	_, err := h.GetLastOpenDown(w.ctx, w.station)
	require.ErrorIs(t, err, pgx.ErrNoRows)

	id, at, err := h.RecordDown(w.ctx, w.station)
	require.NoError(t, err)
	assert.NotZero(t, id)
	assert.False(t, at.IsZero())

	open, err := h.GetLastOpenDown(w.ctx, w.station)
	require.NoError(t, err)
	assert.Equal(t, id, open.ID)
	assert.Equal(t, "down", open.EventType)
	assert.Nil(t, open.DurationSeconds)

	require.NoError(t, h.UpdateDownDuration(w.ctx, id, at, 125))
	_, err = h.GetLastOpenDown(w.ctx, w.station)
	require.ErrorIs(t, err, pgx.ErrNoRows, "com duração preenchida o down deixa de estar aberto")

	require.NoError(t, h.RecordUp(w.ctx, w.station))
	events, err := h.GetForStation(w.ctx, w.station, 7)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "down", events[0].EventType)
	require.NotNil(t, events[0].DurationSeconds)
	assert.Equal(t, 125, *events[0].DurationSeconds)
	assert.Equal(t, "up", events[1].EventType)

	// Outra emissora não enxerga os eventos desta.
	other, err := h.GetForStation(w.ctx, uuid.New(), 7)
	require.NoError(t, err)
	assert.Empty(t, other)
}

func TestCovC_HealthEvents_GetForStation_RespeitaJanelaDeDias(t *testing.T) {
	w := covCSeedWorld(t, "health-window")
	covCHealthCleanup(t, w)
	h := NewHealthEvents(w.pool)

	_, err := w.pool.Exec(w.ctx, `
		INSERT INTO stream_health_events (station_id, event_type, event_at, duration_seconds)
		VALUES ($1, 'down', now() - interval '10 days', 60),
		       ($1, 'down', now() - interval '1 day', 30)`, w.station)
	require.NoError(t, err)

	last3, err := h.GetForStation(w.ctx, w.station, 3)
	require.NoError(t, err)
	require.Len(t, last3, 1)
	assert.Equal(t, 30, *last3[0].DurationSeconds)

	last30, err := h.GetForStation(w.ctx, w.station, 30)
	require.NoError(t, err)
	require.Len(t, last30, 2)
	assert.True(t, last30[0].EventAt.Before(last30[1].EventAt), "ordem ascendente")
}

func TestCovC_HealthEvents_CloseOrphanedOpenDowns(t *testing.T) {
	w := covCSeedWorld(t, "health-orphans")
	covCHealthCleanup(t, w)
	h := NewHealthEvents(w.pool)

	// Três downs sem duração (zumbis) + um já fechado.
	_, err := w.pool.Exec(w.ctx, `
		INSERT INTO stream_health_events (station_id, event_type, event_at, duration_seconds)
		VALUES ($1, 'down', now() - interval '3 hours', NULL),
		       ($1, 'down', now() - interval '2 hours', NULL),
		       ($1, 'down', now() - interval '1 hour', NULL),
		       ($1, 'down', now() - interval '5 hours', 90)`, w.station)
	require.NoError(t, err)

	closed, err := h.CloseOrphanedOpenDowns(w.ctx, w.station)
	require.NoError(t, err)
	assert.Equal(t, 2, closed, "fecha todos menos o mais recente")

	// Sobrou exatamente 1 down aberto: o mais recente.
	open, err := h.GetLastOpenDown(w.ctx, w.station)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(-time.Hour), open.EventAt, 5*time.Minute)

	var openCount int
	require.NoError(t, w.pool.QueryRow(w.ctx,
		`SELECT count(*) FROM stream_health_events WHERE station_id=$1 AND event_type='down' AND duration_seconds IS NULL`,
		w.station).Scan(&openCount))
	assert.Equal(t, 1, openCount)

	// Os fechados recebem uma duração estimada (~ tempo decorrido).
	var durOldest int
	require.NoError(t, w.pool.QueryRow(w.ctx, `
		SELECT duration_seconds FROM stream_health_events
		 WHERE station_id=$1 AND event_at < now() - interval '2 hours 30 minutes' AND event_at > now() - interval '3 hours 30 minutes'`,
		w.station).Scan(&durOldest))
	assert.InDelta(t, 3*3600, durOldest, 300)

	// Idempotente.
	again, err := h.CloseOrphanedOpenDowns(w.ctx, w.station)
	require.NoError(t, err)
	assert.Zero(t, again)
}

func TestCovC_HealthEvents_GetSummariesForActive(t *testing.T) {
	w := covCSeedWorld(t, "health-summary")
	covCHealthCleanup(t, w)
	h := NewHealthEvents(w.pool)

	// Pausada: não entra no resumo.
	_, err := w.pool.Exec(w.ctx, `
		INSERT INTO stream_health_events (station_id, event_type, event_at, duration_seconds)
		VALUES ($1, 'down', now() - interval '2 hours', 3600)`, w.station)
	require.NoError(t, err)
	sums, err := h.GetSummariesForActive(w.ctx, 7)
	require.NoError(t, err)
	_, found := sums[w.station]
	assert.False(t, found, "emissora pausada fica fora do resumo")

	require.NoError(t, NewStations(w.pool).UpdateMonitoringStatus(w.ctx, w.station, "active"))
	// Um incidente fechado de 1h + um aberto recente.
	_, err = w.pool.Exec(w.ctx, `
		INSERT INTO stream_health_events (station_id, event_type, event_at, duration_seconds)
		VALUES ($1, 'down', now() - interval '30 minutes', NULL)`, w.station)
	require.NoError(t, err)

	sums, err = h.GetSummariesForActive(w.ctx, 7)
	require.NoError(t, err)
	s := sums[w.station]
	require.NotNil(t, s)
	assert.Equal(t, 2, s.IncidentCount)
	assert.True(t, s.IsCurrentlyDown, "último evento é um down sem duração")
	require.NotNil(t, s.LastIncidentAt)
	assert.Len(t, s.DailySummary, 7)
	// 1h fechada + ~30min aberta em 7 dias: uptime ligeiramente abaixo de 100%.
	assert.Less(t, s.UptimePct, 100.0)
	assert.Greater(t, s.UptimePct, 99.0)
}

func TestCovC_HealthEvents_ComputeSummary_UptimeSatura(t *testing.T) {
	sid := uuid.New()
	at := time.Now().Add(-time.Hour)
	over := 10 * 86400
	// Downtime maior que o período: uptime nunca fica negativo.
	s := computeSummary(sid, []HealthEvent{{StationID: sid, EventType: "down", EventAt: at, DurationSeconds: &over}}, 1, 86400)
	assert.Equal(t, 0.0, s.UptimePct)
	assert.False(t, s.IsCurrentlyDown, "down com duração está fechado")
	assert.Equal(t, 1, s.IncidentCount)

	// Sem eventos: 100% e nada aberto.
	empty := computeSummary(sid, nil, 3, 3*86400)
	assert.Equal(t, 100.0, empty.UptimePct)
	assert.False(t, empty.IsCurrentlyDown)
	assert.Nil(t, empty.LastIncidentAt)
	assert.Len(t, empty.DailySummary, 3)
}

func TestCovC_HealthEvents_ErroDeBancoPropaga(t *testing.T) {
	w := covCSeedWorld(t, "health-dberr")
	h := NewHealthEvents(w.pool)
	ctx := covCCancelled()

	_, _, err := h.RecordDown(ctx, w.station)
	require.Error(t, err)
	require.Error(t, h.RecordUp(ctx, w.station))
	require.Error(t, h.UpdateDownDuration(ctx, 1, time.Now(), 5))
	_, err = h.CloseOrphanedOpenDowns(ctx, w.station)
	require.Error(t, err)
	_, err = h.GetLastOpenDown(ctx, w.station)
	require.Error(t, err)
	_, err = h.GetForStation(ctx, w.station, 1)
	require.Error(t, err)
	_, err = h.GetSummariesForActive(ctx, 1)
	require.Error(t, err)
}
