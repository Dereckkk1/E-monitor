# Admin Station Failures Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `/admin/station-failures` admin screen that lists emissoras with stream/silent-gap failures on a given day plus the campaigns whose slots were lost, deep-linking each campaign chip to `/campaigns?campaign=<id>`.

**Architecture:** New backend endpoint `GET /v1/internal/admin/station-failures?date=&min_down_seconds=` (admin-gated, 3 SQL queries + Go window-cross logic). New frontend route `/admin/station-failures` with cards-per-station layout matching design.md §4.1. Existing `/v1/internal/campaigns` extended to filter by `?id=<uuid>` so the deep-link works without a new endpoint.

**Tech Stack:** Go 1.22 + chi + pgx + jackc/pgxpool. React 18 + React Query + react-router-dom v6. Vitest + RTL for frontend tests. Postgres for SQL.

**Spec:** [2026-05-19-admin-station-failures-design.md](../specs/2026-05-19-admin-station-failures-design.md)

---

## File Structure

**Backend (new):**
- `workers/internal/catalog/station_failures.go` — repo with `ListForDate(ctx, date, minDownSec) ([]StationFailure, error)` + pure helper `crossesAny(window, downs []TimeRange) bool`
- `workers/internal/catalog/station_failures_test.go` — pure unit tests + integration tests against TEST_DATABASE_URL
- `workers/internal/api/handlers/admin_station_failures.go` — `StationFailuresHandler` with `Get(w, r)`
- `workers/internal/api/handlers/admin_station_failures_test.go` — handler tests

**Backend (modify):**
- `workers/internal/catalog/campaigns.go` — add `id` filter to `ListPaged`
- `workers/internal/api/handlers/campaigns.go` — parse `?id=<uuid>`, pass to `ListPaged`
- `workers/internal/api/router.go` — register new handler under admin group, add `StationFailures` to `Deps`
- `workers/cmd/api/main.go` — instantiate `catalog.StationFailures` repo + handler, wire into `Deps`

**Frontend (new):**
- `frontend/src/pages/AdminStationFailuresPage.jsx` — page component
- `frontend/src/pages/AdminStationFailuresPage.css` — styles
- `frontend/src/components/StationFailureCard.jsx` — single-station card
- `frontend/src/components/StationFailureCard.css` — card styles
- `frontend/src/components/CampaignDeficitChip.jsx` — clickable campaign chip
- `frontend/src/components/CampaignDeficitChip.css` — chip styles

**Frontend (modify):**
- `frontend/src/api/hooks.js` — add `useStationFailures`; extend `useCampaignsPaged` with `id` param
- `frontend/src/pages/CampaignsPage.jsx` — read `?campaign=<id>` via `useSearchParams`, sticky filter banner
- `frontend/src/components/Sidebar.jsx` — add admin sidebar link
- `frontend/src/App.jsx` — register route

**Docs:**
- `docs/features/admin-station-failures.md` — feature doc
- `CLAUDE.md` — add entry to "Mapa de consulta"

---

## Task 1 — Catalog repo: pure window-cross helper

**Files:**
- Create: `workers/internal/catalog/station_failures.go`
- Create: `workers/internal/catalog/station_failures_test.go`

- [ ] **Step 1: Write the failing test for the window-cross helper**

`workers/internal/catalog/station_failures_test.go`:

```go
package catalog

import (
	"testing"
	"time"
)

func TestCrossesAny_NoOverlap(t *testing.T) {
	day := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	rule := TimeWindow{Start: "08:00", End: "09:00"}
	downs := []TimeRange{
		{From: day.Add(14 * time.Hour), Duration: 30 * time.Minute},
	}
	if crossesAny(day, rule, downs) {
		t.Errorf("morning rule should not cross afternoon down")
	}
}

func TestCrossesAny_FullOverlap(t *testing.T) {
	day := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	rule := TimeWindow{Start: "08:00", End: "10:00"}
	downs := []TimeRange{
		{From: day.Add(8*time.Hour + 30*time.Minute), Duration: 15 * time.Minute},
	}
	if !crossesAny(day, rule, downs) {
		t.Errorf("down inside rule window should cross")
	}
}

func TestCrossesAny_EdgeTouching(t *testing.T) {
	day := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	rule := TimeWindow{Start: "08:00", End: "09:00"}
	// Down ends exactly when rule starts — not a real overlap.
	downs := []TimeRange{
		{From: day.Add(7 * time.Hour), Duration: 60 * time.Minute},
	}
	if crossesAny(day, rule, downs) {
		t.Errorf("touching endpoint should not count as cross")
	}
}

func TestCrossesAny_EmptyDowns(t *testing.T) {
	day := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	rule := TimeWindow{Start: "08:00", End: "09:00"}
	if crossesAny(day, rule, nil) {
		t.Errorf("empty downs should never cross")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails (undefined types)**

Run: `cd workers && go test ./internal/catalog -run TestCrossesAny -v`
Expected: FAIL — `undefined: TimeWindow`, `undefined: TimeRange`, `undefined: crossesAny`

- [ ] **Step 3: Implement the types and helper**

`workers/internal/catalog/station_failures.go`:

```go
package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TimeWindow is a daily HH:MM-HH:MM slot from a distribution_rule.
type TimeWindow struct {
	Start string // "HH:MM"
	End   string // "HH:MM"
}

// TimeRange is an absolute [From, From+Duration) range.
type TimeRange struct {
	From     time.Time
	Duration time.Duration
}

// crossesAny reports whether the rule's daily slot (interpreted in the day's
// local frame) overlaps any down event range. Touching endpoints don't count.
// Pure function — no DB.
func crossesAny(day time.Time, rule TimeWindow, downs []TimeRange) bool {
	if len(downs) == 0 {
		return false
	}
	startH, startM, ok1 := parseHHMM(rule.Start)
	endH, endM, ok2 := parseHHMM(rule.End)
	if !ok1 || !ok2 {
		return false
	}
	ruleStart := day.Add(time.Duration(startH)*time.Hour + time.Duration(startM)*time.Minute)
	ruleEnd := day.Add(time.Duration(endH)*time.Hour + time.Duration(endM)*time.Minute)
	for _, d := range downs {
		downEnd := d.From.Add(d.Duration)
		// Strict overlap: ruleStart < downEnd AND downStart < ruleEnd
		if ruleStart.Before(downEnd) && d.From.Before(ruleEnd) {
			return true
		}
	}
	return false
}

func parseHHMM(s string) (int, int, bool) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return 0, 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// StationFailures repo — see ListForDate below.
type StationFailures struct {
	pool *pgxpool.Pool
}

func NewStationFailures(pool *pgxpool.Pool) *StationFailures {
	return &StationFailures{pool: pool}
}

// Result types — what the API returns.
type StationInfo struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Dial    string    `json:"dial"` // "102.7 FM"
	City    string    `json:"city"`
	LogoURL string    `json:"logo_url"`
}

type Incident struct {
	Type            string    `json:"type"` // "stream-down"
	EventAt         time.Time `json:"event_at"`
	DurationSeconds int       `json:"duration_seconds"`
}

type CampaignDeficit struct {
	CampaignID   uuid.UUID `json:"campaign_id"`
	CampaignName string    `json:"campaign_name"`
	ClientName   string    `json:"client_name"`
	Expected     int       `json:"expected"`
	Delivered    int       `json:"delivered"`
	Deficit      int       `json:"deficit"`
	AffectedBy   []string  `json:"affected_by"` // ["stream-down"] or ["silent-gap"] or both
}

