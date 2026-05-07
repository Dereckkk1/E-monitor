# Stream Health Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the useless monitoring page with a streaming health dashboard backed by real `up`/`down` events recorded by the ingestor workers.

**Architecture:** Workers call `OnStreamUp`/`OnStreamDown` callbacks on stream state transitions; the supervisor records these to `stream_health_events`. Two new API endpoints serve summaries (list) and raw events (detail drawer). The frontend replaces `MonitoringPage` with a paginated list with mini 7-day health bars, plus a detail drawer with a zoomable timeline (1h → 15m → 1m resolution via drag-to-zoom).

**Tech Stack:** Go (`pgxpool`, `chi`, `pgx/v5`), React + TanStack Query, plain CSS `div` blocks for timeline rendering (no chart library).

---

## File Map

| Action | File | Responsibility |
|---|---|---|
| Create | `workers/internal/catalog/health_events.go` | DB repo for `stream_health_events` + summary computation |
| Create | `workers/internal/catalog/health_events_test.go` | Unit tests for pure summary logic |
| Modify | `workers/internal/catalog/stations.go` | Add `ListActive` method |
| Modify | `workers/internal/ingestor/worker.go` | Add `OnStreamUp`/`OnStreamDown` callbacks |
| Modify | `workers/internal/supervisor/supervisor.go` | Wire health event recording + startup recovery |
| Create | `workers/internal/api/handlers/stream_health.go` | HTTP handlers for health endpoints |
| Modify | `workers/internal/api/router.go` | Register new routes |
| Modify | `workers/cmd/api/main.go` | Wire catalog + handler |
| Modify | `frontend/src/api/hooks.js` | Add `useStreamHealth`, `useStationHealthEvents` |
| Create | `frontend/src/components/HealthTimeline.jsx` | Zoomable timeline block renderer |
| Replace | `frontend/src/pages/MonitoringPage.jsx` | New health dashboard (list + drawer) |
| Modify | `frontend/src/index.css` | Styles for health page, drawer, timeline |

---

## Task 1: HealthEvents catalog repo

**Files:**
- Create: `workers/internal/catalog/health_events.go`
- Create: `workers/internal/catalog/health_events_test.go`

- [ ] **Step 1: Write the failing tests for the pure summary functions**

```go
// workers/internal/catalog/health_events_test.go
package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestComputeSummary_NoEvents(t *testing.T) {
	id := uuid.New()
	got := computeSummary(id, nil, 7, float64(7*24*60*60))
	if got.UptimePct != 100.0 {
		t.Errorf("want 100%% uptime, got %.2f", got.UptimePct)
	}
	if got.IncidentCount != 0 {
		t.Errorf("want 0 incidents, got %d", got.IncidentCount)
	}
	if len(got.DailySummary) != 7 {
		t.Errorf("want 7 daily entries, got %d", len(got.DailySummary))
	}
}

func TestComputeSummary_WithDowntime(t *testing.T) {
	id := uuid.New()
	dur := 3600 // 1 hour down
	events := []HealthEvent{
		{EventType: "down", EventAt: time.Now().Add(-2 * time.Hour), DurationSeconds: &dur},
		{EventType: "up", EventAt: time.Now().Add(-1 * time.Hour)},
	}
	periodSec := float64(7 * 24 * 60 * 60)
	got := computeSummary(id, events, 7, periodSec)

	want := (1 - float64(dur)/periodSec) * 100
	if diff := got.UptimePct - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("want uptime %.4f%%, got %.4f%%", want, got.UptimePct)
	}
	if got.IncidentCount != 1 {
		t.Errorf("want 1 incident, got %d", got.IncidentCount)
	}
	if got.LastIncidentAt == nil {
		t.Error("want non-nil LastIncidentAt")
	}
}

func TestBuildDailySummary_AllUp(t *testing.T) {
	daily := buildDailySummary(nil, 3)
	if len(daily) != 3 {
		t.Fatalf("want 3 entries, got %d", len(daily))
	}
	for _, d := range daily {
		if d.UptimePct != 100.0 {
			t.Errorf("want 100%% uptime for %s, got %.2f", d.Date, d.UptimePct)
		}
	}
}

func TestBuildDailySummary_WithDowntime(t *testing.T) {
	dur := 43200 // 12 hours down on today
	today := time.Now().UTC().Format("2006-01-02")
	events := []HealthEvent{
		{EventType: "down", EventAt: time.Now().Add(-13 * time.Hour), DurationSeconds: &dur},
	}
	daily := buildDailySummary(events, 3)

	var todayEntry *DailySummary
	for i := range daily {
		if daily[i].Date == today {
			todayEntry = &daily[i]
		}
	}
	if todayEntry == nil {
		t.Fatal("today not in daily summary")
	}
	want := (1 - float64(dur)/86400.0) * 100
	if diff := todayEntry.UptimePct - want; diff > 0.1 || diff < -0.1 {
		t.Errorf("want today uptime %.2f%%, got %.2f%%", want, todayEntry.UptimePct)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail (types not defined yet)**

```bash
cd workers && go test ./internal/catalog/... -run TestComputeSummary -v
```
Expected: compile error — `HealthEvent`, `DailySummary`, etc. undefined.

- [ ] **Step 3: Create the catalog file**

```go
// workers/internal/catalog/health_events.go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Types ──────────────────────────────────────────────────────────────────

type HealthEvent struct {
	ID              int64
	StationID       uuid.UUID
	EventType       string // "up" | "down"
	EventAt         time.Time
	DurationSeconds *int
}

type DailySummary struct {
	Date      string  `json:"date"`       // "2026-05-01"
	UptimePct float64 `json:"uptime_pct"` // 0–100
}

type StationHealthSummary struct {
	StationID      uuid.UUID      `json:"station_id"`
	UptimePct      float64        `json:"uptime_pct"`
	IncidentCount  int            `json:"incident_count"`
	LastIncidentAt *time.Time     `json:"last_incident_at,omitempty"`
	DailySummary   []DailySummary `json:"daily_summary"`
}

// ─── Repository ─────────────────────────────────────────────────────────────

type HealthEvents struct {
	pool *pgxpool.Pool
}

func NewHealthEvents(pool *pgxpool.Pool) *HealthEvents {
	return &HealthEvents{pool: pool}
}

// RecordDown inserts a 'down' event. Returns (id, event_at) for retroactive duration update.
func (h *HealthEvents) RecordDown(ctx context.Context, stationID uuid.UUID) (int64, time.Time, error) {
	var id int64
	var at time.Time
	err := h.pool.QueryRow(ctx,
		`INSERT INTO stream_health_events (station_id, event_type)
		 VALUES ($1, 'down')
		 RETURNING id, event_at`,
		stationID,
	).Scan(&id, &at)
	return id, at, err
}

// RecordUp inserts an 'up' event.
func (h *HealthEvents) RecordUp(ctx context.Context, stationID uuid.UUID) error {
	_, err := h.pool.Exec(ctx,
		`INSERT INTO stream_health_events (station_id, event_type) VALUES ($1, 'up')`,
		stationID,
	)
	return err
}

// UpdateDownDuration retroactively fills duration_seconds on a 'down' event.
// Both id and eventAt are required because the table is partitioned on event_at.
func (h *HealthEvents) UpdateDownDuration(ctx context.Context, id int64, eventAt time.Time, durationSeconds int) error {
	_, err := h.pool.Exec(ctx,
		`UPDATE stream_health_events SET duration_seconds = $3
		 WHERE id = $1 AND event_at = $2`,
		id, eventAt, durationSeconds,
	)
	return err
}

// GetLastOpenDown returns the most recent 'down' event with no duration_seconds for a station.
// Used for startup recovery when a worker crashed mid-outage.
func (h *HealthEvents) GetLastOpenDown(ctx context.Context, stationID uuid.UUID) (*HealthEvent, error) {
	var ev HealthEvent
	err := h.pool.QueryRow(ctx,
		`SELECT id, station_id, event_type, event_at, duration_seconds
		 FROM stream_health_events
		 WHERE station_id = $1 AND event_type = 'down' AND duration_seconds IS NULL
		 ORDER BY event_at DESC
		 LIMIT 1`,
		stationID,
	).Scan(&ev.ID, &ev.StationID, &ev.EventType, &ev.EventAt, &ev.DurationSeconds)
	if err != nil {
		return nil, err // pgx.ErrNoRows if none found
	}
	return &ev, nil
}

// GetForStation returns raw events for a station in the last `days` days, ascending.
func (h *HealthEvents) GetForStation(ctx context.Context, stationID uuid.UUID, days int) ([]HealthEvent, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT id, station_id, event_type, event_at, duration_seconds
		 FROM stream_health_events
		 WHERE station_id = $1
		   AND event_at >= NOW() - ($2::int * INTERVAL '1 day')
		 ORDER BY event_at ASC`,
		stationID, days,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []HealthEvent
	for rows.Next() {
		var ev HealthEvent
		if err := rows.Scan(&ev.ID, &ev.StationID, &ev.EventType, &ev.EventAt, &ev.DurationSeconds); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// GetSummariesForActive fetches all health events for active stations in the
// last `days` days and returns computed summaries keyed by station ID.
func (h *HealthEvents) GetSummariesForActive(ctx context.Context, days int) (map[uuid.UUID]*StationHealthSummary, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT id, station_id, event_type, event_at, duration_seconds
		 FROM stream_health_events
		 WHERE station_id IN (SELECT id FROM stations WHERE monitoring_status = 'active')
		   AND event_at >= NOW() - ($1::int * INTERVAL '1 day')
		 ORDER BY station_id, event_at ASC`,
		days,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byStation := make(map[uuid.UUID][]HealthEvent)
	for rows.Next() {
		var ev HealthEvent
		if err := rows.Scan(&ev.ID, &ev.StationID, &ev.EventType, &ev.EventAt, &ev.DurationSeconds); err != nil {
			return nil, err
		}
		byStation[ev.StationID] = append(byStation[ev.StationID], ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	periodSec := float64(days * 24 * 60 * 60)
	result := make(map[uuid.UUID]*StationHealthSummary, len(byStation))
	for id, events := range byStation {
		s := computeSummary(id, events, days, periodSec)
		result[id] = s
	}
	return result, nil
}

// ─── Pure helpers (exported for testing) ─────────────────────────────────────

func computeSummary(stationID uuid.UUID, events []HealthEvent, days int, periodSec float64) *StationHealthSummary {
	var totalDowntime int
	var incidentCount int
	var lastIncidentAt *time.Time

	for _, ev := range events {
		if ev.EventType == "down" {
			incidentCount++
			t := ev.EventAt
			lastIncidentAt = &t
			if ev.DurationSeconds != nil {
				totalDowntime += *ev.DurationSeconds
			}
		}
	}

	uptimePct := 100.0
	if periodSec > 0 && totalDowntime > 0 {
		uptimePct = (1 - float64(totalDowntime)/periodSec) * 100
		if uptimePct < 0 {
			uptimePct = 0
		}
	}

	return &StationHealthSummary{
		StationID:      stationID,
		UptimePct:      uptimePct,
		IncidentCount:  incidentCount,
		LastIncidentAt: lastIncidentAt,
		DailySummary:   buildDailySummary(events, days),
	}
}

func buildDailySummary(events []HealthEvent, days int) []DailySummary {
	downtimeByDate := make(map[string]int)
	for _, ev := range events {
		if ev.EventType == "down" && ev.DurationSeconds != nil {
			date := ev.EventAt.UTC().Format("2006-01-02")
			downtimeByDate[date] += *ev.DurationSeconds
		}
	}

	now := time.Now().UTC()
	daily := make([]DailySummary, days)
	for i := 0; i < days; i++ {
		d := now.AddDate(0, 0, -(days - 1 - i))
		date := d.Format("2006-01-02")
		downtime := downtimeByDate[date]
		uptimePct := 100.0
		if downtime > 0 {
			uptimePct = (1 - float64(downtime)/86400.0) * 100
			if uptimePct < 0 {
				uptimePct = 0
			}
		}
		daily[i] = DailySummary{Date: date, UptimePct: uptimePct}
	}
	return daily
}

// IsNoRows reports whether err is the pgx "no rows" sentinel.
func isNoRows(err error) bool { return err == pgx.ErrNoRows }
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd workers && go test ./internal/catalog/... -run "TestComputeSummary|TestBuildDailySummary" -v
```
Expected: all 4 tests PASS.

- [ ] **Step 5: Verify the package compiles**

```bash
cd workers && go build ./internal/catalog/...
```
Expected: exits 0, no output.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/catalog/health_events.go workers/internal/catalog/health_events_test.go
git commit -m "feat(catalog): add HealthEvents repo with stream_health_events CRUD and summary computation"
```

---

## Task 2: Add `ListActive` to Stations catalog

**Files:**
- Modify: `workers/internal/catalog/stations.go` (append after `ListAll`)

- [ ] **Step 1: Write the failing test**

Append to `workers/internal/catalog/health_events_test.go`:

```go
// Note: TestListActive requires the DB. Set TEST_DATABASE_URL to run.
// import (
//   "os"
//   "github.com/jackc/pgx/v5/pgxpool"
// )
//
// func TestListActive_ReturnsOnlyActive(t *testing.T) {
//   url := os.Getenv("TEST_DATABASE_URL")
//   if url == "" { t.Skip("TEST_DATABASE_URL not set") }
//   pool, _ := pgxpool.New(context.Background(), url)
//   defer pool.Close()
//   repo := NewStations(pool)
//   stations, err := repo.ListActive(context.Background())
//   if err != nil { t.Fatal(err) }
//   for _, s := range stations {
//     if s.MonitoringStatus != "active" {
//       t.Errorf("got non-active station %s (status=%s)", s.Name, s.MonitoringStatus)
//     }
//   }
// }
```

(DB integration test, left as comment — run manually against dev DB.)

- [ ] **Step 2: Add `ListActive` to `workers/internal/catalog/stations.go`**

Append after the `ListAll` method at the bottom of the file:

```go
// ListActive returns all stations with monitoring_status = 'active', ordered by name.
// Used by the stream-health API to build the health dashboard list.
func (s *Stations) ListActive(ctx context.Context) ([]Station, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM stations
		WHERE monitoring_status = 'active'
		ORDER BY name`, stationSelectCols))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Station
	for rows.Next() {
		st, err := scanStationRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
```

- [ ] **Step 3: Verify compilation**

```bash
cd workers && go build ./internal/catalog/...
```
Expected: exits 0.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/catalog/stations.go
git commit -m "feat(catalog): add Stations.ListActive for health dashboard"
```

---

## Task 3: Worker stream state callbacks

**Files:**
- Modify: `workers/internal/ingestor/worker.go`

The worker's `Run` loop already resets state on each reconnect attempt. We add two optional callbacks: `OnStreamUp` (fired ~2s after audio starts flowing) and `OnStreamDown` (fired when `runPCMReader` exits due to stream failure, not clean shutdown).

- [ ] **Step 1: Add callback fields to `WorkerConfig` and `streamUpFired` to `Worker`**

In `worker.go`, replace the `WorkerConfig` struct definition (lines 23–49) with:

```go
type WorkerConfig struct {
	StationID           uuid.UUID
	StreamURL           string
	CommercialShortIDs  []int32
	CommercialFrames    map[int32]int
	MatchThreshold      int
	MinScoreCoverage    float64
	MinTemporalCoverage float64
	ConfirmTimeout      time.Duration
	AACBuffer           *ringbuffer.ByteRing
	// HeartbeatFn is called every ~30s while PCM audio is flowing.
	HeartbeatFn func()
	// OnStreamUp is called once per connect attempt, ~2s after audio starts flowing.
	OnStreamUp func()
	// OnStreamDown is called when the stream disconnects unexpectedly (not on ctx cancel).
	OnStreamDown func()
}
```

Replace the `Worker` struct (lines 63–68) with:

```go
type Worker struct {
	cfg           WorkerConfig
	store         *index.Store
	nc            *nats.Conn
	log           *zap.Logger
	streamUpFired bool // true after OnStreamUp fired for current connect attempt
}
```

- [ ] **Step 2: Reset `streamUpFired` and call `OnStreamDown` in `Run`**

In `Run`, replace lines 89–171 (the outer reconnect loop) with the version below. The only changes are: (a) reset `w.streamUpFired = false` at the top of each iteration, and (b) call `OnStreamDown` after `wg.Wait()` when the stream went down unexpectedly.

```go
func (w *Worker) Run(ctx context.Context) {
	backoff := 2 * time.Second
	const maxBackoff = 60 * time.Second

	stationIDStr := w.cfg.StationID.String()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		w.streamUpFired = false // reset for this connect attempt

		proc, err := StartFFmpeg(ctx, w.cfg.StreamURL, w.log)
		if err != nil {
			w.log.Error("ffmpeg start failed", zap.String("stationID", stationIDStr), zap.Error(err))
			if sleep(ctx, jitter(backoff)); ctx.Err() != nil {
				return
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		aacBuf := w.cfg.AACBuffer
		if aacBuf == nil {
			aacBuf = ringbuffer.NewByteRing(3000)
		}
		pcmBuf := ringbuffer.NewPCMRing(16000 * 35)

		machines := make(map[int32]*match.StateMachine, len(w.cfg.CommercialShortIDs))
		for _, id := range w.cfg.CommercialShortIDs {
			totalFrames := w.cfg.CommercialFrames[id]
			frameDur := time.Duration(float64(time.Second) * float64(totalFrames) * 2048 / 16000)
			cooldown := frameDur + 5*time.Second
			machines[id] = match.NewStateMachine(
				stationIDStr, id, totalFrames,
				w.cfg.MatchThreshold, w.cfg.MinTemporalCoverage,
				w.cfg.ConfirmTimeout, cooldown, w.log,
			)
		}

		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			defer wg.Done()
			w.runAACReader(proc.AACReader(), aacBuf)
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			w.runPCMReader(proc.PCMReader(), pcmBuf, machines, stationIDStr)
		}()

		wg.Wait()
		proc.Stop()

		if ctx.Err() != nil {
			return
		}

		// Stream disconnected unexpectedly. Only fire if we were ever up this attempt.
		if w.streamUpFired && w.cfg.OnStreamDown != nil {
			w.cfg.OnStreamDown()
		}

		w.log.Info("ffmpeg exited unexpectedly, reconnecting",
			zap.String("stationID", stationIDStr),
			zap.Duration("backoff", backoff),
		)
		if sleep(ctx, jitter(backoff)); ctx.Err() != nil {
			return
		}
		backoff = min(backoff*2, maxBackoff)
	}
}
```

- [ ] **Step 3: Call `OnStreamUp` on first tick in `runPCMReader`**

In `runPCMReader`, after `sampleCount -= tickEvery` (currently line 230), add the `OnStreamUp` call before the heartbeat block:

```go
		if sampleCount < tickEvery {
			continue
		}
		sampleCount -= tickEvery

		// Fire OnStreamUp once per connect attempt (first 2-second tick).
		if !w.streamUpFired {
			w.streamUpFired = true
			if w.cfg.OnStreamUp != nil {
				w.cfg.OnStreamUp()
			}
		}

		heartbeatTick++
		if heartbeatTick >= heartbeatEvery {
			heartbeatTick = 0
			if w.cfg.HeartbeatFn != nil {
				w.cfg.HeartbeatFn()
			}
		}
		// ... rest of the loop unchanged
```

- [ ] **Step 4: Verify compilation**

```bash
cd workers && go build ./internal/ingestor/...
```
Expected: exits 0.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/ingestor/worker.go
git commit -m "feat(ingestor): add OnStreamUp/OnStreamDown callbacks for health event tracking"
```

---

## Task 4: Supervisor — wire health event recording

**Files:**
- Modify: `workers/internal/supervisor/supervisor.go`

The supervisor tracks the last `down` event ID in `workerEntry` so that when `OnStreamUp` fires, it can retroactively fill `duration_seconds` on the down row. Startup recovery queries for an open `down` event before starting the worker.

- [ ] **Step 1: Add `healthEvents` to `Supervisor` and tracking fields to `workerEntry`**

Replace the `workerEntry` struct (lines 31–34):

```go
type workerEntry struct {
	worker     *ingestor.Worker
	cancel     context.CancelFunc
	lastDownID *int64
	lastDownAt *time.Time
}
```

Add `healthEvents *catalog.HealthEvents` to the `Supervisor` struct (after `commercials`):

```go
type Supervisor struct {
	db          *pgxpool.Pool
	store       *index.Store
	nc          *nats.Conn
	evidence    *evidence.Service
	campaigns   *catalog.Campaigns
	stations    *catalog.Stations
	commercials *catalog.Commercials
	healthEvents *catalog.HealthEvents  // NEW
	log         *zap.Logger

	mu      sync.Mutex
	workers map[uuid.UUID]*workerEntry
}
```

Update `New` to accept and store `healthEvents`:

```go
func New(
	db *pgxpool.Pool,
	store *index.Store,
	nc *nats.Conn,
	ev *evidence.Service,
	campaigns *catalog.Campaigns,
	stations *catalog.Stations,
	commercials *catalog.Commercials,
	healthEvents *catalog.HealthEvents,  // NEW
	log *zap.Logger,
) *Supervisor {
	return &Supervisor{
		db:           db,
		store:        store,
		nc:           nc,
		evidence:     ev,
		campaigns:    campaigns,
		stations:     stations,
		commercials:  commercials,
		healthEvents: healthEvents,  // NEW
		log:          log,
		workers:      make(map[uuid.UUID]*workerEntry),
	}
}
```

- [ ] **Step 2: Add health event callbacks in `startStationWorker`**

In `startStationWorker`, replace the section from `capturedStationID := stationID` through `cfg := ingestor.WorkerConfig{...}` (currently lines 190–222) with:

```go
	capturedStationID := stationID

	// ── Startup recovery: find open 'down' event from a previous crash ──────
	entry := &workerEntry{cancel: cancel}
	if ev, err := s.healthEvents.GetLastOpenDown(context.Background(), capturedStationID); err == nil {
		entry.lastDownID = &ev.ID
		entry.lastDownAt = &ev.EventAt
		s.log.Info("supervisor: startup recovery — found open down event",
			zap.String("station_id", capturedStationID.String()),
			zap.Time("event_at", ev.EventAt),
		)
	}

	// Store entry in map NOW so callbacks can find it (worker starts below).
	s.mu.Lock()
	s.workers[capturedStationID] = entry
	s.mu.Unlock()

	// ── Heartbeat ────────────────────────────────────────────────────────────
	heartbeatFn := func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.stations.UpdateHealthCheck(ctx, capturedStationID); err != nil {
				s.log.Warn("supervisor: heartbeat update failed",
					zap.String("station_id", capturedStationID.String()),
					zap.Error(err),
				)
			}
		}()
	}

	// ── Stream up callback ────────────────────────────────────────────────────
	onStreamUp := func() {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			s.mu.Lock()
			e, ok := s.workers[capturedStationID]
			downID := e.lastDownID
			downAt := e.lastDownAt
			s.mu.Unlock()

			if ok && downID != nil && downAt != nil {
				dur := int(time.Since(*downAt).Seconds())
				if err := s.healthEvents.UpdateDownDuration(bgCtx, *downID, *downAt, dur); err != nil {
					s.log.Warn("supervisor: update down duration failed",
						zap.String("station_id", capturedStationID.String()),
						zap.Error(err),
					)
				}
			}

			if err := s.healthEvents.RecordUp(bgCtx, capturedStationID); err != nil {
				s.log.Warn("supervisor: record up failed",
					zap.String("station_id", capturedStationID.String()),
					zap.Error(err),
				)
			}

			// Clear the tracked down event — outage is resolved.
			s.mu.Lock()
			if e, ok := s.workers[capturedStationID]; ok {
				e.lastDownID = nil
				e.lastDownAt = nil
			}
			s.mu.Unlock()
		}()
	}

	// ── Stream down callback ─────────────────────────────────────────────────
	onStreamDown := func() {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			id, at, err := s.healthEvents.RecordDown(bgCtx, capturedStationID)
			if err != nil {
				s.log.Warn("supervisor: record down failed",
					zap.String("station_id", capturedStationID.String()),
					zap.Error(err),
				)
				return
			}

			s.mu.Lock()
			if e, ok := s.workers[capturedStationID]; ok {
				e.lastDownID = &id
				e.lastDownAt = &at
			}
			s.mu.Unlock()
		}()
	}

	cfg := ingestor.WorkerConfig{
		StationID:          station.ID,
		StreamURL:          station.StreamURL,
		CommercialShortIDs: shortIDs,
		CommercialFrames:   frames,
		MatchThreshold:     3,
		MinScoreCoverage:   0.05,
		MinTemporalCoverage: 0.15,
		ConfirmTimeout:     30 * time.Second,
		AACBuffer:          aacBuf,
		HeartbeatFn:        heartbeatFn,
		OnStreamUp:         onStreamUp,   // NEW
		OnStreamDown:       onStreamDown, // NEW
	}