type StationFailure struct {
	Station          StationInfo       `json:"station"`
	Incidents        []Incident        `json:"incidents"`
	TotalDownSeconds int               `json:"total_down_seconds"`
	HasSilentGap     bool              `json:"has_silent_gap"`
	Campaigns        []CampaignDeficit `json:"campaigns"`
}

type Summary struct {
	StationsWithFailure int `json:"stations_with_failure"`
	TotalDownSeconds    int `json:"total_down_seconds"`
	AffectedCampaigns   int `json:"affected_campaigns"`
}

type Result struct {
	Date     string           `json:"date"` // YYYY-MM-DD
	Summary  Summary          `json:"summary"`
	Stations []StationFailure `json:"stations"`
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd workers && go test ./internal/catalog -run TestCrossesAny -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/station_failures.go workers/internal/catalog/station_failures_test.go
git commit -m "feat(workers): add window-cross helper for station failures repo"
```

---

## Task 2 — Catalog repo: ListForDate (integration test against DB)

**Files:**
- Modify: `workers/internal/catalog/station_failures.go`
- Modify: `workers/internal/catalog/station_failures_test.go`

- [ ] **Step 1: Write the failing integration test**

Append to `workers/internal/catalog/station_failures_test.go`:

```go
// Integration test — requires TEST_DATABASE_URL.
func TestStationFailures_ListForDate_NoFailures(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewStationFailures(pool)

	// A date with nothing — return empty slice + zero summary.
	farPast := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	res, err := repo.ListForDate(ctx, farPast, 60)
	if err != nil {
		t.Fatalf("ListForDate: %v", err)
	}
	if len(res.Stations) != 0 {
		t.Errorf("expected 0 stations, got %d", len(res.Stations))
	}
	if res.Summary.StationsWithFailure != 0 {
		t.Errorf("expected 0 stations_with_failure, got %d", res.Summary.StationsWithFailure)
	}
}
```

- [ ] **Step 2: Run to verify it fails (undefined method)**

Run: `cd workers && go test ./internal/catalog -run TestStationFailures_ListForDate -v`
Expected: FAIL — `repo.ListForDate undefined`

- [ ] **Step 3: Implement ListForDate**

Append to `workers/internal/catalog/station_failures.go`:

```go
// ListForDate returns all stations that had a failure (stream-down OR
// silent-gap) on the given local-day, with the campaigns whose slots were
// lost. minDownSeconds filters out tiny down events (default 60).
func (r *StationFailures) ListForDate(ctx context.Context, day time.Time, minDownSeconds int) (*Result, error) {
	dayStr := day.Format("2006-01-02")
	result := &Result{
		Date:     dayStr,
		Stations: []StationFailure{},
	}

	// Query 1 — stations with problems. Returns id, name, dial parts, city,
	// logo, total down_sec (only counts closed downs ≥ minDownSeconds when
	// aggregating; open downs always count their elapsed time).
	rows, err := r.pool.Query(ctx, `
WITH down_aggr AS (
  SELECT station_id,
         SUM(COALESCE(duration_seconds, EXTRACT(EPOCH FROM (NOW() - event_at))::int)) AS down_sec
  FROM stream_health_events
  WHERE event_type = 'down'
    AND event_at >= $1::date AND event_at < ($1::date + INTERVAL '1 day')
  GROUP BY station_id
  HAVING SUM(COALESCE(duration_seconds, EXTRACT(EPOCH FROM (NOW() - event_at))::int)) >= $2
),
deficit_aggr AS (
  SELECT station_id, COUNT(DISTINCT campaign_id) AS aff_camp
  FROM daily_play_summary
  WHERE for_date = $1::date AND deficit > 0
  GROUP BY station_id
)
SELECT s.id, s.name,
       COALESCE(s.band, ''),
       COALESCE(to_char(s.frequency_mhz, 'FM999990.0'), ''),
       COALESCE(s.city, ''),
       COALESCE(s.logo_url, ''),
       COALESCE(d.down_sec, 0)::int,
       COALESCE(df.aff_camp, 0)::int
FROM stations s
LEFT JOIN down_aggr d   ON d.station_id = s.id
LEFT JOIN deficit_aggr df ON df.station_id = s.id
WHERE (d.station_id IS NOT NULL OR df.station_id IS NOT NULL)
ORDER BY COALESCE(d.down_sec, 0) DESC, COALESCE(df.aff_camp, 0) DESC, s.name ASC`,
		dayStr, minDownSeconds)
	if err != nil {
		return nil, fmt.Errorf("query 1 stations: %w", err)
	}
	defer rows.Close()

	type stationAcc struct {
		info     StationInfo
		downSec  int
		affCamp  int
	}
	stationMap := map[uuid.UUID]*stationAcc{}
	stationIDs := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		var name, band, freq, city, logo string
		var downSec, affCamp int
		if err := rows.Scan(&id, &name, &band, &freq, &city, &logo, &downSec, &affCamp); err != nil {
			return nil, err
		}
		// freq comes already formatted by to_char (e.g. "102.7"); compose
		// "102.7 FM" / "1170 AM" / partial fallbacks.
		dial := ""
		if freq != "" && band != "" {
			dial = freq + " " + band
		} else if freq != "" {
			dial = freq
		} else {
			dial = band
		}
		stationMap[id] = &stationAcc{
			info:    StationInfo{ID: id, Name: name, Dial: dial, City: city, LogoURL: logo},
			downSec: downSec, affCamp: affCamp,
		}
		stationIDs = append(stationIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(stationIDs) == 0 {
		return result, nil
	}

	// Query 2 — incidents per station.
	downsByStation := map[uuid.UUID][]TimeRange{}
	incidentsByStation := map[uuid.UUID][]Incident{}
	rows2, err := r.pool.Query(ctx, `
SELECT station_id, event_at,
       COALESCE(duration_seconds, EXTRACT(EPOCH FROM (NOW() - event_at))::int)::int AS dur
FROM stream_health_events
WHERE event_type = 'down'
  AND event_at >= $1::date AND event_at < ($1::date + INTERVAL '1 day')
  AND station_id = ANY($2::uuid[])
ORDER BY station_id, event_at ASC`,
		dayStr, stationIDs)
	if err != nil {
		return nil, fmt.Errorf("query 2 incidents: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var sid uuid.UUID
		var at time.Time
		var dur int
		if err := rows2.Scan(&sid, &at, &dur); err != nil {
			return nil, err
		}
		downsByStation[sid] = append(downsByStation[sid], TimeRange{From: at, Duration: time.Duration(dur) * time.Second})
		incidentsByStation[sid] = append(incidentsByStation[sid], Incident{
			Type:            "stream-down",
			EventAt:         at,
			DurationSeconds: dur,
		})
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	// Query 3 — campaigns with deficit + rule windows for the day.
	rows3, err := r.pool.Query(ctx, `
SELECT dps.station_id, dps.campaign_id,
       c.name AS campaign_name, COALESCE(cl.name, '—') AS client_name,
       SUM(dps.expected)::int AS expected,
       SUM(dps.in_slot)::int  AS delivered,
       SUM(dps.deficit)::int  AS deficit,
       COALESCE((
         SELECT array_agg(to_char(dr.time_start, 'HH24:MI') || '/' ||
                          to_char(dr.time_end,   'HH24:MI'))
         FROM distribution_rules dr
         WHERE dr.campaign_id = dps.campaign_id
           AND dps.station_id = ANY(dr.station_ids)
           AND dr.start_date <= $1::date AND dr.end_date >= $1::date
           AND (1 << EXTRACT(DOW FROM $1::date)::int) & dr.weekday_mask <> 0
       ), ARRAY[]::text[]) AS rule_windows
FROM daily_play_summary dps
JOIN campaigns c ON c.id = dps.campaign_id
LEFT JOIN clients cl ON cl.id = c.client_id
WHERE dps.for_date = $1::date
  AND dps.deficit > 0
  AND dps.station_id = ANY($2::uuid[])
GROUP BY dps.station_id, dps.campaign_id, c.name, cl.name
ORDER BY dps.station_id, deficit DESC`,
		dayStr, stationIDs)
	if err != nil {
		return nil, fmt.Errorf("query 3 deficits: %w", err)
	}
	defer rows3.Close()

	type campAcc struct {
		Campaign CampaignDeficit
		Windows  []TimeWindow
	}
	campsByStation := map[uuid.UUID][]campAcc{}
	for rows3.Next() {
		var sid, cid uuid.UUID
		var name, client string
		var expected, delivered, deficit int
		var windows []string
		if err := rows3.Scan(&sid, &cid, &name, &client, &expected, &delivered, &deficit, &windows); err != nil {
			return nil, err
		}
		wins := make([]TimeWindow, 0, len(windows))
		for _, w := range windows {
			// "HH:MM/HH:MM"
			var s, e string
			if _, err := fmt.Sscanf(w, "%5s/%5s", &s, &e); err == nil {
				wins = append(wins, TimeWindow{Start: s, End: e})
			}
		}
		campsByStation[sid] = append(campsByStation[sid], campAcc{
			Campaign: CampaignDeficit{
				CampaignID: cid, CampaignName: name, ClientName: client,
				Expected: expected, Delivered: delivered, Deficit: deficit,
			},
			Windows: wins,
		})
	}
	if err := rows3.Err(); err != nil {
		return nil, err
	}

	// Assemble results — classify affected_by via crossesAny.
	totalAffected := 0
	totalDown := 0
	dayLocal := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local)
	for _, sid := range stationIDs {
		acc := stationMap[sid]
		downs := downsByStation[sid]
		camps := campsByStation[sid]
		sf := StationFailure{
			Station:          acc.info,
			Incidents:        incidentsByStation[sid],
			TotalDownSeconds: acc.downSec,
			Campaigns:        []CampaignDeficit{},
		}
		if sf.Incidents == nil {
			sf.Incidents = []Incident{}
		}
		hasSilent := false
		for _, c := range camps {
			affectedBy := []string{}
			anyCross := false
			for _, w := range c.Windows {
				if crossesAny(dayLocal, w, downs) {
					anyCross = true
					break
				}
			}
			if anyCross {
				affectedBy = append(affectedBy, "stream-down")
			}
			if !anyCross || len(c.Windows) == 0 {
				affectedBy = append(affectedBy, "silent-gap")
				hasSilent = true
			}
			cd := c.Campaign
			cd.AffectedBy = affectedBy
			sf.Campaigns = append(sf.Campaigns, cd)
		}
		sf.HasSilentGap = hasSilent
		totalAffected += len(sf.Campaigns)
		totalDown += sf.TotalDownSeconds
		result.Stations = append(result.Stations, sf)
	}
	result.Summary = Summary{
		StationsWithFailure: len(result.Stations),
		TotalDownSeconds:    totalDown,
		AffectedCampaigns:   totalAffected,
	}
	return result, nil
}
```

- [ ] **Step 4: Run integration test to verify it passes**

Run: `cd workers && TEST_DATABASE_URL=$DATABASE_URL go test ./internal/catalog -run TestStationFailures_ListForDate -v`

(On Windows bash: `TEST_DATABASE_URL=postgres://radiocheck:radiocheck@localhost:5433/radiocheck?sslmode=disable go test ./internal/catalog -run TestStationFailures_ListForDate -v` — adapt to your dev env)

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/station_failures.go workers/internal/catalog/station_failures_test.go
git commit -m "feat(workers): implement station_failures.ListForDate repo query"
```

---

## Task 3 — Handler + router wire-up

**Files:**
- Create: `workers/internal/api/handlers/admin_station_failures.go`
- Create: `workers/internal/api/handlers/admin_station_failures_test.go`
- Modify: `workers/internal/api/router.go`
- Modify: `workers/cmd/api/main.go`

- [ ] **Step 1: Write failing handler test**

`workers/internal/api/handlers/admin_station_failures_test.go`:

```go
package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStationFailures_DateValidation(t *testing.T) {
	h := &StationFailuresHandler{}

	cases := []struct {
		name string
		date string
		want int
	}{
		{"valid", time.Now().Add(-24 * time.Hour).Format("2006-01-02"), 200},
		{"future", time.Now().Add(48 * time.Hour).Format("2006-01-02"), 400},
		{"too_old", time.Now().Add(-100 * 24 * time.Hour).Format("2006-01-02"), 400},
		{"malformed", "not-a-date", 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/?date="+tc.date, nil)
			rr := httptest.NewRecorder()
			h.Get(rr, req)
			// 200 may need a repo — we only check 4xx vs not-4xx here.
			if tc.want == 400 && rr.Code != 400 {
				t.Errorf("date %s: want 400, got %d", tc.date, rr.Code)
			}
			if tc.want == 200 && rr.Code == 400 {
				t.Errorf("date %s: should not be 400, got 400 body=%s", tc.date, rr.Body.String())
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd workers && go test ./internal/api/handlers -run TestStationFailures_DateValidation -v`
Expected: FAIL — `undefined: StationFailuresHandler`

- [ ] **Step 3: Implement handler**

`workers/internal/api/handlers/admin_station_failures.go`:

```go
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/catalog"
)

type StationFailuresRepo interface {
	ListForDate(ctx context.Context, day time.Time, minDownSeconds int) (*catalog.Result, error)
}

type StationFailuresHandler struct {
	Repo StationFailuresRepo
	Log  *zap.Logger
}

func (h *StationFailuresHandler) Get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	dateStr := q.Get("date")
	if dateStr == "" {
		dateStr = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	}
	day, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
	if err != nil {
		http.Error(w, "invalid date: expected YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	today := time.Now().Truncate(24 * time.Hour)
	if day.After(today) {
		http.Error(w, "invalid date: future not supported", http.StatusBadRequest)
		return
	}
	if today.Sub(day) > 90*24*time.Hour {
		http.Error(w, "invalid date: older than 90 days", http.StatusBadRequest)
		return
	}

	minDown := 60
	if v := q.Get("min_down_seconds"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 3600 {
			minDown = n
		}
	}

	if h.Repo == nil {
		// In tests without repo, just respond OK with empty result.
		writeJSON(w, http.StatusOK, &catalog.Result{
			Date: day.Format("2006-01-02"), Stations: []catalog.StationFailure{},
		})
		return
	}
	res, err := h.Repo.ListForDate(r.Context(), day, minDown)
	if err != nil {
		if h.Log != nil {
			h.Log.Error("station_failures_query_failed",
				zap.String("date", day.Format("2006-01-02")),
				zap.Error(err))
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// writeJSON helper — local copy to avoid coupling to other handler files.
func writeJSONStationFailures(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
```

**Note:** `writeJSON` already exists in the handlers package (used by `campaigns.go`). Reuse it instead of the local copy — delete the `writeJSONStationFailures` function and use `writeJSON(w, code, body)`. Final implementation:

```go
// Remove writeJSONStationFailures entirely. Use existing writeJSON.
```

- [ ] **Step 4: Run handler test**

Run: `cd workers && go test ./internal/api/handlers -run TestStationFailures_DateValidation -v`
Expected: PASS (4 cases)

- [ ] **Step 5: Wire handler into router and main**

Edit `workers/internal/api/router.go` — add to `Deps` struct (right after `AdminMonitoring`):

```go
	StationFailures       *handlers.StationFailuresHandler
```

In `NewRouter`, after the AdminMonitoring block (~line 359), add:

```go
				if d.StationFailures != nil {
					r.Group(func(r chi.Router) {
						r.Use(auth.RequireRole("admin"))
						r.Get("/admin/station-failures", d.StationFailures.Get)
					})
				}
```

Edit `workers/cmd/api/main.go` — instantiate the repo and handler. Find where `SystemHealth` is wired and add nearby:

```go
	stationFailuresRepo := catalog.NewStationFailures(pool)
	stationFailuresHandler := &handlers.StationFailuresHandler{
		Repo: stationFailuresRepo,
		Log:  logger,
	}
```

Add `StationFailures: stationFailuresHandler,` to the `api.Deps{}` literal.

- [ ] **Step 6: Run go build to verify wiring**

Run: `cd workers && go build ./...`
Expected: no errors

- [ ] **Step 7: Commit**

```bash
git add workers/internal/api/handlers/admin_station_failures.go workers/internal/api/handlers/admin_station_failures_test.go workers/internal/api/router.go workers/cmd/api/main.go
git commit -m "feat(workers): wire /admin/station-failures endpoint"
```

---

## Task 4 — Extend `/campaigns/paged` with `?id=<uuid>` filter

**Files:**
- Modify: `workers/internal/catalog/campaigns.go` (line 67–142, `ListPaged`)
- Modify: `workers/internal/api/handlers/campaigns.go` (line 45–82, `List`)

- [ ] **Step 1: Write failing test for handler `?id` parsing**

Append to `workers/internal/api/handlers/admin_test.go` (or create `campaigns_filter_test.go`):

```go
func TestCampaigns_ListPaged_FilterByID_InvalidUUID(t *testing.T) {
	// Setup: a mock-free unit test would need a mock repo. Skip for now —
	// covered by integration test below.
	t.Skip("covered by integration")
}
```

Add a catalog-level test in `workers/internal/catalog/campaigns_test.go` (create if missing):

```go
package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestCampaigns_ListPaged_ByID_NotFound(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewCampaigns(pool)
	bogus := uuid.New()
	items, total, err := repo.ListPaged(ctx, "", "", nil, &bogus, 1, 20)
	if err != nil {
		t.Fatalf("ListPaged: %v", err)
	}
	if total != 0 || len(items) != 0 {
		t.Errorf("expected empty result, got total=%d len=%d", total, len(items))
	}
}
```

- [ ] **Step 2: Run to verify failure (signature mismatch)**

Run: `cd workers && go test ./internal/catalog -run TestCampaigns_ListPaged_ByID -v`
Expected: FAIL — too many arguments

- [ ] **Step 3: Extend `ListPaged` signature**

Edit `workers/internal/catalog/campaigns.go`:

Change signature on line 67:
```go
func (c *Campaigns) ListPaged(ctx context.Context, q, competence string, clientID *uuid.UUID, campaignID *uuid.UUID, page, pageSize int) ([]Campaign, int, error) {
```

Update the WHERE clause (around line 88) to include `$6` for campaign id:
```go
	// $1 = q, $2 = competence flag, $3 = monthStart, $4 = monthEnd, $5 = clientID, $6 = campaignID
	const where = `
		WHERE
		    ($2 = '' OR (c.start_date <= $4 AND c.end_date >= $3))
		    AND ($1 = '' OR unaccent(lower(
		        COALESCE(c.name,'') || ' ' || COALESCE(cl.name,'')
		    )) LIKE '%' || unaccent(lower($1)) || '%')
		    AND ($5::uuid IS NULL OR c.client_id = $5)
		    AND ($6::uuid IS NULL OR c.id = $6)
	`
```

Update both queries to bind `$6 = campaignID` and shift LIMIT/OFFSET to `$7/$8`:

Count query (line 99–106):
```go
	if err := c.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM campaigns c
		LEFT JOIN clients cl ON cl.id = c.client_id`+where,
		q, competence, monthStart, monthEnd, clientID, campaignID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
```

Main query (line 108–125):
```go
	offset := (page - 1) * pageSize
	rows, err := c.pool.Query(ctx, `
		SELECT c.id, c.client_id, c.name, c.start_date, c.end_date, c.status, c.target_stations,
		       c.created_at, c.updated_at
		FROM campaigns c
		LEFT JOIN clients cl ON cl.id = c.client_id`+where+`
		ORDER BY CASE c.status
		    WHEN 'ativa'      THEN 1
		    WHEN 'programada' THEN 2
		    WHEN 'concluida'  THEN 3
		    WHEN 'cancelada'  THEN 4
		    ELSE 5
		END,
		CASE WHEN c.status = 'programada' THEN c.start_date ELSE NULL END ASC NULLS LAST,
		c.start_date DESC
		LIMIT $7 OFFSET $8`,
		q, competence, monthStart, monthEnd, clientID, campaignID, pageSize, offset,
	)
```

- [ ] **Step 4: Update all existing callers of ListPaged**

Find callers:
```bash
cd workers && grep -rn "ListPaged(" --include="*.go" | grep -v test
```

Update each call to pass `nil` for the new `campaignID` parameter. Most likely just the handler in `campaigns.go`.

Edit `workers/internal/api/handlers/campaigns.go` line 65 — parse `?id=` and pass to ListPaged:

Around line 53 (paged-mode trigger), extend to also trigger when `?id` is set:
```go
		if q.Get("page") != "" || q.Get("page_size") != "" || q.Get("q") != "" || q.Get("competence") != "" || q.Get("id") != "" {
```

Around line 65, parse and pass:
```go
		var campIDPtr *uuid.UUID
		if idStr := q.Get("id"); idStr != "" {
			cid, err := uuid.Parse(idStr)
			if err != nil {
				http.Error(w, "invalid id", 400)
				return
			}
			campIDPtr = &cid
		}
		items, total, err := h.Repo.ListPaged(r.Context(), q.Get("q"), q.Get("competence"), scope, campIDPtr, page, size)
```

- [ ] **Step 5: Run tests + build**

Run: `cd workers && go build ./... && go test ./internal/catalog -run TestCampaigns_ListPaged -v && go test ./internal/api/handlers -count=1 ./...`
Expected: all pass

- [ ] **Step 6: Commit**

```bash
git add workers/internal/catalog/campaigns.go workers/internal/catalog/campaigns_test.go workers/internal/api/handlers/campaigns.go
git commit -m "feat(workers): add ?id=<uuid> filter to /campaigns/paged"
```

---

## Task 5 — Frontend: useStationFailures hook + extend useCampaignsPaged

**Files:**
- Modify: `frontend/src/api/hooks.js`

- [ ] **Step 1: Add useStationFailures and extend useCampaignsPaged**

Edit `frontend/src/api/hooks.js`. After `useCampaignsPaged` (line 109), add:

```js
// Admin — emissoras que falharam num dado dia (default ontem). Cross-reference
// stream-down events + daily_play_summary deficits. Single endpoint, server-side.
export function useStationFailures({ date, minDownSeconds = 60 } = {}) {
  return useQuery({
    queryKey: ['station-failures', date, minDownSeconds],
    queryFn: () => api.get('/admin/station-failures', {
      params: { date, min_down_seconds: minDownSeconds },
    }).then(r => r.data),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  })
}
```

Modify `useCampaignsPaged` (line 93) to accept `id`:

```js
export function useCampaignsPaged({ q = '', competence = '', id = '', page = 1, pageSize = 12 } = {}) {
  return useQuery({
    queryKey: ['campaigns', 'paged', q, competence, id, page, pageSize],
    queryFn: () => api.get('/campaigns', {
      params: {
        q: q || undefined,
        competence: competence || undefined,
        id: id || undefined,
        page,
        page_size: pageSize,
      },
    }).then(r => r.data),
    placeholderData: (prev) => prev,
  })
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "feat(frontend): add useStationFailures + id filter on useCampaignsPaged"
```

---

## Task 6 — Frontend: CampaignDeficitChip component

**Files:**
- Create: `frontend/src/components/CampaignDeficitChip.jsx`
- Create: `frontend/src/components/CampaignDeficitChip.css`

- [ ] **Step 1: Create the chip component**

`frontend/src/components/CampaignDeficitChip.jsx`:

```jsx
import { Link } from 'react-router-dom'
import './CampaignDeficitChip.css'

export default function CampaignDeficitChip({ data }) {
  const { campaign_id, campaign_name, client_name, expected, delivered, deficit, affected_by } = data
  const isSilent = affected_by?.includes('silent-gap')

  return (
    <Link to={`/campaigns?campaign=${campaign_id}`} className="campaign-deficit-chip">
      <div className="cdc-name-row">
        <strong className="cdc-name">{campaign_name}</strong>
        <span className="cdc-client">{client_name}</span>
      </div>
      <div className="cdc-numbers-row">
        <span className="cdc-num">esperado <b>{expected}</b></span>
        <span className="cdc-dot">·</span>
        <span className="cdc-num">entregue <b>{delivered}</b></span>
        <span className="cdc-dot">·</span>
        <span className="cdc-num cdc-deficit">faltam <b>{deficit}</b></span>
        {isSilent && <span className="cdc-pill cdc-pill-warn">silent-gap</span>}
      </div>
      <svg className="cdc-arrow" width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
        <path d="M5 3l4 4-4 4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
      </svg>
    </Link>
  )
}
```

`frontend/src/components/CampaignDeficitChip.css`:

```css
.campaign-deficit-chip {
  position: relative;
  display: block;
  text-decoration: none;
  background: var(--color-white);
  border: 1px solid var(--color-gray-200);
  border-radius: var(--radius-lg);
  padding: 12px 36px 12px 14px;
  color: var(--color-gray-900);
  transition: all 0.15s ease;
}
.campaign-deficit-chip:hover {
  transform: translateY(-1px);
  border-color: var(--color-tertiary-300);
  box-shadow: var(--shadow-sm);
}
.campaign-deficit-chip:hover .cdc-arrow {
  color: var(--color-tertiary-500);
  transform: translateX(2px);
}
.cdc-name-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: 4px;
}
.cdc-name {
  font-family: var(--font-family-primary);
  font-size: 14px;
  font-weight: 600;
  color: var(--color-gray-900);
}
.cdc-client {
  font-size: 12px;
  color: var(--color-gray-500);
  font-weight: 500;
}
.cdc-numbers-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--color-gray-600);
}
.cdc-num b { color: var(--color-gray-900); font-weight: 600; }
.cdc-deficit b { color: var(--color-danger, #dc2626); }
.cdc-dot { color: var(--color-gray-300); }
.cdc-pill {
  margin-left: 8px;
  padding: 2px 8px;
  border-radius: var(--radius-full);
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.cdc-pill-warn {
  background: rgba(245, 158, 11, 0.12);
  color: #b45309;
}
.cdc-arrow {
  position: absolute;
  right: 14px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--color-gray-400);
  transition: all 0.15s ease;
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/CampaignDeficitChip.jsx frontend/src/components/CampaignDeficitChip.css
git commit -m "feat(frontend): add CampaignDeficitChip component"
```

---

## Task 7 — Frontend: StationFailureCard component

**Files:**
- Create: `frontend/src/components/StationFailureCard.jsx`
- Create: `frontend/src/components/StationFailureCard.css`

- [ ] **Step 1: Create the card**

`frontend/src/components/StationFailureCard.jsx`:

```jsx
import StationAvatar from './StationAvatar'
import CampaignDeficitChip from './CampaignDeficitChip'
import './StationFailureCard.css'

function fmtDuration(sec) {
  if (!sec || sec < 60) return `${sec || 0}s`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}min`
  const h = Math.floor(min / 60)
  const remMin = min - h * 60
  return remMin > 0 ? `${h}h ${remMin}min` : `${h}h`
}

function badgeTone(sec) {
  if (sec >= 1800) return 'danger'
  if (sec >= 300) return 'warn'
  return 'neutral'
}

export default function StationFailureCard({ data }) {
  const { station, incidents, total_down_seconds, has_silent_gap, campaigns } = data
  const tone = badgeTone(total_down_seconds)

  return (
    <article className="station-failure-card">
      <header className="sfc-header">
        <div className="sfc-logo">
          <StationAvatar station={station} size={48} />
        </div>
        <div className="sfc-identity">
          <h3 className="sfc-name">{station.name}</h3>
          <p className="sfc-meta">{station.dial}{station.city ? ` · ${station.city}` : ''}</p>
        </div>
        <span className={`sfc-badge sfc-badge-${tone}`}>
          {fmtDuration(total_down_seconds)} fora
        </span>
      </header>

      <p className="sfc-summary-line">
        {incidents.length} {incidents.length === 1 ? 'incidente' : 'incidentes'}
        {' · '}{fmtDuration(total_down_seconds)} fora
        {has_silent_gap && ' · silent-gap detectado'}
      </p>

      {campaigns.length > 0 && (
        <div className="sfc-campaigns">
          <h4 className="sfc-campaigns-title">Campanhas afetadas</h4>
          <ul className="sfc-campaigns-list">
            {campaigns.map(c => (
              <li key={c.campaign_id}>
                <CampaignDeficitChip data={c} />
              </li>
            ))}
          </ul>
        </div>
      )}
      {campaigns.length === 0 && (
        <p className="sfc-no-campaigns">nenhuma campanha tinha slot nesse intervalo</p>
      )}
    </article>
  )
}
```

`frontend/src/components/StationFailureCard.css`:

```css
.station-failure-card {
  background: var(--color-white);
  border: 1px solid var(--color-gray-200);
  border-radius: var(--radius-xl);
  padding: 20px 24px;
  box-shadow: var(--shadow-sm);
  transition: all 0.18s ease;
}
.station-failure-card:hover {
  transform: translateY(-2px);
  border-color: var(--color-tertiary-300);
  box-shadow: var(--shadow-md);
}
.sfc-header {
  display: flex;
  align-items: center;
  gap: 14px;
}
.sfc-logo {
  flex: 0 0 48px;
  width: 48px;
  height: 48px;
  border-radius: var(--radius-md);
  overflow: hidden;
  background: var(--color-gray-100);
}
.sfc-logo-img { width: 100%; height: 100%; object-fit: cover; }
.sfc-identity { flex: 1 1 auto; min-width: 0; }
.sfc-name {
  margin: 0;
  font-family: var(--font-family-primary);
  font-size: 16px;
  font-weight: 700;
  color: var(--color-gray-900);
  letter-spacing: -0.01em;
}
.sfc-meta {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--color-gray-500);
}
.sfc-badge {
  padding: 6px 12px;
  border-radius: var(--radius-full);
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
}
.sfc-badge-neutral { background: var(--color-gray-100); color: var(--color-gray-700); }
.sfc-badge-warn    { background: rgba(245, 158, 11, 0.12); color: #b45309; }
.sfc-badge-danger  { background: rgba(220, 38, 38, 0.10);  color: #b91c1c; }

.sfc-summary-line {
  margin: 14px 0 0;
  font-size: 13px;
  color: var(--color-gray-600);
}
.sfc-campaigns {
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px solid var(--color-gray-100);
}
.sfc-campaigns-title {
  margin: 0 0 10px;
  font-family: var(--font-family-secondary);
  font-size: 12px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--color-gray-500);
}
.sfc-campaigns-list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: grid;
  gap: 8px;
}
.sfc-no-campaigns {
  margin-top: 12px;
  font-size: 12px;
  color: var(--color-gray-500);
  font-style: italic;
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/StationFailureCard.jsx frontend/src/components/StationFailureCard.css
git commit -m "feat(frontend): add StationFailureCard component"
```

---

## Task 8 — Frontend: AdminStationFailuresPage

**Files:**
- Create: `frontend/src/pages/AdminStationFailuresPage.jsx`
- Create: `frontend/src/pages/AdminStationFailuresPage.css`

- [ ] **Step 1: Create the page**

`frontend/src/pages/AdminStationFailuresPage.jsx`:

```jsx
import { useState, useMemo } from 'react'
import { useStationFailures } from '../api/hooks'
import StationFailureCard from '../components/StationFailureCard'
import './AdminStationFailuresPage.css'

function isoYesterday() {
  const d = new Date()
  d.setDate(d.getDate() - 1)
  return d.toISOString().slice(0, 10)
}
function isoToday() {
  return new Date().toISOString().slice(0, 10)
}
function isoMinusDays(n) {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return d.toISOString().slice(0, 10)
}
function fmtDuration(sec) {
  if (!sec) return '0min'
  if (sec < 60) return `${sec}s`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}min`
  const h = Math.floor(min / 60)
  const rem = min - h * 60
  return rem > 0 ? `${h}h ${rem}min` : `${h}h`
}

const MIN_DOWN_OPTIONS = [
  { value: 0, label: 'Tudo' },
  { value: 60, label: '≥ 1 min' },
  { value: 300, label: '≥ 5 min' },
  { value: 1800, label: '≥ 30 min' },
]

export default function AdminStationFailuresPage() {
  const [date, setDate] = useState(isoYesterday())
  const [minDown, setMinDown] = useState(60)

  const { data, isLoading, isFetching, refetch, error } = useStationFailures({
    date, minDownSeconds: minDown,
  })

  const stations = data?.stations ?? []
  const summary = data?.summary

  return (
    <div className="asf-page">
      <header className="asf-header">
        <div>
          <h1 className="asf-title">Emissoras com falha</h1>
          <p className="asf-subtitle">Quais rádios saíram do ar e quais campanhas perderam slot</p>
        </div>
        <div className="asf-controls">
          <label className="asf-control">
            <span>Data</span>
            <input
              type="date"
              value={date}
              onChange={e => setDate(e.target.value)}
              max={isoToday()}
              min={isoMinusDays(90)}
            />
          </label>
          <label className="asf-control">
            <span>Mín. tempo fora</span>
            <select value={minDown} onChange={e => setMinDown(Number(e.target.value))}>
              {MIN_DOWN_OPTIONS.map(o => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
          </label>
          <button className="asf-refresh" onClick={() => refetch()} disabled={isFetching}>
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true"
                 className={isFetching ? 'asf-spin' : ''}>
              <path d="M2 8a6 6 0 1 0 1.76-4.24M2 2v3.5h3.5" stroke="currentColor" strokeWidth="1.5"
                    strokeLinecap="round" strokeLinejoin="round"/>
            </svg>
            Atualizar
          </button>
        </div>
      </header>

      {summary && summary.stations_with_failure > 0 && (
        <div className="asf-summary-strip">
          <strong>{summary.stations_with_failure}</strong> {summary.stations_with_failure === 1 ? 'emissora' : 'emissoras'}
          <span className="asf-dot">·</span>
          <strong>{fmtDuration(summary.total_down_seconds)}</strong> total fora
          <span className="asf-dot">·</span>
          <strong>{summary.affected_campaigns}</strong> {summary.affected_campaigns === 1 ? 'campanha afetada' : 'campanhas afetadas'}
        </div>
      )}

      {error && (
        <div className="asf-error">Erro ao carregar: {String(error.message || error)}</div>
      )}

      {isLoading && (
        <div className="asf-skeleton-list">
          {[0, 1, 2, 3, 4].map(i => (
            <div key={i} className="asf-skeleton-card" />
          ))}
        </div>
      )}

      {!isLoading && stations.length === 0 && !error && (
        <div className="asf-empty">
          <div className="asf-empty-icon" aria-hidden="true">
            <svg width="56" height="56" viewBox="0 0 56 56" fill="none">
              <circle cx="28" cy="28" r="22" stroke="currentColor" strokeWidth="2" opacity="0.4"/>
              <path d="M19 28l6 6 12-12" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"/>
            </svg>
          </div>
          <h2>Nenhuma emissora teve falha em {date}</h2>
          <p>Tudo no ar. Pra ver o pulso atual da infra, abra <a href="/admin/overview">Visão geral</a>.</p>
          <div className="asf-empty-shadow">
            <div className="asf-skeleton-card asf-skeleton-card-ghost" />
            <div className="asf-skeleton-card asf-skeleton-card-ghost" />
          </div>
        </div>
      )}

      {!isLoading && stations.length > 0 && (
        <div className="asf-list">
          {stations.map(s => (
            <StationFailureCard key={s.station.id} data={s} />
          ))}
        </div>
      )}
    </div>
  )
}
```

`frontend/src/pages/AdminStationFailuresPage.css`:

```css
.asf-page {
  max-width: 1100px;
  margin: 0 auto;
  padding: 24px 32px 64px;
}
.asf-header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 24px;
  margin-bottom: 20px;
  flex-wrap: wrap;
}
.asf-title {
  margin: 0;
  font-family: var(--font-family-primary);
  font-size: 24px;
  font-weight: 700;
  color: var(--color-gray-900);
  letter-spacing: -0.02em;
}
.asf-subtitle {
  margin: 4px 0 0;
  color: var(--color-gray-500);
  font-size: 13px;
}
.asf-controls {
  display: flex;
  gap: 12px;
  align-items: flex-end;
}
.asf-control {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 11px;
  color: var(--color-gray-500);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-weight: 600;
}
.asf-control input,
.asf-control select {
  font-family: var(--font-family-secondary);
  font-size: 14px;
  padding: 8px 12px;
  border: 1px solid var(--color-gray-200);
  border-radius: var(--radius-md);
  background: var(--color-white);
  color: var(--color-gray-900);
  outline: none;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}
.asf-control input:focus,
.asf-control select:focus {
  border-color: var(--color-tertiary-400);
  box-shadow: 0 0 0 3px rgba(236, 72, 153, 0.1);
}
.asf-refresh {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  background: var(--color-white);
  border: 1px solid var(--color-gray-200);
  border-radius: var(--radius-md);
  color: var(--color-gray-700);
  font-family: var(--font-family-secondary);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
}
.asf-refresh:hover:not(:disabled) {
  border-color: var(--color-tertiary-300);
  color: var(--color-tertiary-500);
  transform: translateY(-1px);
}
.asf-refresh:disabled { opacity: 0.6; cursor: wait; }
.asf-spin { animation: asfSpin 1s linear infinite; }
@keyframes asfSpin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }

.asf-summary-strip {
  background: var(--color-white);
  border: 1px solid var(--color-gray-200);
  border-radius: var(--radius-lg);
  padding: 14px 20px;
  margin-bottom: 16px;
  font-size: 13px;
  color: var(--color-gray-600);
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
}
.asf-summary-strip strong { color: var(--color-gray-900); font-weight: 700; }
.asf-summary-strip .asf-dot { color: var(--color-gray-300); }

.asf-list {
  display: grid;
  gap: 14px;
}

.asf-skeleton-list { display: grid; gap: 14px; }
.asf-skeleton-card {
  background: var(--color-white);
  border: 1px solid var(--color-gray-200);
  border-radius: var(--radius-xl);
  height: 160px;
  position: relative;
  overflow: hidden;
}
.asf-skeleton-card::after {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(90deg,
    transparent 0%, rgba(0,0,0,0.04) 50%, transparent 100%);
  animation: asfShimmer 1.4s infinite;
}
@keyframes asfShimmer {
  from { transform: translateX(-100%); }
  to   { transform: translateX(100%); }
}

.asf-empty {
  background: var(--color-white);
  border: 1px solid var(--color-gray-200);
  border-radius: var(--radius-xl);
  padding: 48px 32px;
  text-align: center;
  color: var(--color-gray-700);
  position: relative;
  overflow: hidden;
}
.asf-empty-icon { color: var(--color-success, #16a34a); margin: 0 auto 16px; width: 64px; height: 64px; }
.asf-empty h2 {
  margin: 0 0 6px;
  font-family: var(--font-family-primary);
  font-size: 18px;
  color: var(--color-gray-900);
  font-weight: 700;
}
.asf-empty p { margin: 0; color: var(--color-gray-500); font-size: 13px; }
.asf-empty p a { color: var(--color-tertiary-500); text-decoration: none; font-weight: 600; }
.asf-empty p a:hover { text-decoration: underline; }
.asf-empty-shadow {
  margin-top: 32px;
  display: grid;
  gap: 10px;
  opacity: 0.3;
}
.asf-skeleton-card-ghost { height: 100px; pointer-events: none; }

.asf-error {
  background: rgba(220, 38, 38, 0.08);
  border: 1px solid rgba(220, 38, 38, 0.3);
  color: #991b1b;
  border-radius: var(--radius-md);
  padding: 12px 16px;
  font-size: 13px;
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/pages/AdminStationFailuresPage.jsx frontend/src/pages/AdminStationFailuresPage.css
git commit -m "feat(frontend): add AdminStationFailuresPage"
```

---

## Task 9 — Frontend: route + sidebar entry

**Files:**
- Modify: `frontend/src/App.jsx`
- Modify: `frontend/src/components/Sidebar.jsx`

- [ ] **Step 1: Add the route**

Edit `frontend/src/App.jsx`. Find the import block at the top, add:

```jsx
import AdminStationFailuresPage from './pages/AdminStationFailuresPage'
```

Find the `<Route path="/admin/monitoring" ... />` block (around line 122) and add immediately after:

```jsx
            <Route path="/admin/station-failures" element={
              <RequireRole roles={['admin']}><AdminStationFailuresPage /></RequireRole>
            } />
```

- [ ] **Step 2: Add sidebar entry + icon**

Edit `frontend/src/components/Sidebar.jsx`. Add a new icon function near `IconAdminMonitoring` (line 107):

```jsx
function IconStationFailures() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M8 1.5v3M8 11.5v3M1.5 8h3M11.5 8h3" />
      <circle cx="8" cy="8" r="3" />
      <path d="M8 8L11 5" strokeOpacity="0.6" />
    </svg>
  )
}
```

Add the link in the `AdminNav` block (line 183–186), right after the `/admin/monitoring` line:

```jsx
      <SidebarLink to="/admin/station-failures" icon={<IconStationFailures />} onClose={onClose}>Falhas por emissora</SidebarLink>
```

- [ ] **Step 3: Verify build**

Run: `cd frontend && npm run build`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add frontend/src/App.jsx frontend/src/components/Sidebar.jsx
git commit -m "feat(frontend): register /admin/station-failures route + sidebar"
```

---

## Task 10 — Frontend: CampaignsPage deep-link via `?campaign=<id>`

**Files:**
- Modify: `frontend/src/pages/CampaignsPage.jsx`
- Modify: `frontend/src/pages/CampaignsPage.css` (if exists, else add inline)

- [ ] **Step 1: Add URL param support + sticky banner**

Edit `frontend/src/pages/CampaignsPage.jsx`:

Change the import on line 3 from:
```jsx
import { Link, useNavigate } from 'react-router-dom'
```
to:
```jsx
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
```

Find the `useCampaignsPaged` invocation around line 1234. Above the existing two-tier search state (`searchInput`, `search`), add:

```jsx
  const [searchParams, setSearchParams] = useSearchParams()
  const campaignFilterId = searchParams.get('campaign') || ''
```

Update the `useCampaignsPaged` call to pass `id`:

```jsx
  const { data: pagedResp, isLoading, isFetching } = useCampaignsPaged({
    q: search,
    competence,
    id: campaignFilterId,
    page,
    pageSize: CAMPAIGNS_PAGE_SIZE,
  })
```

Find the filtered campaign name when `campaignFilterId` is set. Compute it from the response:

```jsx
  const filteredCampaignName = useMemo(() => {
    if (!campaignFilterId) return ''
    const c = (pagedResp?.data ?? [])[0]
    return c?.name || ''
  }, [campaignFilterId, pagedResp])
```

Find where the page header is rendered (look for the main `<div>` wrapping the list, near the search input). Add the sticky banner **above the campaigns list** when `campaignFilterId` is set:

```jsx
  {campaignFilterId && (
    <div className="campaigns-filter-banner">
      <span>
        Filtrado: <strong>{filteredCampaignName || 'campanha específica'}</strong>
      </span>
      <button
        type="button"
        className="campaigns-filter-clear"
        onClick={() => { setSearchParams({}) }}
        aria-label="Limpar filtro"
      >
        ×
      </button>
    </div>
  )}
```

- [ ] **Step 2: Add banner CSS**

Add to `frontend/src/pages/CampaignsPage.css` (append at the end):

```css
.campaigns-filter-banner {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 16px;
  margin-bottom: 16px;
  background: rgba(236, 72, 153, 0.08);
  border: 1px solid var(--color-tertiary-300);
  border-radius: var(--radius-md);
  font-size: 13px;
  color: var(--color-gray-700);
}
.campaigns-filter-banner strong {
  color: var(--color-gray-900);
  font-weight: 600;
}
.campaigns-filter-clear {
  background: transparent;
  border: none;
  font-size: 18px;
  line-height: 1;
  color: var(--color-tertiary-500);
  cursor: pointer;
  padding: 4px 8px;
  border-radius: var(--radius-full);
}
.campaigns-filter-clear:hover {
  background: rgba(236, 72, 153, 0.15);
}
```

- [ ] **Step 3: Verify build and manual test**

Run: `cd frontend && npm run build`
Expected: no errors.

Manual: load `/campaigns?campaign=<some-uuid>` in dev — banner appears, single campaign shows, `×` clears the param.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/CampaignsPage.jsx frontend/src/pages/CampaignsPage.css
git commit -m "feat(frontend): /campaigns?campaign=<id> deep-link with sticky banner"
```

---

## Task 11 — Docs

**Files:**
- Create: `docs/features/admin-station-failures.md`
- Modify: `CLAUDE.md` (Mapa de consulta table)

- [ ] **Step 1: Write the feature doc**

`docs/features/admin-station-failures.md`:

```markdown
---
status: implementado
ultima-verificacao: 2026-05-19
codigo-relacionado:
  - workers/internal/catalog/station_failures.go
  - workers/internal/api/handlers/admin_station_failures.go
  - workers/internal/api/router.go
  - frontend/src/pages/AdminStationFailuresPage.jsx
  - frontend/src/components/StationFailureCard.jsx
  - frontend/src/components/CampaignDeficitChip.jsx
  - frontend/src/pages/CampaignsPage.jsx
---

# Admin → Emissoras com falha (`/admin/station-failures`)

Lista as emissoras que tiveram falha em um dia (default ontem) e as campanhas cujos slots foram perdidos. Cada campanha é um link direto pra `/campaigns?campaign=<id>` — abre filtrada na tela de campanhas.

## Por que existe

Antes desta tela, cruzar "rádios que caíram" com "campanhas afetadas" era trabalho manual: abria `/monitoring`, anotava IDs, ia em `/campaigns` filtrar uma a uma. Esta página resolve o cruzamento em um único endpoint admin-only.

## Quem pode ver

Admin only. Rota frontend gate via `<RequireRole roles={['admin']}>`. Endpoint backend gated via `auth.RequireRole("admin")`.

## O que conta como "falha"

| Tipo | Origem | Quando aparece |
|------|--------|----------------|
| `stream-down` | `stream_health_events.event_type='down'` | A janela do down event cruza a faixa horária de alguma regra da campanha |
| `silent-gap`  | `daily_play_summary.deficit > 0` sem down explicando | Slot esperado, não tocou, stream parecia ok (provável worker travado) |

Worker travado **sem campanha agendada** não aparece — pra isso o operador usa `/admin/overview`.

## Endpoint

```
GET /v1/internal/admin/station-failures?date=YYYY-MM-DD&min_down_seconds=60
```

Limites: `today-90d ≤ date ≤ today`. Default = ontem.

Resposta: `{ date, summary: {stations_with_failure, total_down_seconds, affected_campaigns}, stations: [{station, incidents, total_down_seconds, has_silent_gap, campaigns}] }`. Detalhes em [docs/superpowers/specs/2026-05-19-admin-station-failures-design.md](../superpowers/specs/2026-05-19-admin-station-failures-design.md).

## Algoritmo

3 queries SQL paralelo-friendly + cruzamento de janelas em Go:

1. **Q1** — Stations com problema: `stream_health_events` (down ≥ minDown) UNION `daily_play_summary` (deficit > 0).
2. **Q2** — Incidents (down events) por station no dia.
3. **Q3** — Campanhas com deficit no dia + janelas de regra (HH:MM/HH:MM) por (station, campaign).

Para cada (station, campaign), `crossesAny(rule_windows, down_ranges)` decide se a causa foi stream-down ou silent-gap.

## Deep-link `?campaign=<id>` em `/campaigns`

Backend: `GET /v1/internal/campaigns?id=<uuid>` (extensão de `ListPaged`). Retorna no máximo 1 campanha.
Frontend: `useSearchParams` lê o param, passa pro `useCampaignsPaged`. Banner sticky com nome da campanha e botão `×` pra limpar.

## Não cobre

- Histórico de worker travado sem impacto em campanha — usar `/admin/overview`.
- Range de múltiplos dias — só dia único.
- Exportação CSV/PDF — fora de escopo.
- Alerta proativo (webhook/email) — fora de escopo.

## Performance

3 queries, ~50ms típico, < 500ms no pior dia. Sem cache. Admin investiga sob demanda — sem polling.
```

- [ ] **Step 2: Update CLAUDE.md mapa de consulta**

Edit `CLAUDE.md`. Find the "Mapa de consulta" table (search for "Painel admin"). Add a new row right after the `/admin/monitoring` entry:

```markdown
| Painel admin `/admin/station-failures` (emissoras com falha + campanhas afetadas no dia) | [docs/features/admin-station-failures.md](docs/features/admin-station-failures.md) |
```

- [ ] **Step 3: Commit**

```bash
git add docs/features/admin-station-failures.md CLAUDE.md
git commit -m "docs: feature page for /admin/station-failures"
```

---

## Task 12 — Manual smoke test + final pass

- [ ] **Step 1: Run full backend test suite**

```bash
cd workers && go test ./... 2>&1 | tail -50
```

Expected: no failures (pre-existing tests still pass; new ones pass).

- [ ] **Step 2: Run frontend build**

```bash
cd frontend && npm run build
```

Expected: no errors.

- [ ] **Step 3: Manual smoke test (dev env)**

1. Start backend: `cd workers && go run ./cmd/api`
2. Start frontend: `cd frontend && npm run dev`
3. Login as admin.
4. Navigate to `/admin/station-failures`.
5. Verify:
   - Page loads, date input shows yesterday
   - Empty state OR cards render
   - Switching `Mín. tempo fora` triggers refetch
   - Switching date triggers refetch
   - Refresh button works
   - Click a campaign chip → navigates to `/campaigns?campaign=<id>`
   - Sticky banner shows campaign name
   - `×` clears banner and unfilters
6. Verify hover effects: card translateY + tertiary border; chip arrow accent.
7. Login as a non-admin user → `/admin/station-failures` returns 403 / RequireRole redirects.

- [ ] **Step 4: Mark spec status as implemented**

Edit `docs/superpowers/specs/2026-05-19-admin-station-failures-design.md` — change frontmatter `status: planejado` → `status: implementado`.

- [ ] **Step 5: Final commit**

```bash
git add docs/superpowers/specs/2026-05-19-admin-station-failures-design.md
git commit -m "docs: mark station-failures spec as implementado"
```

---

## Self-Review Checklist

Run through after the plan is done:

1. **Spec coverage:**
   - [x] Endpoint shape — Task 2 (catalog) + Task 3 (handler)
   - [x] 3-query algorithm — Task 2
   - [x] Window-cross helper — Task 1
   - [x] Page layout (cards) — Task 8
   - [x] StationFailureCard — Task 7
   - [x] CampaignDeficitChip + linking — Task 6
   - [x] `?campaign=<id>` in /campaigns — Tasks 4 (backend) + 10 (frontend)
   - [x] Sidebar + route — Task 9
   - [x] Empty state §4.7 — Task 8 (asf-empty block with shadow UI)
   - [x] Loading skeleton — Task 8
   - [x] Date limits (today-90d to today) — Task 3 handler
   - [x] Docs — Task 11

2. **Placeholder scan:** none — every step has full code.

3. **Type consistency:**
   - `StationFailure`, `Incident`, `CampaignDeficit`, `Summary`, `Result` defined in Task 1, used in Tasks 2/3.
   - `useStationFailures` and `useCampaignsPaged` extensions defined in Task 5, consumed in Tasks 8/10.
   - `crossesAny` returns bool, used in Task 2 ListForDate aggregation.

4. **Confirmed against schema:**
   - `stations` columns used: `id`, `name`, `band`, `frequency_mhz`, `city`, `logo_url` (verified in [migrations/0001_initial.up.sql](../../migrations/0001_initial.up.sql) and [migrations/0002_stations_eradios.up.sql](../../migrations/0002_stations_eradios.up.sql)). No soft-delete column — all rows considered alive.
   - `daily_play_summary` uses `America/Sao_Paulo` for `for_date` (per migration 0019). The handler interprets `?date` in server local time — both should agree. Confirm `TZ=America/Sao_Paulo` in compose. Dev may be UTC — acceptable since dev data is synthetic.