```

- [ ] **Step 3: Update the worker start / store section**

Replace lines 223–233 (create worker, register evidence, start goroutine, store entry):

```go
	w := ingestor.NewWorker(cfg, s.store, s.nc, s.log)
	entry.worker = w

	// Register ByteRing with evidence service before worker starts.
	s.evidence.Register(stationID, aacBuf)

	// Start goroutine.
	go w.Run(workerCtx)

	// entry was already stored in the map above (before callbacks were set).

	s.log.Info("supervisor: worker started",
		zap.String("station_id", stationID.String()),
		zap.String("stream_url", station.StreamURL),
		zap.Int("commercials", len(shortIDs)),
	)
	return nil
```

Also remove the old `s.mu.Lock() / s.workers[stationID] = &workerEntry{...} / s.mu.Unlock()` block that was previously at the end — entry is now stored before the worker starts.

- [ ] **Step 4: Verify compilation**

```bash
cd workers && go build ./internal/supervisor/...
```
Expected: compile error about `supervisor.New` signature mismatch with main.go — that is fixed in Task 5.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/supervisor/supervisor.go
git commit -m "feat(supervisor): wire OnStreamUp/OnStreamDown to stream_health_events with startup recovery"
```

---

## Task 5: StreamHealth API handler, router, and main wiring

**Files:**
- Create: `workers/internal/api/handlers/stream_health.go`
- Modify: `workers/internal/api/router.go`
- Modify: `workers/cmd/api/main.go`

- [ ] **Step 1: Create the handler**

```go
// workers/internal/api/handlers/stream_health.go
package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"radiocheck/internal/catalog"
)

type StreamHealthHandler struct {
	HealthEvents *catalog.HealthEvents
	Stations     *catalog.Stations
}

// List returns health summaries for all stations with monitoring_status = 'active'.
// GET /v1/internal/stream-health?days=7
func (h *StreamHealthHandler) List(w http.ResponseWriter, r *http.Request) {
	days := parseDays(r, 7)
	ctx := r.Context()

	stations, err := h.Stations.ListActive(ctx)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	summaries, err := h.HealthEvents.GetSummariesForActive(ctx, days)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	type row struct {
		catalog.Station
		UptimePct      float64                  `json:"uptime_pct"`
		IncidentCount  int                      `json:"incident_count"`
		LastIncidentAt *time.Time               `json:"last_incident_at,omitempty"`
		DailySummary   []catalog.DailySummary   `json:"daily_summary"`
	}

	result := make([]row, 0, len(stations))
	for _, st := range stations {
		r := row{Station: st, UptimePct: 100.0, DailySummary: make([]catalog.DailySummary, 0)}
		if s, ok := summaries[st.ID]; ok {
			r.UptimePct = s.UptimePct
			r.IncidentCount = s.IncidentCount
			r.LastIncidentAt = s.LastIncidentAt
			r.DailySummary = s.DailySummary
		}
		result = append(result, r)
	}

	writeJSON(w, http.StatusOK, result)
}

// Detail returns raw events for a single station.
// GET /v1/internal/stream-health/{stationId}?days=7
func (h *StreamHealthHandler) Detail(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "stationId")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid station id", http.StatusBadRequest)
		return
	}

	days := parseDays(r, 7)
	ctx := r.Context()

	events, err := h.HealthEvents.GetForStation(ctx, id, days)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []catalog.HealthEvent{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"station_id":   id,
		"period_start": time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339),
		"period_end":   time.Now().UTC().Format(time.RFC3339),
		"events":       events,
	})
}

func parseDays(r *http.Request, defaultVal int) int {
	d := r.URL.Query().Get("days")
	if d == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(d)
	if err != nil || n < 1 || n > 90 {
		return defaultVal
	}
	return n
}
```

- [ ] **Step 2: Register routes in `workers/internal/api/router.go`**

Add `StreamHealth *handlers.StreamHealthHandler` to the `Deps` struct (after `Health`):

```go
type Deps struct {
	Stations     *handlers.StationsHandler
	Clients      *handlers.ClientsHandler
	Campaigns    *handlers.CampaignsHandler
	Commercials  *handlers.CommercialsHandler
	Detections   *handlers.DetectionsHandler
	Health       *handlers.HealthHandler
	StreamHealth *handlers.StreamHealthHandler  // NEW
}
```

In the `New` function, inside the `r.Route("/v1/internal", ...)` block, add after the existing station routes:

```go
		r.Route("/stream-health", func(r chi.Router) {
			r.Get("/", d.StreamHealth.List)
			r.Get("/{stationId}", d.StreamHealth.Detail)
		})
```

- [ ] **Step 3: Wire in `workers/cmd/api/main.go`**

After `detections := catalog.NewDetections(pool)`, add:

```go
healthEvents := catalog.NewHealthEvents(pool)
```

Update the `supervisor.New(...)` call to pass `healthEvents` as the new 8th argument (before `log`):

```go
sup := supervisor.New(pool, store, nc, evidenceSvc, campaigns, stations, commercials, healthEvents, log)
```

Add `StreamHealth` to the `deps` struct:

```go
deps := api.Deps{
	Stations:     &handlers.StationsHandler{Repo: stations},
	Clients:      &handlers.ClientsHandler{Repo: clients},
	Campaigns:    campaignsHandler,
	Commercials:  &handlers.CommercialsHandler{Repo: commercials, NATS: nc, MastersPath: cfg.MastersPath, Supervisor: sup},
	Detections:   &handlers.DetectionsHandler{Repo: detections, Storage: s3Client},
	Health:       &handlers.HealthHandler{DB: pool, NATS: nc},
	StreamHealth: &handlers.StreamHealthHandler{HealthEvents: healthEvents, Stations: stations},  // NEW
}
```

- [ ] **Step 4: Verify full backend build**

```bash
cd workers && go build ./...
```
Expected: exits 0.

- [ ] **Step 5: Smoke test the endpoints**

Start the server (or use docker-compose), then:

```bash
curl -s http://localhost:8080/v1/internal/stream-health | jq '.[0]'
# Expected: station object with uptime_pct, incident_count, daily_summary array

curl -s "http://localhost:8080/v1/internal/stream-health/<a-real-station-uuid>?days=7" | jq '.events'
# Expected: [] (empty, no events recorded yet — that's correct at this stage)
```

- [ ] **Step 6: Commit**

```bash
git add workers/internal/api/handlers/stream_health.go workers/internal/api/router.go workers/cmd/api/main.go
git commit -m "feat(api): add /stream-health list and detail endpoints"
```

---

## Task 6: Frontend API hooks

**Files:**
- Modify: `frontend/src/api/hooks.js`

- [ ] **Step 1: Add the two new hooks**

Append to `frontend/src/api/hooks.js`:

```js
export function useStreamHealth(params = {}) {
  return useQuery({
    queryKey: ['stream-health', params],
    queryFn: () => api.get('/stream-health', { params }).then(r => r.data),
    refetchInterval: 120_000,
  })
}

export function useStationHealthEvents(stationId, days = 7) {
  return useQuery({
    queryKey: ['stream-health-events', stationId, days],
    queryFn: () =>
      api.get(`/stream-health/${stationId}`, { params: { days } }).then(r => r.data),
    enabled: !!stationId,
  })
}
```

- [ ] **Step 2: Verify no import changes needed**

`useQuery` and `api` are already imported at the top of `hooks.js`. No changes needed.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "feat(hooks): add useStreamHealth and useStationHealthEvents"
```

---

## Task 7: HealthTimeline component

**Files:**
- Create: `frontend/src/components/HealthTimeline.jsx`

The timeline renders segments (up/down/unknown) as colored `div` blocks. Drag-to-zoom sets a `[viewStart, viewEnd]` window; block resolution adapts automatically.

- [ ] **Step 1: Create the component**

```jsx
// frontend/src/components/HealthTimeline.jsx
import { useState, useRef, useCallback, useMemo } from 'react'

// ── Resolution tiers ─────────────────────────────────────────────────────────
// windowMs → block size in ms
function blockMsForWindow(windowMs) {
  if (windowMs > 2 * 24 * 60 * 60 * 1000) return 60 * 60 * 1000        // >2d → 1h
  if (windowMs > 6 * 60 * 60 * 1000)       return 15 * 60 * 1000        // >6h → 15m
  return 60 * 1000                                                         // ≤6h → 1m
}

// ── Convert raw events into contiguous segments ───────────────────────────────
// Returns [{start, end, type: 'up'|'down'|'unknown'}]
function eventsToSegments(events, periodStart, periodEnd) {
  if (!events || events.length === 0) {
    return [{ start: periodStart, end: periodEnd, type: 'unknown' }]
  }

  const sorted = [...events].sort((a, b) => new Date(a.event_at) - new Date(b.event_at))
  const segments = []

  let cursor = periodStart

  for (const ev of sorted) {
    const evAt = new Date(ev.event_at)
    if (evAt <= cursor) continue

    // Gap before this event — unknown state
    if (evAt > cursor) {
      segments.push({ start: cursor, end: evAt, type: 'unknown' })
    }

    if (ev.event_type === 'down') {
      const dur = ev.duration_seconds != null ? ev.duration_seconds * 1000 : 0
      const end = dur > 0 ? new Date(evAt.getTime() + dur) : evAt
      segments.push({ start: evAt, end: end > periodEnd ? periodEnd : end, type: 'down' })
      cursor = end > periodEnd ? periodEnd : end
    } else {
      cursor = evAt
    }
  }

  if (cursor < periodEnd) {
    segments.push({ start: cursor, end: periodEnd, type: 'up' })
  }

  return segments
}

// ── Tooltip content ───────────────────────────────────────────────────────────
function fmtDuration(ms) {
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}min ${s % 60}s`
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}min`
}

function fmtTimestamp(d) {
  return d.toLocaleString('pt-BR', {
    day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

// ── Main component ────────────────────────────────────────────────────────────
export default function HealthTimeline({ events, periodStart, periodEnd, onZoomToEvent }) {
  const [viewStart, setViewStart] = useState(periodStart)
  const [viewEnd, setViewEnd]     = useState(periodEnd)
  const [tooltip, setTooltip]     = useState(null) // { x, y, content }
  const [drag, setDrag]           = useState(null)  // { startX, startMs, currentX }
  const containerRef = useRef(null)

  // Reset view when period changes (e.g., user switches 1d/7d)
  const resetView = useCallback(() => {
    setViewStart(periodStart)
    setViewEnd(periodEnd)
  }, [periodStart, periodEnd])

  const windowMs  = viewEnd - viewStart
  const blockMs   = blockMsForWindow(windowMs)
  const isZoomed  = viewStart !== periodStart || viewEnd !== periodEnd

  const segments = useMemo(
    () => eventsToSegments(events, new Date(periodStart), new Date(periodEnd)),
    [events, periodStart, periodEnd],
  )

  // ── Drag-to-zoom ─────────────────────────────────────────────────────────
  function xToMs(x) {
    const rect = containerRef.current.getBoundingClientRect()
    const ratio = (x - rect.left) / rect.width
    return viewStart + ratio * windowMs
  }

  function onMouseDown(e) {
    setDrag({ startX: e.clientX, startMs: xToMs(e.clientX), currentX: e.clientX })
  }

  function onMouseMove(e) {
    if (!drag) return
    setDrag(d => ({ ...d, currentX: e.clientX }))
  }

  function onMouseUp(e) {
    if (!drag) return
    const endMs = xToMs(e.clientX)
    const lo = Math.min(drag.startMs, endMs)
    const hi = Math.max(drag.startMs, endMs)
    const minWindow = 2 * 60 * 1000 // don't zoom to less than 2 min
    if (hi - lo > minWindow) {
      setViewStart(lo)
      setViewEnd(hi)
    }
    setDrag(null)
  }

  function onMouseLeave() {
    setDrag(null)
    setTooltip(null)
  }

  // ── Drag selection overlay ───────────────────────────────────────────────
  let selectionStyle = null
  if (drag && containerRef.current) {
    const rect  = containerRef.current.getBoundingClientRect()
    const x1    = Math.min(drag.startX, drag.currentX) - rect.left
    const x2    = Math.max(drag.startX, drag.currentX) - rect.left
    selectionStyle = {
      position: 'absolute', top: 0, bottom: 0,
      left: Math.max(0, x1), width: Math.min(x2, rect.width) - Math.max(0, x1),
      background: 'rgba(232, 30, 117, 0.12)',
      border: '1px solid rgba(232, 30, 117, 0.4)',
      borderRadius: 2, pointerEvents: 'none',
    }
  }

  // ── Render segments as blocks ─────────────────────────────────────────────
  const visibleBlocks = []
  const startMs  = viewStart
  const endMs    = viewEnd

  for (const seg of segments) {
    const segStart = Math.max(seg.start.getTime(), startMs)
    const segEnd   = Math.min(seg.end.getTime(), endMs)
    if (segEnd <= segStart) continue

    const leftPct  = ((segStart - startMs) / windowMs) * 100
    const widthPct = ((segEnd - segStart) / windowMs) * 100

    visibleBlocks.push({
      key: `${seg.type}-${segStart}`,
      type: seg.type,
      leftPct,
      widthPct,
      segStart: new Date(segStart),
      segEnd:   new Date(segEnd),
      durMs:    segEnd - segStart,
    })
  }

  // ── X-axis ticks ──────────────────────────────────────────────────────────
  const ticks = []
  const tickMs = blockMs * Math.ceil((windowMs / blockMs) / 8) // ~8 ticks max
  let t = Math.ceil(viewStart / tickMs) * tickMs
  while (t <= viewEnd) {
    ticks.push({ ms: t, leftPct: ((t - viewStart) / windowMs) * 100 })
    t += tickMs
  }

  return (
    <div className="htl-wrap">
      {/* Timeline bar */}
      <div
        ref={containerRef}
        className="htl-bar"
        onMouseDown={onMouseDown}
        onMouseMove={onMouseMove}
        onMouseUp={onMouseUp}
        onMouseLeave={onMouseLeave}
        style={{ position: 'relative', userSelect: 'none' }}
      >
        {visibleBlocks.map(b => (
          <div
            key={b.key}
            className={`htl-seg htl-seg--${b.type}`}
            style={{ left: `${b.leftPct}%`, width: `${b.widthPct}%` }}
            onMouseEnter={e => {
              const label = b.type === 'down'
                ? `Offline · ${fmtTimestamp(b.segStart)} – ${fmtTimestamp(b.segEnd)} · ${fmtDuration(b.durMs)}`
                : b.type === 'up'
                ? `Online · ${fmtTimestamp(b.segStart)} · ${fmtDuration(b.durMs)}`
                : `Sem dados · ${fmtTimestamp(b.segStart)}`
              setTooltip({ x: e.clientX, y: e.clientY, content: label })
            }}
            onMouseLeave={() => setTooltip(null)}
          />
        ))}
        {selectionStyle && <div style={selectionStyle} />}
      </div>

      {/* X-axis ticks */}
      <div className="htl-axis">
        {ticks.map(tick => (
          <div key={tick.ms} className="htl-tick" style={{ left: `${tick.leftPct}%` }}>
            <span className="htl-tick-label">
              {new Date(tick.ms).toLocaleString('pt-BR', {
                day: '2-digit', month: '2-digit',
                hour: '2-digit', minute: '2-digit',
              })}
            </span>
          </div>
        ))}
      </div>

      {/* Controls */}
      <div className="htl-controls">
        {isZoomed && (
          <button className="htl-reset-btn" onClick={resetView} type="button">
            Resetar zoom
          </button>
        )}
        <span className="htl-resolution">
          {blockMs >= 3600000 ? '1h/bloco' : blockMs >= 900000 ? '15min/bloco' : '1min/bloco'}
        </span>
      </div>

      {/* Tooltip */}
      {tooltip && (
        <div
          className="htl-tooltip"
          style={{ left: tooltip.x + 12, top: tooltip.y - 36, position: 'fixed' }}
        >
          {tooltip.content}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Add styles to `frontend/src/index.css`**

Append to `frontend/src/index.css`:

```css
/* ── HealthTimeline ───────────────────────────────────────────────── */
.htl-wrap {
  display: flex;
  flex-direction: column;
  gap: 4px;
  position: relative;
}

.htl-bar {
  height: 36px;
  border-radius: var(--radius-md);
  background: var(--c-surface-2);
  overflow: hidden;
  cursor: crosshair;
  position: relative;
  border: 1px solid var(--c-border);
}

.htl-seg {
  position: absolute;
  top: 0;
  bottom: 0;
  transition: filter 80ms ease;
}
.htl-seg:hover { filter: brightness(0.92); }

.htl-seg--up      { background: oklch(0.72 0.18 145); }
.htl-seg--down    { background: oklch(0.62 0.22 25); }
.htl-seg--unknown { background: var(--c-border); }

.htl-axis {
  position: relative;
  height: 18px;
}

.htl-tick {
  position: absolute;
  top: 0;
  transform: translateX(-50%);
  display: flex;
  flex-direction: column;
  align-items: center;
}

.htl-tick::before {
  content: '';
  width: 1px;
  height: 4px;
  background: var(--c-border);
  display: block;
}

.htl-tick-label {
  font-size: 10px;
  color: var(--c-text-3);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.htl-controls {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 20px;
}

.htl-reset-btn {
  font-size: 11px;
  font-weight: 600;
  color: var(--c-action);
  background: none;
  border: none;
  cursor: pointer;
  padding: 0;
  font-family: var(--font-body);
}
.htl-reset-btn:hover { text-decoration: underline; }

.htl-resolution {
  font-size: 11px;
  color: var(--c-text-3);
  margin-left: auto;
}

.htl-tooltip {
  background: var(--c-text);
  color: #fff;
  font-size: 11px;
  padding: 5px 9px;
  border-radius: var(--radius-sm);
  pointer-events: none;
  z-index: 9999;
  max-width: 360px;
  white-space: nowrap;
  box-shadow: var(--shadow-md);
}

/* ── Health mini bar (7-day row indicator) ────────────────────────── */
.health-mini-bar {
  display: flex;
  gap: 2px;
  height: 14px;
  align-items: stretch;
  min-width: 70px;
}

.health-mini-day {
  flex: 1;
  border-radius: 2px;
  cursor: default;
}
.health-mini-day--ok    { background: oklch(0.72 0.18 145); }
.health-mini-day--warn  { background: oklch(0.62 0.22 25); }
.health-mini-day--empty { background: var(--c-border); }

/* ── Health Drawer ────────────────────────────────────────────────── */
.health-drawer-overlay {
  position: fixed;
  inset: 0;
  z-index: 300;
  pointer-events: none;
}

.health-drawer {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  width: 62%;
  max-width: 900px;
  background: var(--c-surface);
  border-left: 1px solid var(--c-border);
  box-shadow: var(--shadow-lg);
  display: flex;
  flex-direction: column;
  z-index: 301;
  pointer-events: all;
  overflow: hidden;
  animation: drawer-slide-in 200ms cubic-bezier(0.4, 0, 0.2, 1);
}

@keyframes drawer-slide-in {
  from { transform: translateX(100%); }
  to   { transform: translateX(0); }
}

.health-drawer-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px 20px;
  border-bottom: 1px solid var(--c-border);
  flex-shrink: 0;
}

.health-drawer-station-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.health-drawer-station-name {
  font-family: var(--font-heading);
  font-size: 16px;
  font-weight: 700;
  color: var(--c-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.health-drawer-station-meta {
  font-size: 12px;
  color: var(--c-text-3);
}

.health-drawer-status {
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
}
.health-drawer-status--up   { color: var(--c-success); }
.health-drawer-status--down { color: var(--c-danger); }

.health-drawer-close {
  width: 30px; height: 30px;
  border: 1px solid var(--c-border);
  border-radius: var(--radius-md);
  background: transparent;
  cursor: pointer;
  display: flex; align-items: center; justify-content: center;
  color: var(--c-text-3);
  transition: background 120ms, color 120ms;
  flex-shrink: 0;
}
.health-drawer-close:hover { background: var(--c-surface-2); color: var(--c-text); }

.health-drawer-body {
  flex: 1;
  overflow-y: auto;
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.health-drawer-period-tabs {
  display: flex;
  gap: 4px;
  background: var(--c-surface-2);
  border-radius: var(--radius-md);
  padding: 3px;
  align-self: flex-start;
}

.health-drawer-period-tab {
  padding: 4px 14px;
  border-radius: calc(var(--radius-md) - 2px);
  border: none;
  background: transparent;
  font-family: var(--font-body);
  font-size: 13px;
  font-weight: 500;
  color: var(--c-text-2);
  cursor: pointer;
  transition: background 120ms, color 120ms;
}
.health-drawer-period-tab:hover:not(.active) { background: var(--c-border); color: var(--c-text); }
.health-drawer-period-tab.active {
  background: var(--c-action);
  color: #fff;
  font-weight: 600;
}

.health-stats-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
}

.health-stat {
  background: var(--c-surface-2);
  border: 1px solid var(--c-border);
  border-radius: var(--radius-lg);
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.health-stat-label {
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--c-text-3);
}

.health-stat-value {
  font-size: 20px;
  font-weight: 700;
  font-family: var(--font-heading);
  color: var(--c-text);
  font-variant-numeric: tabular-nums;
}

.health-stat-value--ok   { color: var(--c-success); }
.health-stat-value--warn { color: var(--c-warning); }
.health-stat-value--bad  { color: var(--c-danger); }

.health-events-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.health-events-label {
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--c-text-3);
}

.health-events-list {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--c-border);
  border-radius: var(--radius-lg);
  overflow: hidden;
  background: var(--c-surface);
}

.health-event-row {
  display: grid;
  grid-template-columns: 160px 80px 1fr;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--c-border);
  font-size: 13px;
  transition: background 100ms;
}
.health-event-row:last-child { border-bottom: none; }
.health-event-row:hover { background: var(--c-surface-2); }

.health-event-time  { font-variant-numeric: tabular-nums; color: var(--c-text); font-weight: 600; }
.health-event-dur   { color: var(--c-danger); font-weight: 600; }
.health-event-empty { text-align: center; padding: 24px; color: var(--c-text-3); font-size: 13px; }

/* ── Health page list ─────────────────────────────────────────────── */
.health-page-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 20px;
  flex-wrap: wrap;
}

.health-page-title {
  flex: 1;
}

.health-page-count {
  font-size: 13px;
  color: var(--c-text-3);
  margin-top: 2px;
}

.health-filters {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 16px;
  flex-wrap: wrap;
}

.health-list {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--c-border);
  border-radius: var(--radius-lg);
  overflow: hidden;
  background: var(--c-border);
  gap: 1px;
  margin-bottom: 20px;
}

.health-row {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 12px 16px;
  background: var(--c-surface);
  cursor: pointer;
  transition: background 100ms;
}
.health-row:hover { background: var(--c-surface-2); }
.health-row.selected { background: var(--c-action-light); }

.health-row-main {
  flex: 0 0 220px;
  min-width: 0;
}

.health-row-name {
  font-weight: 600;
  font-size: 14px;
  color: var(--c-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.health-row-sub {
  font-size: 11.5px;
  color: var(--c-text-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.health-row-bar { flex: 1; min-width: 70px; max-width: 120px; }

.health-row-uptime {
  width: 70px;
  text-align: right;
  font-size: 13px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  font-family: var(--font-heading);
  color: var(--c-text);
}
.health-row-uptime--ok  { color: var(--c-success); }
.health-row-uptime--bad { color: var(--c-danger); }

.health-row-incident {
  width: 120px;
  font-size: 11.5px;
  color: var(--c-text-3);
  text-align: right;
  white-space: nowrap;
}

@media (max-width: 900px) {
  .health-drawer { width: 100%; }
  .health-stats-grid { grid-template-columns: repeat(2, 1fr); }
}
```

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/HealthTimeline.jsx frontend/src/index.css
git commit -m "feat(ui): add HealthTimeline component with drag-to-zoom and adaptive resolution"
```

---

## Task 8: MonitoringPage rewrite

**Files:**
- Replace: `frontend/src/pages/MonitoringPage.jsx`

This is a complete replacement of the current file.

- [ ] **Step 1: Write the new MonitoringPage**

```jsx
// frontend/src/pages/MonitoringPage.jsx
import { useState, useMemo } from 'react'
import { useStreamHealth, useStationHealthEvents } from '../api/hooks'
import StationAvatar from '../components/StationAvatar'
import HealthTimeline from '../components/HealthTimeline'

// ── Mini 7-day bar ────────────────────────────────────────────────────────────
function MiniHealthBar({ dailySummary }) {
  if (!dailySummary || dailySummary.length === 0) {
    return (
      <div className="health-mini-bar">
        {Array.from({ length: 7 }).map((_, i) => (
          <div key={i} className="health-mini-day health-mini-day--empty" title="Sem dados" />
        ))}
      </div>
    )
  }
  return (
    <div className="health-mini-bar">
      {dailySummary.map(d => {
        const cls = d.uptime_pct === 100
          ? 'health-mini-day--ok'
          : d.uptime_pct < 95
          ? 'health-mini-day--warn'
          : 'health-mini-day--ok'
        const label = `${d.date}: ${d.uptime_pct.toFixed(1)}% uptime`
        return <div key={d.date} className={`health-mini-day ${cls}`} title={label} />
      })}
    </div>
  )
}

// ── Status dot ────────────────────────────────────────────────────────────────
function StatusDot({ status }) {
  const cls =
    status === 'active'      ? 'dot-active'      :
    status === 'error'       ? 'dot-error'        :
    status === 'calibrating' ? 'dot-calibrating'  : 'dot-paused'
  return <span className={`station-status-dot ${cls}`} />
}

// ── Relative time ─────────────────────────────────────────────────────────────
function relativeTime(iso) {
  if (!iso) return null
  const diff = Date.now() - new Date(iso)
  const s = Math.floor(diff / 1000)
  if (s < 60)   return `há ${s}s`
  if (s < 3600) return `há ${Math.floor(s / 60)}min`
  if (s < 86400) return `há ${Math.floor(s / 3600)}h`
  return `há ${Math.floor(s / 86400)}d`
}

// ── Format duration in ms ────────────────────────────────────────────────────
function fmtDuration(ms) {
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}min ${s % 60}s`
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}min`
}

// ── Drawer ────────────────────────────────────────────────────────────────────
function HealthDrawer({ station, onClose }) {
  const [days, setDays] = useState(7)
  const { data, isLoading } = useStationHealthEvents(station?.id, days)

  if (!station) return null

  const events     = data?.events ?? []
  const periodEnd  = data?.period_end   ? new Date(data.period_end).getTime()   : Date.now()
  const periodStart = data?.period_start ? new Date(data.period_start).getTime() : periodEnd - days * 86400000

  // Compute summary stats from events
  const stats = useMemo(() => {
    const downs = events.filter(e => e.event_type === 'down')
    const totalDownMs = downs.reduce((acc, e) => acc + (e.duration_seconds ?? 0) * 1000, 0)
    const periodMs = periodEnd - periodStart
    const uptime = periodMs > 0 ? ((1 - totalDownMs / periodMs) * 100) : 100
    const longest = downs.reduce((max, e) => Math.max(max, (e.duration_seconds ?? 0) * 1000), 0)
    const avg = downs.length > 0 ? totalDownMs / downs.length : 0
    return { uptime, incidents: downs.length, longestMs: longest, avgMs: avg }
  }, [events, periodStart, periodEnd])

  const uptimeCls = stats.uptime >= 99 ? 'health-stat-value--ok'
    : stats.uptime >= 95 ? 'health-stat-value--warn'
    : 'health-stat-value--bad'

  const currentStatus = station.monitoring_status === 'active' ? 'up' : 'down'

  return (
    <>
      <div className="health-drawer-overlay" onClick={onClose} />
      <div className="health-drawer" role="dialog" aria-label={`Saúde: ${station.name}`}>
        {/* Header */}
        <div className="health-drawer-header">
          <StationAvatar station={station} size={40} />
          <div className="health-drawer-station-info">
            <div className="health-drawer-station-name">{station.name}</div>
            <div className="health-drawer-station-meta">
              {station.band}{station.frequency_mhz ? ` · ${station.frequency_mhz} MHz` : ''}
              {station.city ? ` · ${station.city}` : ''}
              {station.state ? `/${station.state}` : ''}
            </div>
          </div>
          <span className={`health-drawer-status health-drawer-status--${currentStatus}`}>
            {currentStatus === 'up' ? '● Online' : '● Offline'}
          </span>
          <button className="health-drawer-close" onClick={onClose} aria-label="Fechar">✕</button>
        </div>

        {/* Body */}
        <div className="health-drawer-body">
          {/* Period selector */}
          <div className="health-drawer-period-tabs">
            {[1, 7].map(d => (
              <button
                key={d}
                type="button"
                className={`health-drawer-period-tab${days === d ? ' active' : ''}`}
                onClick={() => setDays(d)}
              >
                {d === 1 ? '1 dia' : '7 dias'}
              </button>
            ))}
          </div>

          {/* Timeline */}
          {isLoading ? (
            <div className="skeleton" style={{ height: 36, borderRadius: 'var(--radius-md)' }} />
          ) : (
            <HealthTimeline
              events={events}
              periodStart={periodStart}
              periodEnd={periodEnd}
            />
          )}

          {/* Stats */}
          <div className="health-stats-grid">
            <div className="health-stat">
              <span className="health-stat-label">Uptime</span>
              <span className={`health-stat-value ${uptimeCls}`}>{stats.uptime.toFixed(1)}%</span>
            </div>
            <div className="health-stat">
              <span className="health-stat-label">Quedas</span>
              <span className="health-stat-value">{stats.incidents}</span>
            </div>
            <div className="health-stat">
              <span className="health-stat-label">Maior queda</span>
              <span className="health-stat-value" style={{ fontSize: 15 }}>
                {stats.longestMs > 0 ? fmtDuration(stats.longestMs) : '—'}
              </span>
            </div>
            <div className="health-stat">
              <span className="health-stat-label">Média por queda</span>
              <span className="health-stat-value" style={{ fontSize: 15 }}>
                {stats.avgMs > 0 ? fmtDuration(stats.avgMs) : '—'}
              </span>
            </div>
          </div>

          {/* Event list */}
          <div className="health-events-section">
            <div className="health-events-label">Histórico de quedas</div>
            <div className="health-events-list">
              {events.filter(e => e.event_type === 'down').length === 0 ? (
                <div className="health-event-empty">Nenhuma queda no período</div>
              ) : (
                [...events]
                  .filter(e => e.event_type === 'down')
                  .sort((a, b) => new Date(b.event_at) - new Date(a.event_at))
                  .map(ev => (
                    <div key={ev.id} className="health-event-row">
                      <span className="health-event-time">
                        {new Date(ev.event_at).toLocaleString('pt-BR', {
                          day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit',
                        })}
                      </span>
                      <span className="health-event-dur">
                        {ev.duration_seconds != null ? fmtDuration(ev.duration_seconds * 1000) : '—'}
                      </span>
                      <span className="text-muted">offline</span>
                    </div>
                  ))
              )}
            </div>
          </div>
        </div>
      </div>
    </>
  )
}

// ── Main page ─────────────────────────────────────────────────────────────────
const HEALTH_FILTERS = ['Todas', 'Com falha', 'Estável']

export default function MonitoringPage() {
  const [search, setSearch]       = useState('')
  const [band, setBand]           = useState('Todas')
  const [healthFilter, setHealth] = useState('Todas')
  const [page, setPage]           = useState(1)
  const [selectedId, setSelectedId] = useState(null)
  const PER_PAGE = 25

  const { data: stations = [], isLoading } = useStreamHealth()

  const filtered = useMemo(() => {
    let list = stations
    if (search) {
      const q = search.toLowerCase()
      list = list.filter(s =>
        s.name.toLowerCase().includes(q) ||
        (s.city ?? '').toLowerCase().includes(q) ||
        (s.state ?? '').toLowerCase().includes(q)
      )
    }
    if (band !== 'Todas') {
      list = list.filter(s => s.band === band)
    }
    if (healthFilter === 'Com falha') {
      list = list.filter(s => s.uptime_pct < 99.9 || s.monitoring_status === 'error')
    } else if (healthFilter === 'Estável') {
      list = list.filter(s => s.uptime_pct >= 99.9 && s.monitoring_status !== 'error')
    }
    // Errors first, then by uptime asc, then name
    return [...list].sort((a, b) => {
      if (a.monitoring_status === 'error' && b.monitoring_status !== 'error') return -1
      if (b.monitoring_status === 'error' && a.monitoring_status !== 'error') return 1
      if (a.uptime_pct !== b.uptime_pct) return a.uptime_pct - b.uptime_pct
      return a.name.localeCompare(b.name)
    })
  }, [stations, search, band, healthFilter])

  const totalPages = Math.max(1, Math.ceil(filtered.length / PER_PAGE))
  const currentPage = Math.min(page, totalPages)
  const pageItems = filtered.slice((currentPage - 1) * PER_PAGE, currentPage * PER_PAGE)

  const selectedStation = stations.find(s => s.id === selectedId) ?? null

  return (
    <div>
      {/* Page header */}
      <div className="health-page-header">
        <div className="health-page-title">
          <h2 style={{ margin: 0 }}>Saúde do Stream</h2>
          {!isLoading && (
            <div className="health-page-count">
              {stations.length} emissora{stations.length !== 1 ? 's' : ''} em campanha ativa
            </div>
          )}
        </div>
      </div>

      {/* Filters */}
      <div className="health-filters">
        {/* Search */}
        <div className="stations-search" style={{ maxWidth: 320 }}>
          <span className="stations-search-icon">
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75">
              <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" strokeLinecap="round" />
            </svg>
          </span>
          <input
            className="input stations-search-input"
            placeholder="Buscar emissora…"
            value={search}
            onChange={e => { setSearch(e.target.value); setPage(1) }}
          />
        </div>

        {/* Band */}
        <div className="stations-band-filter">
          {['Todas', 'FM', 'AM'].map(b => (
            <button
              key={b}
              type="button"
              className={`band-tab${band === b ? ' active' : ''}`}
              onClick={() => { setBand(b); setPage(1) }}
            >
              {b}
            </button>
          ))}
        </div>

        {/* Health filter */}
        <div className="stations-band-filter">
          {HEALTH_FILTERS.map(f => (
            <button
              key={f}
              type="button"
              className={`band-tab${healthFilter === f ? ' active' : ''}`}
              onClick={() => { setHealth(f); setPage(1) }}
            >
              {f}
            </button>
          ))}
        </div>
      </div>

      {/* List */}
      {isLoading ? (
        <div className="health-list">
          {Array.from({ length: 8 }).map((_, i) => (
            <div key={i} className="health-row" style={{ gap: 14, pointerEvents: 'none' }}>
              <div className="skeleton" style={{ width: 36, height: 36, borderRadius: 8, flexShrink: 0 }} />
              <div style={{ flex: '0 0 220px', display: 'flex', flexDirection: 'column', gap: 5 }}>
                <div className="skeleton" style={{ width: 140, height: 13 }} />
                <div className="skeleton" style={{ width: 90, height: 11 }} />
              </div>
              <div className="skeleton" style={{ flex: 1, maxWidth: 120, height: 14 }} />
              <div className="skeleton" style={{ width: 60, height: 13 }} />
              <div className="skeleton" style={{ width: 110, height: 11 }} />
              <div className="skeleton" style={{ width: 10, height: 10, borderRadius: '50%' }} />
            </div>
          ))}
        </div>
      ) : (
        <>
          <div className="health-list">
            {pageItems.length === 0 && (
              <div className="health-event-empty">Nenhuma emissora encontrada</div>
            )}
            {pageItems.map(st => (
              <div
                key={st.id}
                className={`health-row${selectedId === st.id ? ' selected' : ''}`}
                onClick={() => setSelectedId(st.id === selectedId ? null : st.id)}
              >
                <StationAvatar station={st} size={36} />
                <div className="health-row-main">
                  <div className="health-row-name">{st.name}</div>
                  <div className="health-row-sub">
                    {st.band}{st.frequency_mhz ? ` · ${st.frequency_mhz}` : ''}
                    {st.city ? ` · ${st.city}` : ''}
                    {st.state ? `/${st.state}` : ''}
                  </div>
                </div>
                <div className="health-row-bar">
                  <MiniHealthBar dailySummary={st.daily_summary} />
                </div>
                <div className={`health-row-uptime${st.uptime_pct < 99 ? ' health-row-uptime--bad' : ' health-row-uptime--ok'}`}>
                  {st.uptime_pct.toFixed(1)}%
                </div>
                <div className="health-row-incident">
                  {st.last_incident_at
                    ? `Queda ${relativeTime(st.last_incident_at)}`
                    : 'Sem quedas'}
                </div>
                <StatusDot status={st.monitoring_status} />
              </div>
            ))}
          </div>

          {/* Pagination */}
          {totalPages > 1 && (
            <div className="pagination">
              <button className="pagination-btn" disabled={currentPage === 1} onClick={() => setPage(p => p - 1)}>
                ← Anterior
              </button>
              <div className="pagination-pages">
                {Array.from({ length: totalPages }, (_, i) => i + 1)
                  .filter(p => p === 1 || p === totalPages || Math.abs(p - currentPage) <= 1)
                  .reduce((acc, p, i, arr) => {
                    if (i > 0 && p - arr[i - 1] > 1) acc.push('…')
                    acc.push(p)
                    return acc
                  }, [])
                  .map((p, i) =>
                    typeof p === 'string'
                      ? <span key={`e${i}`} className="pagination-ellipsis">{p}</span>
                      : <button
                          key={p}
                          className={`pagination-page${p === currentPage ? ' active' : ''}`}
                          onClick={() => setPage(p)}
                        >{p}</button>
                  )}
              </div>
              <button className="pagination-btn" disabled={currentPage === totalPages} onClick={() => setPage(p => p + 1)}>
                Próxima →
              </button>
            </div>
          )}
        </>
      )}

      {/* Drawer */}
      {selectedStation && (
        <HealthDrawer
          station={selectedStation}
          onClose={() => setSelectedId(null)}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 2: Verify the frontend builds**

```bash
cd frontend && npm run build 2>&1 | tail -20
```
Expected: no errors. Warnings about unused imports are ok; errors are not.

- [ ] **Step 3: Manual smoke test**

Start dev server (`npm run dev`), open `http://localhost:3000/monitoring`:
- List renders (shows "0 emissoras em campanha ativa" if no active campaigns, which is expected before events accumulate)
- Filters work (search, band tabs, health filter)
- Clicking a row opens the drawer from the right
- Clicking another row swaps drawer content
- Clicking outside the drawer closes it
- Period tabs (1d / 7d) switch correctly
- Timeline renders (all gray = no data, expected initially)
- Pagination appears with 26+ items

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/MonitoringPage.jsx
git commit -m "feat(ui): replace MonitoringPage with Stream Health dashboard — list + drawer + timeline"
```

---

## Self-Review Checklist (completed inline)

- [x] **Spec coverage:** List view with mini bars (§4) ✓ | Drawer with timeline (§5) ✓ | Backend event recording with startup recovery (§6.1) ✓ | Two API endpoints (§6.2) ✓ | Zoom levels adaptive (§5) ✓ | Pagination + filters (§4) ✓
- [x] **Placeholder scan:** No TBD or TODO found. DB integration test for `ListActive` left as a comment with explicit instructions.
- [x] **Type consistency:** `HealthEvent`, `DailySummary`, `StationHealthSummary` defined in Task 1 and used consistently in Tasks 4/5/7/8. `OnStreamUp`/`OnStreamDown` defined in Task 3, consumed in Task 4. `ListActive` defined in Task 2, used in Task 5.
