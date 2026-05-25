# Campaign Failures View Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Por campanha" mode to `/admin/station-failures` that mirrors the external `Relatório Campanha` UX. Two sub-views (daily failures, historical), drill-in drawer with full per-campaign failure breakdown, and a "PDF de cobrança" generated client-side.

**Architecture:** New backend endpoint `GET /v1/internal/admin/campaign-failures` (admin-only) with 3 modes: `?date=YYYY-MM-DD` (daily), `?mode=historical` (paginated all-time), and `/{id}` (drill-in). All queries read from the existing `daily_play_summary` view — no migration. Frontend adds a toggle to the existing page, 3 new components, and a new PDF builder.

**Tech Stack:** Go 1.22 + chi + pgx + pgxpool. React 18 + React Query + react-router-dom v6 (no frontend tests — codebase convention). PostgreSQL view `daily_play_summary` exposes `expected/in_slot/out_slot/out_date/bonus/deficit`. jsPDF + jspdf-autotable for the PDF.

**Spec:** [2026-05-25-campaign-failures-view-design.md](../specs/2026-05-25-campaign-failures-view-design.md)

---

## File Structure

**Backend (new):**
- `workers/internal/catalog/campaign_failures.go` — repo with `ListForDate`, `ListHistorical`, `Get` + pure helper `IsBonified`
- `workers/internal/catalog/campaign_failures_test.go` — pure unit tests for `IsBonified`
- `workers/internal/api/handlers/admin_campaign_failures.go` — `CampaignFailuresHandler` with `GetList(w, r)` + `GetByID(w, r)`
- `workers/internal/api/handlers/admin_campaign_failures_test.go` — validation tests (param parsing, dates, modes)

**Backend (modify):**
- `workers/internal/api/router.go` — register 2 routes under admin group, add `CampaignFailures` field to `Deps`
- `workers/cmd/api/main.go` — instantiate `catalog.NewCampaignFailures(pool)` + handler

**Frontend (new):**
- `frontend/src/components/CampaignFailureCard.jsx` — card for the daily grid
- `frontend/src/components/CampaignFailureRow.jsx` — row for the historical table
- `frontend/src/components/CampaignFailureDrawer.jsx` — side drawer with full failure breakdown
- `frontend/src/components/CampaignFailureCard.css` — shared styles for the 3 components above
- `frontend/src/utils/pdfCampaignFailure.js` — jsPDF builder for the "PDF de cobrança"

**Frontend (modify):**
- `frontend/src/api/hooks.js` — add `useCampaignFailures` and `useCampaignFailureDetail`
- `frontend/src/pages/AdminStationFailuresPage.jsx` — add toggle (Por emissora / Por campanha) + sub-tabs + render new components
- `frontend/src/pages/AdminStationFailuresPage.css` — toggle styles

**Docs:**
- `docs/features/admin-campaign-failures.md` — feature doc
- `docs/features/admin-station-failures.md` — small update on "Não cobre" section (drop "perspectiva campaign-first")
- `docs/README.md` — index entry (if applicable)
- `CLAUDE.md` — "Mapa de consulta" entry for the new tab

---

## Task 1 — Catalog: types + `IsBonified` pure helper

**Files:**
- Create: `workers/internal/catalog/campaign_failures.go`
- Create: `workers/internal/catalog/campaign_failures_test.go`

- [ ] **Step 1: Write the failing tests for `IsBonified`**

`workers/internal/catalog/campaign_failures_test.go`:

```go
package catalog

import "testing"

func TestIsBonified_NoExtras(t *testing.T) {
	if IsBonified(5, 0) {
		t.Errorf("zero extras should never be bonified")
	}
}

func TestIsBonified_ExtrasCoverDeficit(t *testing.T) {
	if !IsBonified(3, 3) {
		t.Errorf("extras == deficit should bonify")
	}
	if !IsBonified(2, 5) {
		t.Errorf("extras > deficit should bonify")
	}
}

func TestIsBonified_ExtrasInsufficient(t *testing.T) {
	if IsBonified(5, 3) {
		t.Errorf("extras < deficit should NOT bonify")
	}
}

func TestIsBonified_ZeroDeficit(t *testing.T) {
	// No deficit, irrelevant; explicit choice: not bonified because nothing
	// to compensate. UI only shows bonificada when something failed.
	if IsBonified(0, 5) {
		t.Errorf("zero deficit should not register as bonified")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd workers && go test ./internal/catalog -run TestIsBonified -v`
Expected: FAIL — `undefined: IsBonified`

- [ ] **Step 3: Implement types and `IsBonified`**

`workers/internal/catalog/campaign_failures.go`:

```go
package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IsBonified reports whether enough non-strict-in-slot plays exist to cover
// the remaining deficit. Pure function — used by all 3 endpoints (daily,
// historical, drill-in) to keep the rule consistent.
//
// Definition mirrors the external "Relatório Campanha" semantics:
//   extras    = out_slot + out_date + bonus aggregated across campaign period
//   deficit   = sum of daily_play_summary.deficit across campaign period
//   bonified  = (extras >= deficit) AND extras > 0 AND deficit > 0
//
// Zero deficit means nothing failed → never bonified (nothing to compensate).
func IsBonified(deficit, extras int) bool {
	if deficit <= 0 || extras <= 0 {
		return false
	}
	return extras >= deficit
}

// CampaignFailures repo — see ListForDate / ListHistorical / Get below.
type CampaignFailures struct {
	pool *pgxpool.Pool
}

func NewCampaignFailures(pool *pgxpool.Pool) *CampaignFailures {
	return &CampaignFailures{pool: pool}
}

// ─── Output types ────────────────────────────────────────────────

type CampaignInfo struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	ClientID      uuid.UUID `json:"client_id"`
	ClientName    string    `json:"client_name"`
	ClientLogoURL string    `json:"client_logo_url"`
	StartDate     string    `json:"start_date"` // YYYY-MM-DD
	EndDate       string    `json:"end_date"`
	Status        string    `json:"status"`
}

type StationFailureInfo struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Dial    string    `json:"dial"`
	City    string    `json:"city"`
	LogoURL string    `json:"logo_url"`
}

type CampaignFailureStation struct {
	Station             StationFailureInfo `json:"station"`
	Programmed          int                `json:"programmed"`
	Identified          int                `json:"identified"`
	Deficit             int                `json:"deficit"`
	Extras              int                `json:"extras"`
	IsBonified          bool               `json:"is_bonified"`
	FailureDaysOnDate   []string           `json:"failure_days_on_date,omitempty"` // only in modo dia
	FailureDays         []string           `json:"failure_days,omitempty"`         // only in drill-in
}

type CampaignDailyFailure struct {
	Campaign CampaignInfo             `json:"campaign"`
	Stations []CampaignFailureStation `json:"stations"`
}

type DailySummary struct {
	Campaigns    int `json:"campaigns"`
	Stations     int `json:"stations"`
	TotalDeficit int `json:"total_deficit"`
}

type DailyResult struct {
	Mode      string                 `json:"mode"` // "by_date"
	Date      string                 `json:"date"`
	Summary   DailySummary           `json:"summary"`
	Campaigns []CampaignDailyFailure `json:"campaigns"`
}

type CampaignHistoricalRow struct {
	Campaign            CampaignInfo `json:"campaign"`
	StationsWithFailure int          `json:"stations_with_failure"`
	TotalFailureDays    int          `json:"total_failure_days"`
	TotalDeficit        int          `json:"total_deficit"`
	IsFullyBonified     bool         `json:"is_fully_bonified"`
}

type HistoricalSummary struct {
	Campaigns        int `json:"campaigns"`
	TotalFailureDays int `json:"total_failure_days"`
}

type HistoricalResult struct {
	Mode      string                  `json:"mode"` // "historical"
	Summary   HistoricalSummary       `json:"summary"`
	Campaigns []CampaignHistoricalRow `json:"campaigns"`
	Page      int                     `json:"page"`
	PageSize  int                     `json:"page_size"`
	Total     int                     `json:"total"`
}

type DetailSummary struct {
	StationsWithFailure int `json:"stations_with_failure"`
	TotalFailureDays    int `json:"total_failure_days"`
	TotalDeficit        int `json:"total_deficit"`
}

type DetailResult struct {
	Campaign CampaignInfo             `json:"campaign"`
	Summary  DetailSummary            `json:"summary"`
	Stations []CampaignFailureStation `json:"stations"`
}

// ─── Helpers ─────────────────────────────────────────────────────

// makeDial joins a pre-formatted frequency string with the band into
// "102.7 FM" / "1080 AM". Both args may be empty.
func makeDial(freq, band string) string {
	if freq != "" && band != "" {
		return freq + " " + band
	}
	if freq != "" {
		return freq
	}
	return band
}

// dateOnly trims a time.Time to YYYY-MM-DD.
func dateOnly(t time.Time) string { return t.Format("2006-01-02") }

// _ used to silence unused imports during stub phase.
var _ = context.Background
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd workers && go test ./internal/catalog -run TestIsBonified -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Run the whole catalog package to verify it compiles**

Run: `cd workers && go build ./internal/catalog`
Expected: no output (success)

- [ ] **Step 6: Commit**

```bash
git add workers/internal/catalog/campaign_failures.go workers/internal/catalog/campaign_failures_test.go
git commit -m "feat(catalog): scaffold CampaignFailures repo + IsBonified pure helper"
```

---

## Task 2 — Catalog: `ListForDate` implementation

**Files:**
- Modify: `workers/internal/catalog/campaign_failures.go` (add method)

- [ ] **Step 1: Add `ListForDate` method**

Append to `workers/internal/catalog/campaign_failures.go` (and update unused-import marker):

```go
// ListForDate returns campaigns that had any (station, date) deficit > 0
// on the given local day. Stations inside each campaign are ONLY the ones
// that failed on that specific day, but their programmed/identified/deficit
// /extras numbers are aggregated across the entire campaign period (needed
// for IsBonified to mean anything).
//
// Excludes campaigns with status='cancelada'. Returns empty slice (never nil)
// when nothing matches.
func (r *CampaignFailures) ListForDate(ctx context.Context, day time.Time) (*DailyResult, error) {
	dayStr := day.Format("2006-01-02")
	result := &DailyResult{
		Mode:      "by_date",
		Date:      dayStr,
		Campaigns: []CampaignDailyFailure{},
	}

	// Q1: campaigns with at least one (station, day) deficit > 0
	rows1, err := r.pool.Query(ctx, `
SELECT DISTINCT c.id, c.name, c.start_date, c.end_date, c.status,
                cl.id, COALESCE(cl.name, '—') AS client_name,
                COALESCE(cl.logo_url, '') AS client_logo_url
FROM daily_play_summary dps
JOIN campaigns c ON c.id = dps.campaign_id
LEFT JOIN clients cl ON cl.id = c.client_id
WHERE dps.for_date = $1::date
  AND dps.deficit > 0
  AND c.status != 'cancelada'
ORDER BY c.id`, dayStr)
	if err != nil {
		return nil, fmt.Errorf("q1 campaigns: %w", err)
	}
	defer rows1.Close()

	campByID := map[uuid.UUID]*CampaignDailyFailure{}
	campIDs := []uuid.UUID{}
	for rows1.Next() {
		var info CampaignInfo
		var start, end time.Time
		if err := rows1.Scan(
			&info.ID, &info.Name, &start, &end, &info.Status,
			&info.ClientID, &info.ClientName, &info.ClientLogoURL,
		); err != nil {
			return nil, err
		}
		info.StartDate = dateOnly(start)
		info.EndDate = dateOnly(end)
		entry := &CampaignDailyFailure{Campaign: info, Stations: []CampaignFailureStation{}}
		campByID[info.ID] = entry
		campIDs = append(campIDs, info.ID)
	}
	if err := rows1.Err(); err != nil {
		return nil, err
	}
	if len(campIDs) == 0 {
		return result, nil
	}

	// Q2: stations that failed ON THE DAY, grouped by campaign
	rows2, err := r.pool.Query(ctx, `
SELECT dps.campaign_id, dps.station_id,
       s.name, COALESCE(s.band, '') AS band,
       COALESCE(to_char(s.frequency_mhz, 'FM999990.0'), '') AS freq,
       COALESCE(s.city, '') AS city, COALESCE(s.logo_url, '') AS logo_url
FROM daily_play_summary dps
JOIN stations s ON s.id = dps.station_id
WHERE dps.for_date = $1::date
  AND dps.deficit > 0
  AND dps.campaign_id = ANY($2::uuid[])
GROUP BY dps.campaign_id, dps.station_id, s.name, s.band, s.frequency_mhz, s.city, s.logo_url
ORDER BY dps.campaign_id`, dayStr, campIDs)
	if err != nil {
		return nil, fmt.Errorf("q2 stations: %w", err)
	}
	defer rows2.Close()

	type pair struct{ camp, stat uuid.UUID }
	statByPair := map[pair]*CampaignFailureStation{}
	pairs := []pair{}
	for rows2.Next() {
		var cid, sid uuid.UUID
		var name, band, freq, city, logo string
		if err := rows2.Scan(&cid, &sid, &name, &band, &freq, &city, &logo); err != nil {
			return nil, err
		}
		st := &CampaignFailureStation{
			Station: StationFailureInfo{
				ID: sid, Name: name, Dial: makeDial(freq, band),
				City: city, LogoURL: logo,
			},
			FailureDaysOnDate: []string{dayStr},
		}
		statByPair[pair{cid, sid}] = st
		pairs = append(pairs, pair{cid, sid})
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	// Collect station IDs for Q3 (de-duped)
	stationIDSet := map[uuid.UUID]struct{}{}
	for _, p := range pairs {
		stationIDSet[p.stat] = struct{}{}
	}
	stationIDs := make([]uuid.UUID, 0, len(stationIDSet))
	for id := range stationIDSet {
		stationIDs = append(stationIDs, id)
	}

	// Q3: campaign-period aggregates (programmed/identified/deficit/extras)
	rows3, err := r.pool.Query(ctx, `
SELECT dps.campaign_id, dps.station_id,
       SUM(dps.expected)::int  AS programmed,
       SUM(dps.in_slot)::int   AS identified,
       SUM(dps.deficit)::int   AS deficit,
       (SUM(dps.out_slot) + SUM(dps.out_date) + SUM(dps.bonus))::int AS extras
FROM daily_play_summary dps
WHERE dps.campaign_id = ANY($1::uuid[])
  AND dps.station_id  = ANY($2::uuid[])
GROUP BY dps.campaign_id, dps.station_id`, campIDs, stationIDs)
	if err != nil {
		return nil, fmt.Errorf("q3 aggregates: %w", err)
	}
	defer rows3.Close()
	for rows3.Next() {
		var cid, sid uuid.UUID
		var programmed, identified, deficit, extras int
		if err := rows3.Scan(&cid, &sid, &programmed, &identified, &deficit, &extras); err != nil {
			return nil, err
		}
		if st, ok := statByPair[pair{cid, sid}]; ok {
			st.Programmed = programmed
			st.Identified = identified
			st.Deficit = deficit
			st.Extras = extras
			st.IsBonified = IsBonified(deficit, extras)
		}
	}
	if err := rows3.Err(); err != nil {
		return nil, err
	}

	// Attach stations to campaigns, sort, build summary.
	for _, p := range pairs {
		if st, ok := statByPair[p]; ok {
			campByID[p.camp].Stations = append(campByID[p.camp].Stations, *st)
		}
	}

	totalStations := 0
	totalDeficit := 0
	for _, cid := range campIDs {
		entry := campByID[cid]
		// Stations inside campaign: deficit DESC, name ASC
		sortStationsByDeficit(entry.Stations)
		totalStations += len(entry.Stations)
		for _, st := range entry.Stations {
			totalDeficit += st.Deficit
		}
		result.Campaigns = append(result.Campaigns, *entry)
	}
	// Campaigns: stations.length DESC, client_name ASC
	sortCampaignsByImpact(result.Campaigns)

	result.Summary = DailySummary{
		Campaigns:    len(result.Campaigns),
		Stations:     totalStations,
		TotalDeficit: totalDeficit,
	}
	return result, nil
}

// sortStationsByDeficit sorts in-place: deficit DESC, station.name ASC.
func sortStationsByDeficit(ss []CampaignFailureStation) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0; j-- {
			a, b := ss[j-1], ss[j]
			if a.Deficit < b.Deficit ||
				(a.Deficit == b.Deficit && a.Station.Name > b.Station.Name) {
				ss[j-1], ss[j] = b, a
			} else {
				break
			}
		}
	}
}

// sortCampaignsByImpact sorts in-place: stations.length DESC, client_name ASC.
func sortCampaignsByImpact(cs []CampaignDailyFailure) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0; j-- {
			a, b := cs[j-1], cs[j]
			la, lb := len(a.Stations), len(b.Stations)
			if la < lb || (la == lb && a.Campaign.ClientName > b.Campaign.ClientName) {
				cs[j-1], cs[j] = b, a
			} else {
				break
			}
		}
	}
}
```

Then remove the now-unused `_ = context.Background` line at the bottom (the import is now real).

- [ ] **Step 2: Verify it compiles**

Run: `cd workers && go build ./internal/catalog`
Expected: success

- [ ] **Step 3: Verify existing tests still pass**

Run: `cd workers && go test ./internal/catalog -run TestIsBonified -v`
Expected: PASS (4 tests)

- [ ] **Step 4: Commit**

```bash
git add workers/internal/catalog/campaign_failures.go
git commit -m "feat(catalog): CampaignFailures.ListForDate (3 SQL + Go aggregation)"
```

---

## Task 3 — Catalog: `ListHistorical` implementation

**Files:**
- Modify: `workers/internal/catalog/campaign_failures.go` (add method)

- [ ] **Step 1: Add the method**

Append to `workers/internal/catalog/campaign_failures.go`:

```go
// ListHistorical returns one row per non-cancelled campaign that has at
// least one (station, date) with deficit > 0 anywhere in its lifetime.
// Paginated by page (1-based) and pageSize (clamped 1..200, default 50).
func (r *CampaignFailures) ListHistorical(ctx context.Context, page, pageSize int) (*HistoricalResult, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	offset := (page - 1) * pageSize

	result := &HistoricalResult{
		Mode:      "historical",
		Campaigns: []CampaignHistoricalRow{},
		Page:      page,
		PageSize:  pageSize,
	}

	// Q1: paginated list of campaigns with any deficit + per-campaign summary
	rows, err := r.pool.Query(ctx, `
WITH agg AS (
  SELECT dps.campaign_id,
         COUNT(DISTINCT dps.station_id) FILTER (WHERE dps.deficit > 0) AS stations_with_failure,
         COUNT(*) FILTER (WHERE dps.deficit > 0) AS total_failure_days,
         SUM(dps.deficit)::int AS total_deficit,
         SUM(dps.out_slot + dps.out_date + dps.bonus)::int AS total_extras
  FROM daily_play_summary dps
  GROUP BY dps.campaign_id
  HAVING SUM(dps.deficit) > 0
)
SELECT c.id, c.name, c.start_date, c.end_date, c.status,
       cl.id, COALESCE(cl.name, '—') AS client_name,
       COALESCE(cl.logo_url, '') AS client_logo_url,
       a.stations_with_failure::int,
       a.total_failure_days::int,
       a.total_deficit::int,
       a.total_extras::int
FROM agg a
JOIN campaigns c ON c.id = a.campaign_id
LEFT JOIN clients cl ON cl.id = c.client_id
WHERE c.status != 'cancelada'
ORDER BY a.stations_with_failure DESC, a.total_deficit DESC, c.name ASC
LIMIT $1 OFFSET $2`, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("q1 historical: %w", err)
	}
	defer rows.Close()

	totalFailureDaysSum := 0
	for rows.Next() {
		var info CampaignInfo
		var start, end time.Time
		var stationsWithFailure, totalFailureDays, totalDeficit, totalExtras int
		if err := rows.Scan(
			&info.ID, &info.Name, &start, &end, &info.Status,
			&info.ClientID, &info.ClientName, &info.ClientLogoURL,
			&stationsWithFailure, &totalFailureDays, &totalDeficit, &totalExtras,
		); err != nil {
			return nil, err
		}
		info.StartDate = dateOnly(start)
		info.EndDate = dateOnly(end)
		result.Campaigns = append(result.Campaigns, CampaignHistoricalRow{
			Campaign:            info,
			StationsWithFailure: stationsWithFailure,
			TotalFailureDays:    totalFailureDays,
			TotalDeficit:        totalDeficit,
			IsFullyBonified:     IsBonified(totalDeficit, totalExtras),
		})
		totalFailureDaysSum += totalFailureDays
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Q2: total count (for pagination UI). Must mirror Q1's HAVING + WHERE.
	if err := r.pool.QueryRow(ctx, `
WITH agg AS (
  SELECT dps.campaign_id
  FROM daily_play_summary dps
  GROUP BY dps.campaign_id
  HAVING SUM(dps.deficit) > 0
)
SELECT COUNT(*) FROM agg a
JOIN campaigns c ON c.id = a.campaign_id
WHERE c.status != 'cancelada'`).Scan(&result.Total); err != nil {
		return nil, fmt.Errorf("q2 count: %w", err)
	}

	result.Summary = HistoricalSummary{
		Campaigns:        result.Total,
		TotalFailureDays: totalFailureDaysSum, // sum over current page only — full sum would need another query
	}
	return result, nil
}
```

- [ ] **Step 2: Verify compile + tests**

Run: `cd workers && go build ./internal/catalog && go test ./internal/catalog -run TestIsBonified -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add workers/internal/catalog/campaign_failures.go
git commit -m "feat(catalog): CampaignFailures.ListHistorical with pagination"
```

---

## Task 4 — Catalog: `Get` drill-in implementation

**Files:**
- Modify: `workers/internal/catalog/campaign_failures.go` (add method + sentinel)

- [ ] **Step 1: Update imports to include `errors` + `pgx`**

In `workers/internal/catalog/campaign_failures.go`, replace the existing `import (...)` block with:

```go
import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)
```

- [ ] **Step 2: Add `ErrCampaignNotFound` sentinel + `Get` method**

Append to `workers/internal/catalog/campaign_failures.go`:

```go
var ErrCampaignNotFound = errors.New("campaign not found or cancelled")
```

Then append the method:

```go
// Get returns the per-campaign failure breakdown for the drill-in view.
// 404-equivalent: returns ErrCampaignNotFound when the campaign doesn't exist
// or is cancelled.
func (r *CampaignFailures) Get(ctx context.Context, id uuid.UUID) (*DetailResult, error) {
	var info CampaignInfo
	var start, end time.Time
	err := r.pool.QueryRow(ctx, `
SELECT c.id, c.name, c.start_date, c.end_date, c.status,
       cl.id, COALESCE(cl.name, '—') AS client_name,
       COALESCE(cl.logo_url, '') AS client_logo_url
FROM campaigns c
LEFT JOIN clients cl ON cl.id = c.client_id
WHERE c.id = $1 AND c.status != 'cancelada'`, id).Scan(
		&info.ID, &info.Name, &start, &end, &info.Status,
		&info.ClientID, &info.ClientName, &info.ClientLogoURL,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCampaignNotFound
		}
		return nil, fmt.Errorf("query campaign: %w", err)
	}
	info.StartDate = dateOnly(start)
	info.EndDate = dateOnly(end)

	result := &DetailResult{
		Campaign: info,
		Stations: []CampaignFailureStation{},
	}

	rows, err := r.pool.Query(ctx, `
SELECT dps.station_id,
       s.name, COALESCE(s.band, '') AS band,
       COALESCE(to_char(s.frequency_mhz, 'FM999990.0'), '') AS freq,
       COALESCE(s.city, '') AS city, COALESCE(s.logo_url, '') AS logo_url,
       SUM(dps.expected)::int  AS programmed,
       SUM(dps.in_slot)::int   AS identified,
       SUM(dps.deficit)::int   AS deficit,
       (SUM(dps.out_slot) + SUM(dps.out_date) + SUM(dps.bonus))::int AS extras,
       COALESCE(
         array_agg(DISTINCT dps.for_date::text ORDER BY dps.for_date::text)
           FILTER (WHERE dps.deficit > 0),
         ARRAY[]::text[]
       ) AS failure_days,
       COUNT(*) FILTER (WHERE dps.deficit > 0) AS failure_day_count
FROM daily_play_summary dps
JOIN stations s ON s.id = dps.station_id
WHERE dps.campaign_id = $1
GROUP BY dps.station_id, s.name, s.band, s.frequency_mhz, s.city, s.logo_url
HAVING COUNT(*) FILTER (WHERE dps.deficit > 0) > 0
ORDER BY COUNT(*) FILTER (WHERE dps.deficit > 0) DESC, s.name ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("query stations: %w", err)
	}
	defer rows.Close()

	totalDeficit := 0
	totalFailureDays := 0
	for rows.Next() {
		var sid uuid.UUID
		var name, band, freq, city, logo string
		var programmed, identified, deficit, extras, failureDayCount int
		var failureDays []string
		if err := rows.Scan(
			&sid, &name, &band, &freq, &city, &logo,
			&programmed, &identified, &deficit, &extras,
			&failureDays, &failureDayCount,
		); err != nil {
			return nil, err
		}
		result.Stations = append(result.Stations, CampaignFailureStation{
			Station: StationFailureInfo{
				ID: sid, Name: name, Dial: makeDial(freq, band),
				City: city, LogoURL: logo,
			},
			Programmed:  programmed,
			Identified:  identified,
			Deficit:     deficit,
			Extras:      extras,
			IsBonified:  IsBonified(deficit, extras),
			FailureDays: failureDays,
		})
		totalDeficit += deficit
		totalFailureDays += failureDayCount
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result.Summary = DetailSummary{
		StationsWithFailure: len(result.Stations),
		TotalFailureDays:    totalFailureDays,
		TotalDeficit:        totalDeficit,
	}
	return result, nil
}
```

- [ ] **Step 3: Verify compile + tests**

Run: `cd workers && go build ./internal/catalog && go test ./internal/catalog -run TestIsBonified -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add workers/internal/catalog/campaign_failures.go
git commit -m "feat(catalog): CampaignFailures.Get (drill-in) with NotFound sentinel"
```

---

## Task 5 — Handler: validation + routing

**Files:**
- Create: `workers/internal/api/handlers/admin_campaign_failures.go`
- Create: `workers/internal/api/handlers/admin_campaign_failures_test.go`

- [ ] **Step 1: Write the failing handler validation tests**

`workers/internal/api/handlers/admin_campaign_failures_test.go`:

```go
package handlers

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestCampaignFailures_DateValidation(t *testing.T) {
	h := &CampaignFailuresHandler{}

	cases := []struct {
		name string
		url  string
		want int
	}{
		{"valid_yesterday", "/?date=" + time.Now().AddDate(0, 0, -1).Format("2006-01-02"), 200},
		{"future", "/?date=" + time.Now().AddDate(0, 0, 5).Format("2006-01-02"), 400},
		{"too_old", "/?date=" + time.Now().AddDate(0, 0, -100).Format("2006-01-02"), 400},
		{"malformed", "/?date=garbage", 400},
		{"default_empty", "/", 200},
		{"mode_historical", "/?mode=historical", 200},
		{"mode_invalid", "/?mode=garbage", 400},
		{"mode_and_date_conflict", "/?mode=historical&date=2026-05-01", 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.url, nil)
			rr := httptest.NewRecorder()
			h.GetList(rr, req)
			if tc.want != rr.Code {
				t.Errorf("%s: want %d, got %d body=%s", tc.name, tc.want, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestCampaignFailures_HistoricalPaginationClamps(t *testing.T) {
	h := &CampaignFailuresHandler{}
	// Repo nil → empty 200. We only confirm parsing accepts edge values.
	for _, qs := range []string{
		"mode=historical&page=0",
		"mode=historical&page=999",
		"mode=historical&page_size=-5",
		"mode=historical&page_size=99999",
		"mode=historical&page=abc&page_size=def",
	} {
		req := httptest.NewRequest("GET", "/?"+qs, nil)
		rr := httptest.NewRecorder()
		h.GetList(rr, req)
		if rr.Code != 200 {
			t.Errorf("%q: want 200, got %d", qs, rr.Code)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd workers && go test ./internal/api/handlers -run TestCampaignFailures -v`
Expected: FAIL — `undefined: CampaignFailuresHandler`

- [ ] **Step 3: Implement the handler**

`workers/internal/api/handlers/admin_campaign_failures.go`:

```go
package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
)

// CampaignFailuresRepo abstracts the catalog repo so the handler is testable
// without a live DB.
type CampaignFailuresRepo interface {
	ListForDate(ctx context.Context, day time.Time) (*catalog.DailyResult, error)
	ListHistorical(ctx context.Context, page, pageSize int) (*catalog.HistoricalResult, error)
	Get(ctx context.Context, id uuid.UUID) (*catalog.DetailResult, error)
}

// CampaignFailuresHandler powers /v1/internal/admin/campaign-failures.
// Mode is implicit: date param means by_date, mode=historical means historical.
// /{id} is a separate route, handled by GetByID.
//
// Auth: admin-only (mounted under admin group in router.go).
type CampaignFailuresHandler struct {
	Repo CampaignFailuresRepo
	Log  *zap.Logger
}

// GetList serves /admin/campaign-failures.
func (h *CampaignFailuresHandler) GetList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mode := q.Get("mode")
	dateStr := q.Get("date")

	// Param conflict: explicit historical mode + explicit date → 400.
	if mode == "historical" && dateStr != "" {
		http.Error(w, "invalid params: mode=historical does not accept date", http.StatusBadRequest)
		return
	}

	if mode == "historical" {
		page := parseIntDefault(q.Get("page"), 1)
		pageSize := parseIntDefault(q.Get("page_size"), 50)

		if h.Repo == nil {
			writeJSON(w, http.StatusOK, &catalog.HistoricalResult{
				Mode: "historical", Campaigns: []catalog.CampaignHistoricalRow{},
				Page: page, PageSize: pageSize, Total: 0,
			})
			return
		}
		res, err := h.Repo.ListHistorical(r.Context(), page, pageSize)
		if err != nil {
			h.logErr("campaign_failures_historical_failed", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}

	if mode != "" && mode != "by_date" {
		http.Error(w, "invalid mode: expected 'by_date' or 'historical'", http.StatusBadRequest)
		return
	}

	if dateStr == "" {
		dateStr = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	}
	day, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
	if err != nil {
		http.Error(w, "invalid date: expected YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	today := time.Now().In(time.Local)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.Local)
	if day.After(today) {
		http.Error(w, "invalid date: future not supported", http.StatusBadRequest)
		return
	}
	if today.Sub(day) > 90*24*time.Hour {
		http.Error(w, "invalid date: older than 90 days", http.StatusBadRequest)
		return
	}

	if h.Repo == nil {
		writeJSON(w, http.StatusOK, &catalog.DailyResult{
			Mode: "by_date", Date: day.Format("2006-01-02"),
			Campaigns: []catalog.CampaignDailyFailure{},
		})
		return
	}
	res, err := h.Repo.ListForDate(r.Context(), day)
	if err != nil {
		h.logErr("campaign_failures_daily_failed", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GetByID serves /admin/campaign-failures/{id} (drill-in).
func (h *CampaignFailuresHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "id")
	id, err := uuid.Parse(raw)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if h.Repo == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	res, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, catalog.ErrCampaignNotFound) {
			http.Error(w, "campaign not found", http.StatusNotFound)
			return
		}
		h.logErr("campaign_failures_detail_failed", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *CampaignFailuresHandler) logErr(msg string, err error) {
	if h.Log != nil {
		h.Log.Error(msg, zap.Error(err))
	}
}

// parseIntDefault returns def when s is empty / malformed.
func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd workers && go test ./internal/api/handlers -run TestCampaignFailures -v`
Expected: PASS (all cases — 8 sub-tests for `DateValidation`, 5 for `HistoricalPaginationClamps`)

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/admin_campaign_failures.go workers/internal/api/handlers/admin_campaign_failures_test.go
git commit -m "feat(api): CampaignFailuresHandler with date/mode/id validation"
```

---

## Task 6 — Router + main wiring

**Files:**
- Modify: `workers/internal/api/router.go`
- Modify: `workers/cmd/api/main.go`

- [ ] **Step 1: Add `CampaignFailures` field to `Deps`**

In `workers/internal/api/router.go`, find the `Deps` struct (around line 28). Add the new field right after `StationFailures`:

```go
StationFailures       *handlers.StationFailuresHandler
CampaignFailures      *handlers.CampaignFailuresHandler  // NEW
Webhooks              *handlers.WebhooksHandler
```

- [ ] **Step 2: Register the routes under the admin group**

In `workers/internal/api/router.go`, find the block that registers `/admin/station-failures` (around line 366). Add immediately after it:

```go
// /admin/campaign-failures — same intent as station-failures but
// pivoted by campaign. Two list modes (date / historical) plus a
// drill-in by campaign id. Docs em docs/features/admin-campaign-failures.md.
if d.CampaignFailures != nil {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole("admin"))
		r.Get("/admin/campaign-failures",      d.CampaignFailures.GetList)
		r.Get("/admin/campaign-failures/{id}", d.CampaignFailures.GetByID)
	})
}
```

- [ ] **Step 3: Wire the repo + handler in `main.go`**

In `workers/cmd/api/main.go`, find the `StationFailures` wiring (around line 316). Add immediately after:

```go
StationFailures: &handlers.StationFailuresHandler{
	Repo: catalog.NewStationFailures(pool),
	Log:  logger,
},
CampaignFailures: &handlers.CampaignFailuresHandler{
	Repo: catalog.NewCampaignFailures(pool),
	Log:  logger,
},
```

- [ ] **Step 4: Build + run all tests**

Run: `cd workers && go build ./... && go test ./...`
Expected: PASS for all tests, no compile errors.

- [ ] **Step 5: Smoke the routes with curl**

(Optional but recommended.) Start the API locally and hit each endpoint with an admin token:

```bash
# In one shell:
cd workers && go run ./cmd/api

# In another (replace TOKEN):
curl -s -H "Authorization: Bearer TOKEN" "http://localhost:8080/v1/internal/admin/campaign-failures" | head
curl -s -H "Authorization: Bearer TOKEN" "http://localhost:8080/v1/internal/admin/campaign-failures?mode=historical" | head
curl -s -H "Authorization: Bearer TOKEN" "http://localhost:8080/v1/internal/admin/campaign-failures/$(uuidgen)" | head
```

Expected: 200/empty arrays (no data yet), 404 for random uuid.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/api/router.go workers/cmd/api/main.go
git commit -m "feat(api): wire /admin/campaign-failures routes"
```

---

## Task 7 — Frontend hooks

**Files:**
- Modify: `frontend/src/api/hooks.js`

- [ ] **Step 1: Add the two new hooks**

Find the existing `useStationFailures` block (~line 115) in `frontend/src/api/hooks.js`. Add immediately below it:

```js
// Admin — visão "Por campanha" (mesma página /admin/station-failures, modo
// alternativo). Dois sub-modos via param `mode`:
//   - 'by_date':    grade de cards por campanha, falhas no dia
//   - 'historical': tabela paginada de campanhas com qualquer falha
// Doc em docs/features/admin-campaign-failures.md.
export function useCampaignFailures({ mode = 'by_date', date, page = 1, pageSize = 50 } = {}) {
  const params = {}
  if (mode === 'historical') {
    params.mode = 'historical'
    params.page = page
    params.page_size = pageSize
  } else if (date) {
    params.date = date
  }
  return useQuery({
    queryKey: ['campaign-failures', mode, date, page, pageSize],
    queryFn: () => api.get('/admin/campaign-failures', { params }).then(r => r.data),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    keepPreviousData: true, // smoother pagination
  })
}

// Drill-in: detalhe completo de UMA campanha (todas emissoras com falha em
// qualquer dia da vigência). 404 quando a campanha está cancelada ou não
// existe — o drawer trata isso e mostra "campanha não encontrada".
export function useCampaignFailureDetail(id) {
  return useQuery({
    queryKey: ['campaign-failure-detail', id],
    queryFn: () => api.get(`/admin/campaign-failures/${id}`).then(r => r.data),
    enabled: Boolean(id),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  })
}
```

- [ ] **Step 2: Confirm the file still parses**

Run: `cd frontend && npx vite build 2>&1 | tail -20`
Expected: build succeeds (or fails on unrelated reasons — confirm no parse errors mentioning hooks.js).

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "feat(frontend): useCampaignFailures + useCampaignFailureDetail hooks"
```

---

## Task 8 — Frontend: `CampaignFailureCard` component

**Files:**
- Create: `frontend/src/components/CampaignFailureCard.jsx`
- Create: `frontend/src/components/CampaignFailureCard.css`

- [ ] **Step 1: Write the component**

`frontend/src/components/CampaignFailureCard.jsx`:

```jsx
import StationAvatar from './StationAvatar'
import ClientLogo from './ClientLogo'
import './CampaignFailureCard.css'

const MAX_STATIONS_PREVIEW = 6

function pct(identified, programmed) {
  if (!programmed) return 0
  return Math.round((identified / programmed) * 100)
}

function StationRow({ station }) {
  const { station: s, programmed, identified, deficit, is_bonified } = station
  return (
    <li className="cfc-station-row">
      <StationAvatar station={s} size={28} />
      <div className="cfc-station-id">
        <span className="cfc-station-name" title={s.name}>{s.name}</span>
        <span className="cfc-station-city">{s.city || s.dial || '—'}</span>
      </div>
      <div className="cfc-station-num">
        <span className="cfc-station-num-line">
          <span className="cfc-num-ok">{identified.toLocaleString('pt-BR')}</span>
          <span className="cfc-num-sep">/</span>
          <span className="cfc-num-total">{programmed.toLocaleString('pt-BR')}</span>
          <span className="cfc-num-pct"> · {pct(identified, programmed)}%</span>
        </span>
        {is_bonified ? (
          <span className="cfc-tag cfc-tag-bonif">falhou, bonificada</span>
        ) : deficit > 0 ? (
          <span className="cfc-tag cfc-tag-deficit">faltam {deficit.toLocaleString('pt-BR')}</span>
        ) : null}
      </div>
    </li>
  )
}

export default function CampaignFailureCard({ entry, onOpen }) {
  const { campaign, stations } = entry
  const preview = stations.slice(0, MAX_STATIONS_PREVIEW)
  const remaining = stations.length - preview.length

  return (
    <article
      className="cfc-card"
      onClick={onOpen}
      role="button"
      tabIndex={0}
      onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onOpen() } }}
    >
      <header className="cfc-head">
        <ClientLogo client={{ name: campaign.client_name, logo_url: campaign.client_logo_url }} size={44} />
        <div className="cfc-head-id">
          <span className="cfc-client" title={campaign.client_name}>{campaign.client_name}</span>
          <span className="cfc-campaign" title={campaign.name}>{campaign.name}</span>
        </div>
        <div className="cfc-head-num">
          <span className="cfc-num-stations">{stations.length}</span>
          <span className="cfc-num-stations-label">
            {stations.length === 1 ? 'emissora' : 'emissoras'}
          </span>
        </div>
      </header>

      <ul className="cfc-station-list">
        {preview.map((st) => (
          <StationRow key={String(st.station.id)} station={st} />
        ))}
      </ul>

      <footer className="cfc-foot">
        {remaining > 0 && (
          <span className="cfc-more">+{remaining} {remaining === 1 ? 'emissora' : 'emissoras'} no detalhe</span>
        )}
        <span className="cfc-cta">Ver detalhes →</span>
      </footer>
    </article>
  )
}
```

> If `ClientLogo` doesn't exist yet, check `frontend/src/components/` — there's `StationAvatar` there with the fallback-initials pattern. Create `ClientLogo.jsx` only if needed (copy the same pattern). Quick way: `grep -r ClientLogo frontend/src/`. If absent, inline a small `<div>` with logo + fallback initial. See StationAvatar.jsx for the visual pattern.

- [ ] **Step 2: Quick check whether `ClientLogo` already exists**

Run: `grep -l "export default function ClientLogo" frontend/src/components/`

If found: use it (no extra work).
If NOT found: replace the `<ClientLogo ... />` element in the JSX above with this minimal inline:

```jsx
{campaign.client_logo_url ? (
  <img
    src={campaign.client_logo_url}
    alt={campaign.client_name}
    className="cfc-client-logo"
    onError={(e) => { e.target.style.display = 'none' }}
  />
) : (
  <div className="cfc-client-logo cfc-client-logo--fallback">
    {(campaign.client_name || '?').charAt(0).toUpperCase()}
  </div>
)}
```

And drop the `import ClientLogo` line.

- [ ] **Step 3: Write the CSS**

`frontend/src/components/CampaignFailureCard.css`:

```css
/* CampaignFailureCard.css — used by 3 components (Card, Row, Drawer). Cores
 * em sync com src/index.css design tokens. */

.cfc-card {
  display: flex;
  flex-direction: column;
  gap: 0;
  background: var(--surface-1, #fff);
  border: 1px solid var(--border, #e2e8f0);
  border-radius: 16px;
  overflow: hidden;
  cursor: pointer;
  transition: border-color .15s ease, transform .15s ease, box-shadow .15s ease;
}
.cfc-card:hover {
  border-color: var(--action, #E81E75);
  box-shadow: 0 4px 18px rgba(232, 30, 117, .08);
}
.cfc-card:focus-visible {
  outline: 2px solid var(--action, #E81E75);
  outline-offset: 2px;
}

.cfc-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 16px 18px;
  border-bottom: 1px solid var(--border, #e2e8f0);
}
.cfc-client-logo {
  width: 44px; height: 44px;
  border-radius: 12px;
  object-fit: contain;
  background: var(--surface-2, #f1f5f9);
  padding: 4px;
  flex-shrink: 0;
}
.cfc-client-logo--fallback {
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 700;
  color: var(--action, #E81E75);
}
.cfc-head-id {
  flex: 1; min-width: 0;
  display: flex; flex-direction: column; gap: 1px;
}
.cfc-client {
  font-weight: 600; color: var(--text, #06055B);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.cfc-campaign {
  font-size: 13px; color: var(--text-2, #4b5563);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.cfc-head-num {
  display: flex; flex-direction: column; align-items: center;
  padding: 4px 12px;
  border-radius: 10px;
  background: rgba(239, 68, 68, .08);
}
.cfc-num-stations {
  font-weight: 700; font-size: 22px; line-height: 1;
  color: #dc2626;
}
.cfc-num-stations-label {
  font-size: 10px; color: #dc2626;
  text-transform: uppercase; letter-spacing: .04em;
}

.cfc-station-list {
  list-style: none; margin: 0; padding: 0;
}
.cfc-station-row {
  display: flex; align-items: center; gap: 10px;
  padding: 10px 18px;
  border-bottom: 1px solid var(--border, #e2e8f0);
}
.cfc-station-row:last-child { border-bottom: none; }

.cfc-station-id {
  flex: 1; min-width: 0;
  display: flex; flex-direction: column;
}
.cfc-station-name {
  font-size: 13px; color: var(--text, #06055B);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.cfc-station-city {
  font-size: 11px; color: var(--text-3, #9ca3af);
}
.cfc-station-num {
  text-align: right; flex-shrink: 0;
  font-size: 12px; line-height: 1.3;
  font-variant-numeric: tabular-nums;
}
.cfc-num-ok { color: #16a34a; font-weight: 600; }
.cfc-num-sep { color: var(--text-3, #9ca3af); margin: 0 2px; }
.cfc-num-total { color: var(--text-2, #4b5563); }
.cfc-num-pct { color: var(--text-3, #9ca3af); }
.cfc-tag {
  display: inline-block; margin-top: 2px;
  font-size: 11px; font-weight: 500;
  padding: 1px 6px; border-radius: 4px;
}
.cfc-tag-deficit { background: rgba(239, 68, 68, .12); color: #dc2626; }
.cfc-tag-bonif   { background: rgba(168, 85, 247, .12); color: #a855f7; }

.cfc-foot {
  display: flex; justify-content: space-between; align-items: center;
  padding: 10px 18px;
  background: var(--surface-2, #f9fafb);
  font-size: 12px;
}
.cfc-more { color: var(--text-3, #9ca3af); }
.cfc-cta { color: var(--action, #E81E75); font-weight: 500; }
```

- [ ] **Step 4: Verify build**

Run: `cd frontend && npx vite build 2>&1 | tail -10`
Expected: build succeeds.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/CampaignFailureCard.jsx frontend/src/components/CampaignFailureCard.css
git commit -m "feat(frontend): CampaignFailureCard component + styles"
```

---

## Task 9 — Frontend: `CampaignFailureRow` component (historical table)

**Files:**
- Create: `frontend/src/components/CampaignFailureRow.jsx`

- [ ] **Step 1: Write the component**

`frontend/src/components/CampaignFailureRow.jsx`:

```jsx
import './CampaignFailureCard.css'

const STATUS_LABEL = {
  programada: 'Programada',
  ativa:      'Ativa',
  concluida:  'Concluída',
  cancelada:  'Cancelada',
}

function fmtDateBR(yyyymmdd) {
  if (!yyyymmdd) return '—'
  const [y, m, d] = yyyymmdd.split('-')
  return `${d}/${m}/${y}`
}

export default function CampaignFailureRow({ entry, onOpen }) {
  const { campaign, stations_with_failure, total_failure_days, total_deficit, is_fully_bonified } = entry
  return (
    <tr
      className="cfr-row"
      onClick={onOpen}
      role="button"
      tabIndex={0}
      onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onOpen() } }}
    >
      <td className="cfr-cli">
        <div className="cfr-cli-stack">
          <span className="cfr-cli-name" title={campaign.client_name}>{campaign.client_name}</span>
          <span className="cfr-camp-name" title={campaign.name}>{campaign.name}</span>
          <span className="cfr-period">
            {fmtDateBR(campaign.start_date)} – {fmtDateBR(campaign.end_date)}
          </span>
        </div>
      </td>
      <td className="cfr-status">
        <span className={`cfr-status-chip cfr-status-${campaign.status}`}>
          {STATUS_LABEL[campaign.status] || campaign.status}
        </span>
      </td>
      <td className="cfr-num">{stations_with_failure}</td>
      <td className="cfr-num">{total_failure_days}</td>
      <td className="cfr-num cfr-num-deficit">{total_deficit}</td>
      <td className="cfr-bonif">
        {is_fully_bonified ? (
          <span className="cfc-tag cfc-tag-bonif">100% bonificada</span>
        ) : null}
      </td>
    </tr>
  )
}
```

- [ ] **Step 2: Append the table styles to `CampaignFailureCard.css`**

Append:

```css
/* ── Historical table row (CampaignFailureRow) ─────────────────── */
.cfr-row {
  cursor: pointer;
  transition: background .12s ease;
}
.cfr-row:hover { background: var(--surface-2, #f1f5f9); }
.cfr-row:focus-visible { outline: 2px solid var(--action, #E81E75); outline-offset: -2px; }
.cfr-row > td {
  padding: 12px 14px;
  border-bottom: 1px solid var(--border, #e2e8f0);
  vertical-align: middle;
}
.cfr-cli-stack { display: flex; flex-direction: column; min-width: 0; }
.cfr-cli-name { font-weight: 600; color: var(--text, #06055B); }
.cfr-camp-name { font-size: 13px; color: var(--text-2, #4b5563); }
.cfr-period { font-size: 11px; color: var(--text-3, #9ca3af); margin-top: 2px; }

.cfr-status-chip {
  display: inline-block; padding: 2px 8px; border-radius: 999px;
  font-size: 11px; font-weight: 500;
}
.cfr-status-ativa      { background: rgba(34,197,94,.12); color: #16a34a; }
.cfr-status-programada { background: rgba(59,130,246,.12); color: #2563eb; }
.cfr-status-concluida  { background: rgba(107,114,128,.15); color: #4b5563; }
.cfr-status-cancelada  { background: rgba(239,68,68,.12); color: #dc2626; }

.cfr-num { text-align: right; font-variant-numeric: tabular-nums; font-weight: 600; }
.cfr-num-deficit { color: #dc2626; }
.cfr-bonif { width: 130px; }
```

- [ ] **Step 3: Verify build**

Run: `cd frontend && npx vite build 2>&1 | tail -10`
Expected: build succeeds.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/CampaignFailureRow.jsx frontend/src/components/CampaignFailureCard.css
git commit -m "feat(frontend): CampaignFailureRow (historical table)"
```

---

## Task 10 — Frontend: `CampaignFailureDrawer` component

**Files:**
- Create: `frontend/src/components/CampaignFailureDrawer.jsx`

- [ ] **Step 1: Write the component**

`frontend/src/components/CampaignFailureDrawer.jsx`:

```jsx
import { useEffect, useMemo, useState } from 'react'
import { useCampaignFailureDetail } from '../api/hooks'
import { generateCampaignFailurePdf } from '../utils/pdfCampaignFailure'
import StationAvatar from './StationAvatar'
import './CampaignFailureCard.css'

function fmtDateBR(yyyymmdd) {
  if (!yyyymmdd) return '—'
  const [y, m, d] = yyyymmdd.split('-')
  return `${d}/${m}/${y}`
}

// Compact day chip — "24" if same month, "24/05" if crossing months.
function dayChipLabel(allDays, day) {
  // allDays YYYY-MM-DD[]; if all share same YYYY-MM, render DD; else DD/MM.
  const monthSet = new Set(allDays.map(d => d.slice(0, 7)))
  if (monthSet.size === 1) return day.slice(8, 10)
  return `${day.slice(8, 10)}/${day.slice(5, 7)}`
}

function DaysChips({ days }) {
  if (!days?.length) return null
  return (
    <div className="cfd-day-chips">
      {days.map(d => (
        <span key={d} className="cfd-day-chip" title={fmtDateBR(d)}>
          {dayChipLabel(days, d)}
        </span>
      ))}
    </div>
  )
}

function StationRow({ entry }) {
  const { station: s, programmed, identified, deficit, extras, is_bonified, failure_days } = entry
  const pct = programmed > 0 ? Math.round((identified / programmed) * 100) : 0
  return (
    <tr>
      <td className="cfd-st-cell">
        <StationAvatar station={s} size={32} />
        <div className="cfd-st-id">
          <span className="cfd-st-name">{s.name}</span>
          <span className="cfd-st-city">{s.city || s.dial || '—'}</span>
        </div>
      </td>
      <td className="cfd-num">{programmed.toLocaleString('pt-BR')}</td>
      <td className="cfd-num">
        <span className="cfc-num-ok">{identified.toLocaleString('pt-BR')}</span>
        <span className="cfd-num-pct"> ({pct}%)</span>
      </td>
      <td>
        <DaysChips days={failure_days || []} />
        {is_bonified && (
          <div className="cfd-bonif-note">Bonificada — extras: {extras}</div>
        )}
      </td>
    </tr>
  )
}

export default function CampaignFailureDrawer({ campaignId, onClose }) {
  const { data, isLoading, error } = useCampaignFailureDetail(campaignId)
  const [pdfBusy, setPdfBusy] = useState(false)

  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const campaign = data?.campaign
  const summary = data?.summary
  const stations = data?.stations ?? []

  const totalExtras = useMemo(
    () => stations.reduce((acc, s) => acc + (s.extras || 0), 0),
    [stations]
  )

  async function handleDownloadPdf() {
    if (!data) return
    setPdfBusy(true)
    try {
      await generateCampaignFailurePdf(data)
    } catch (e) {
      console.error('PDF gen failed', e)
      alert('Não foi possível gerar o PDF. Tente novamente.')
    } finally {
      setPdfBusy(false)
    }
  }

  return (
    <div className="cfd-backdrop" onClick={onClose}>
      <aside className="cfd-drawer" onClick={e => e.stopPropagation()}>
        <header className="cfd-head">
          <div className="cfd-head-id">
            <span className="cfd-eyebrow">Falhas da campanha</span>
            {campaign ? (
              <>
                <h2 className="cfd-title">{campaign.client_name}</h2>
                <p className="cfd-subtitle">{campaign.name}</p>
                <p className="cfd-meta">
                  {fmtDateBR(campaign.start_date)} – {fmtDateBR(campaign.end_date)}
                </p>
              </>
            ) : (
              <h2 className="cfd-title">Carregando…</h2>
            )}
          </div>
          <div className="cfd-head-actions">
            <button
              className="cfd-btn cfd-btn-primary"
              onClick={handleDownloadPdf}
              disabled={!data || pdfBusy}
            >
              {pdfBusy ? 'Gerando…' : 'Baixar PDF de cobrança'}
            </button>
            <button className="cfd-btn cfd-btn-close" onClick={onClose} aria-label="Fechar">×</button>
          </div>
        </header>

        {campaign?.status === 'ativa' && (
          <div className="cfd-warn">
            Campanha ainda ativa — bonificação considera execuções extras
            até agora. Pode mudar até o fim da campanha.
          </div>
        )}

        {summary && (
          <div className="cfd-kpis">
            <div className="cfd-kpi">
              <span className="cfd-kpi-num">{summary.stations_with_failure}</span>
              <span className="cfd-kpi-label">Emissoras com falha</span>
            </div>
            <div className="cfd-kpi">
              <span className="cfd-kpi-num">{summary.total_failure_days}</span>
              <span className="cfd-kpi-label">Dias com falha</span>
            </div>
            <div className="cfd-kpi">
              <span className="cfd-kpi-num cfd-kpi-num-deficit">{summary.total_deficit}</span>
              <span className="cfd-kpi-label">Déficit total</span>
            </div>
          </div>
        )}

        <div className="cfd-body">
          {isLoading ? (
            <p className="cfd-state">Carregando…</p>
          ) : error ? (
            <p className="cfd-state cfd-state-err">
              {error?.response?.status === 404
                ? 'Campanha não encontrada (pode ter sido cancelada).'
                : 'Erro ao carregar campanha.'}
            </p>
          ) : stations.length === 0 ? (
            <p className="cfd-state">Sem falhas registradas no momento.</p>
          ) : (
            <table className="cfd-table">
              <thead>
                <tr>
                  <th>Emissora</th>
                  <th className="cfd-num">Programado</th>
                  <th className="cfd-num">Veiculou</th>
                  <th>Dias com falha</th>
                </tr>
              </thead>
              <tbody>
                {stations.map(entry => (
                  <StationRow key={String(entry.station.id)} entry={entry} />
                ))}
              </tbody>
            </table>
          )}
          {totalExtras > 0 && (
            <p className="cfd-foot-note">
              Total de execuções extras no período: <strong>{totalExtras}</strong>.
            </p>
          )}
        </div>
      </aside>
    </div>
  )
}
```

- [ ] **Step 2: Append drawer styles to `CampaignFailureCard.css`**

Append:

```css
/* ── Drawer (CampaignFailureDrawer) ────────────────────────────── */
.cfd-backdrop {
  position: fixed; inset: 0;
  background: rgba(15, 23, 42, .45);
  display: flex; justify-content: flex-end;
  z-index: 80;
  animation: cfd-fade .15s ease;
}
@keyframes cfd-fade { from { opacity: 0; } to { opacity: 1; } }

.cfd-drawer {
  width: min(900px, 96vw);
  height: 100%;
  background: var(--surface-1, #fff);
  display: flex; flex-direction: column;
  overflow: hidden;
  box-shadow: -10px 0 40px rgba(0,0,0,.15);
  animation: cfd-slide .2s ease;
}
@keyframes cfd-slide { from { transform: translateX(40px); } to { transform: translateX(0); } }

.cfd-head {
  padding: 22px 28px;
  border-bottom: 1px solid var(--border, #e2e8f0);
  display: flex; justify-content: space-between; gap: 24px;
  align-items: flex-start;
}
.cfd-head-id { flex: 1; min-width: 0; }
.cfd-eyebrow {
  display: block;
  text-transform: uppercase; letter-spacing: .08em;
  font-size: 11px; font-weight: 700; color: var(--action, #E81E75);
}
.cfd-title { margin: 4px 0 2px; font-size: 22px; color: var(--text, #06055B); }
.cfd-subtitle { margin: 0; font-size: 14px; color: var(--text-2, #4b5563); }
.cfd-meta { margin: 4px 0 0; font-size: 12px; color: var(--text-3, #9ca3af); }
.cfd-head-actions { display: flex; gap: 8px; align-items: center; }

.cfd-btn {
  border: 1px solid var(--border, #e2e8f0);
  background: var(--surface-1, #fff);
  border-radius: 8px;
  padding: 8px 14px;
  font-size: 13px; cursor: pointer;
  transition: background .12s ease, color .12s ease, border-color .12s ease;
}
.cfd-btn:disabled { opacity: .55; cursor: not-allowed; }
.cfd-btn-primary {
  background: var(--action, #E81E75); color: #fff;
  border-color: var(--action, #E81E75);
  font-weight: 600;
}
.cfd-btn-primary:hover:not(:disabled) { background: #d11a6a; }
.cfd-btn-close {
  width: 36px; height: 36px;
  font-size: 22px; line-height: 1;
  padding: 0; color: var(--text-2, #4b5563);
}
.cfd-btn-close:hover { background: var(--surface-2, #f1f5f9); }

.cfd-warn {
  margin: 0 28px;
  background: rgba(234, 179, 8, .12);
  color: #a16207;
  padding: 10px 14px;
  border-radius: 8px;
  font-size: 13px;
  margin-top: 14px;
}

.cfd-kpis {
  padding: 16px 28px;
  display: grid; grid-template-columns: repeat(3, 1fr); gap: 14px;
}
.cfd-kpi {
  background: var(--surface-2, #f1f5f9);
  border-radius: 10px;
  padding: 14px 16px;
}
.cfd-kpi-num {
  display: block; font-size: 24px; font-weight: 700; line-height: 1;
  color: var(--text, #06055B);
}
.cfd-kpi-num-deficit { color: #dc2626; }
.cfd-kpi-label {
  display: block; margin-top: 6px;
  font-size: 11px; text-transform: uppercase; letter-spacing: .04em;
  color: var(--text-3, #9ca3af);
}

.cfd-body { flex: 1; overflow: auto; padding: 0 28px 28px; }
.cfd-state { padding: 40px 0; text-align: center; color: var(--text-3, #9ca3af); }
.cfd-state-err { color: #dc2626; }

.cfd-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.cfd-table thead th {
  text-align: left; padding: 10px 8px;
  font-size: 11px; text-transform: uppercase; letter-spacing: .04em;
  color: var(--text-3, #9ca3af);
  border-bottom: 1px solid var(--border, #e2e8f0);
}
.cfd-table tbody td {
  padding: 10px 8px;
  border-bottom: 1px solid var(--border, #e2e8f0);
  vertical-align: middle;
}
.cfd-table .cfd-num { text-align: right; font-variant-numeric: tabular-nums; }
.cfd-num-pct { color: var(--text-3, #9ca3af); font-size: 11px; }

.cfd-st-cell { display: flex; align-items: center; gap: 10px; min-width: 0; }
.cfd-st-id { display: flex; flex-direction: column; min-width: 0; }
.cfd-st-name { font-weight: 600; color: var(--text, #06055B); }
.cfd-st-city { font-size: 11px; color: var(--text-3, #9ca3af); }

.cfd-day-chips { display: flex; flex-wrap: wrap; gap: 4px; }
.cfd-day-chip {
  display: inline-block;
  padding: 2px 6px;
  font-size: 11px; font-weight: 700;
  font-variant-numeric: tabular-nums;
  background: rgba(239, 68, 68, .12);
  color: #dc2626;
  border-radius: 4px;
}

.cfd-bonif-note {
  font-size: 11px;
  color: #a855f7;
  margin-top: 4px;
}
.cfd-foot-note {
  margin-top: 18px;
  font-size: 12px;
  color: var(--text-3, #9ca3af);
}
```

- [ ] **Step 3: Verify build (PDF util doesn't exist yet → expect error)**

Run: `cd frontend && npx vite build 2>&1 | tail -15`
Expected: error mentioning `pdfCampaignFailure` not found. That's fine — Task 12 creates it. Commit anyway (drawer imports it, both land together in the next 2 tasks).

If you want to keep CI green between commits, temporarily stub `generateCampaignFailurePdf` with a `// TODO` placeholder. Otherwise, proceed — the chain commits together.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/CampaignFailureDrawer.jsx frontend/src/components/CampaignFailureCard.css
git commit -m "feat(frontend): CampaignFailureDrawer with KPIs + day-chips + PDF button"
```

---

## Task 11 — Frontend: PDF builder (`pdfCampaignFailure.js`)

**Files:**
- Create: `frontend/src/utils/pdfCampaignFailure.js`

- [ ] **Step 1: Write the builder**

`frontend/src/utils/pdfCampaignFailure.js`:

```js
// pdfCampaignFailure.js — "Relatório de Cobrança" PDF, gerado client-side.
//
// Distingue-se do pdfReport.js (relatório genérico de campanha pra cliente):
// este aqui foca em FALHAS — emissoras que não cumpriram o programado.
// Layout pensado pra ser enviado pra emissora ou pro comercial.
//
// Entrada: payload do GET /v1/internal/admin/campaign-failures/{id}.

import { jsPDF } from 'jspdf'
import autoTable from 'jspdf-autotable'

const TOKENS = {
  action:      [232, 30, 117],   // #E81E75
  actionLight: [252, 231, 243],
  text:        [6, 5, 91],
  text2:       [75, 85, 99],
  text3:       [156, 163, 175],
  border:      [226, 232, 240],
  surface2:    [241, 245, 249],
  white:       [255, 255, 255],
  deficit:     [220, 38, 38],
  bonified:    [168, 85, 247],
}

function pad2(n) { return String(n).padStart(2, '0') }
function fmtDateBR(yyyymmdd) {
  if (!yyyymmdd) return '—'
  const [y, m, d] = String(yyyymmdd).split('-')
  return `${d}/${m}/${y}`
}
function fmtNow() {
  const d = new Date()
  return `${pad2(d.getDate())}/${pad2(d.getMonth()+1)}/${d.getFullYear()} ${pad2(d.getHours())}:${pad2(d.getMinutes())}`
}
function slugify(s) {
  return String(s || 'campanha')
    .toLowerCase()
    .normalize('NFD').replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60) || 'campanha'
}
function stamp() {
  const d = new Date()
  return `${d.getFullYear()}${pad2(d.getMonth()+1)}${pad2(d.getDate())}`
}

let _logoPromise = null
async function loadLogoDataURL() {
  if (_logoPromise) return _logoPromise
  _logoPromise = (async () => {
    try {
      const resp = await fetch('/E-monitor%20logo.png')
      if (!resp.ok) return null
      const blob = await resp.blob()
      return await new Promise((resolve) => {
        const fr = new FileReader()
        fr.onload = () => resolve(fr.result)
        fr.onerror = () => resolve(null)
        fr.readAsDataURL(blob)
      })
    } catch { return null }
  })()
  return _logoPromise
}

// Day chips: render as "24, 26, 29" for compactness in PDF cell.
function fmtDaysList(days) {
  if (!days?.length) return '—'
  const monthSet = new Set(days.map(d => d.slice(0, 7)))
  if (monthSet.size === 1) {
    return days.map(d => d.slice(8, 10)).join(', ')
  }
  return days.map(d => `${d.slice(8, 10)}/${d.slice(5, 7)}`).join(', ')
}

export async function generateCampaignFailurePdf(payload) {
  const { campaign, summary, stations = [] } = payload
  const doc = new jsPDF({ unit: 'pt', format: 'a4' })
  const pageW = doc.internal.pageSize.getWidth()
  const margin = 36
  const logo = await loadLogoDataURL()

  // ─── Header band ────────────────────────────────────────────────
  doc.setFillColor(...TOKENS.action)
  doc.rect(0, 0, pageW, 8, 'F')

  if (logo) {
    doc.addImage(logo, 'PNG', margin, 22, 90, 28)
  } else {
    doc.setTextColor(...TOKENS.action)
    doc.setFont('helvetica', 'bold')
    doc.setFontSize(16)
    doc.text('E-monitor', margin, 42)
  }

  doc.setTextColor(...TOKENS.text)
  doc.setFont('helvetica', 'bold')
  doc.setFontSize(20)
  doc.text('Relatório de Cobrança', margin, 90)

  doc.setFont('helvetica', 'normal')
  doc.setFontSize(11)
  doc.setTextColor(...TOKENS.text2)
  doc.text(campaign.client_name || '—', margin, 110)

  doc.setFontSize(10)
  doc.setTextColor(...TOKENS.text3)
  doc.text(
    `${campaign.name || '—'} · ${fmtDateBR(campaign.start_date)} a ${fmtDateBR(campaign.end_date)}`,
    margin, 126,
  )

  // ─── KPI strip ─────────────────────────────────────────────────
  const kpiY = 150
  const kpiW = (pageW - margin * 2 - 16) / 3
  const kpis = [
    { label: 'Emissoras com falha', value: summary?.stations_with_failure ?? 0, color: TOKENS.text },
    { label: 'Dias com falha',      value: summary?.total_failure_days    ?? 0, color: TOKENS.text },
    { label: 'Déficit total',       value: summary?.total_deficit         ?? 0, color: TOKENS.deficit },
  ]
  kpis.forEach((kpi, i) => {
    const x = margin + i * (kpiW + 8)
    doc.setFillColor(...TOKENS.surface2)
    doc.roundedRect(x, kpiY, kpiW, 56, 8, 8, 'F')
    doc.setTextColor(...kpi.color)
    doc.setFont('helvetica', 'bold')
    doc.setFontSize(22)
    doc.text(String(kpi.value), x + 14, kpiY + 30)
    doc.setFont('helvetica', 'normal')
    doc.setFontSize(9)
    doc.setTextColor(...TOKENS.text3)
    doc.text(kpi.label.toUpperCase(), x + 14, kpiY + 48)
  })

  // ─── Stations table ────────────────────────────────────────────
  const body = stations.map(s => [
    s.station.name + (s.station.city ? `\n${s.station.city}` : ''),
    String(s.programmed ?? 0),
    `${s.identified ?? 0} (${s.programmed ? Math.round(((s.identified || 0) / s.programmed) * 100) : 0}%)`,
    fmtDaysList(s.failure_days || []),
    s.is_bonified ? `Bonificada (extras: ${s.extras ?? 0})` : '—',
  ])

  autoTable(doc, {
    startY: kpiY + 80,
    head: [['Emissora', 'Programado', 'Veiculou', 'Dias com falha', 'Status']],
    body,
    margin: { left: margin, right: margin },
    styles: { fontSize: 9, cellPadding: 6 },
    headStyles: { fillColor: TOKENS.surface2, textColor: TOKENS.text3, fontSize: 8, fontStyle: 'bold' },
    columnStyles: {
      0: { cellWidth: 160 },
      1: { halign: 'right', cellWidth: 70 },
      2: { halign: 'right', cellWidth: 70 },
      3: { cellWidth: 'auto' },
      4: { cellWidth: 100 },
    },
    didParseCell: (data) => {
      if (data.section === 'body' && data.column.index === 4 && data.cell.raw?.startsWith('Bonificada')) {
        data.cell.styles.textColor = TOKENS.bonified
      }
    },
  })

  // ─── Footer (every page) ───────────────────────────────────────
  const pages = doc.getNumberOfPages()
  for (let p = 1; p <= pages; p++) {
    doc.setPage(p)
    doc.setFontSize(8)
    doc.setTextColor(...TOKENS.text3)
    doc.text(`Gerado por E-monitor · ${fmtNow()}`, margin, doc.internal.pageSize.getHeight() - 20)
    doc.text(`Página ${p} de ${pages}`, pageW - margin, doc.internal.pageSize.getHeight() - 20, { align: 'right' })
  }

  doc.save(`cobranca-${slugify(campaign.name)}-${stamp()}.pdf`)
}
```

- [ ] **Step 2: Verify build (drawer now resolves)**

Run: `cd frontend && npx vite build 2>&1 | tail -15`
Expected: build succeeds.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/utils/pdfCampaignFailure.js
git commit -m "feat(frontend): pdfCampaignFailure.js builder (Relatório de Cobrança)"
```

---

## Task 12 — Frontend: integrate into `AdminStationFailuresPage`

**Files:**
- Modify: `frontend/src/pages/AdminStationFailuresPage.jsx`
- Modify: `frontend/src/pages/AdminStationFailuresPage.css`

- [ ] **Step 1: Import the new hooks/components**

In `frontend/src/pages/AdminStationFailuresPage.jsx`, near the existing imports, add:

```jsx
import { useStationFailures, useCampaignFailures } from '../api/hooks'
import CampaignFailureCard from '../components/CampaignFailureCard'
import CampaignFailureRow from '../components/CampaignFailureRow'
import CampaignFailureDrawer from '../components/CampaignFailureDrawer'
```

(Replace the existing `import { useStationFailures } from '../api/hooks'` line.)

- [ ] **Step 2: Add the new state + viewMode toggle**

In the main `AdminStationFailuresPage` function (replacing the existing state block):

```jsx
export default function AdminStationFailuresPage() {
  const [date, setDate] = useState(isoYesterday())
  const [minDown, setMinDown] = useState(60)

  // NEW: viewMode toggle + sub-tab + drill-in state
  const [viewMode, setViewMode] = useState('by_station') // 'by_station' | 'by_campaign'
  const [subTab, setSubTab] = useState('daily')          // 'daily' | 'historical'
  const [historyPage, setHistoryPage] = useState(1)
  const [drillCampaignId, setDrillCampaignId] = useState(null)

  const stationQ = useStationFailures({
    date, minDownSeconds: minDown,
  })
  const campaignQ = useCampaignFailures({
    mode: subTab === 'historical' ? 'historical' : 'by_date',
    date: subTab === 'daily' ? date : undefined,
    page: historyPage,
    pageSize: 50,
  })

  const isByStation = viewMode === 'by_station'
  const data        = isByStation ? stationQ.data        : campaignQ.data
  const isLoading   = isByStation ? stationQ.isLoading   : campaignQ.isLoading
  const isFetching  = isByStation ? stationQ.isFetching  : campaignQ.isFetching
  const refetch     = isByStation ? stationQ.refetch     : campaignQ.refetch
  const error       = isByStation ? stationQ.error       : campaignQ.error

  // ─── Derived for station view (unchanged) ──────────────────────
  const stations  = stationQ.data?.stations ?? []
  const summary   = stationQ.data?.summary
  const masthead  = useMemo(() => fmtMasthead(date), [date])
  const incidents = useMemo(() => flattenIncidents(stations, date), [stations, date])
  const hasData   = !isLoading && stations.length > 0

  // ─── Derived for campaign view ─────────────────────────────────
  const campaigns = campaignQ.data?.campaigns ?? []
```

Then, inside the existing JSX, **after** the date picker / `<label>Data</label>` block (and before the existing content), insert the new toggle:

```jsx
<div className="asf-mode-toggle" role="tablist" aria-label="Modo de visualização">
  <button
    role="tab"
    aria-selected={isByStation}
    className={`asf-mode-btn ${isByStation ? 'asf-mode-btn--active' : ''}`}
    onClick={() => setViewMode('by_station')}
  >
    Por emissora
  </button>
  <button
    role="tab"
    aria-selected={!isByStation}
    className={`asf-mode-btn ${!isByStation ? 'asf-mode-btn--active' : ''}`}
    onClick={() => setViewMode('by_campaign')}
  >
    Por campanha
  </button>
</div>
```

Then wrap the existing station-card rendering in a conditional, and add the campaign rendering. Find the existing `{stations.length > 0 ? (...) : <EmptyState .../>}` block and replace with something like:

```jsx
{isByStation ? (
  /* existing station-first content stays exactly as it was */
  stations.length > 0 ? (
    <div className="asf-grid">
      {stations.map((entry, idx) => (
        <StationFailureCard
          key={String(entry.station.id)}
          entry={entry}
          rank={idx + 1}
          date={date}
        />
      ))}
    </div>
  ) : (
    <EmptyState dateIso={date} />
  )
) : (
  <>
    <div className="asf-subtabs" role="tablist" aria-label="Sub-modo">
      <button
        role="tab"
        aria-selected={subTab === 'daily'}
        className={`asf-subtab ${subTab === 'daily' ? 'asf-subtab--active' : ''}`}
        onClick={() => setSubTab('daily')}
      >
        Falhas de {fmtMasthead(date).day}/{String(parseLocalDate(date).getMonth()+1).padStart(2,'0')}
      </button>
      <button
        role="tab"
        aria-selected={subTab === 'historical'}
        className={`asf-subtab ${subTab === 'historical' ? 'asf-subtab--active' : ''}`}
        onClick={() => { setSubTab('historical'); setHistoryPage(1) }}
      >
        Por Campanha (histórico)
      </button>
    </div>

    {isLoading ? (
      <p className="asf-state">Carregando…</p>
    ) : campaigns.length === 0 ? (
      <p className="asf-state">
        {subTab === 'daily' ? 'Nenhuma campanha falhou nesse dia.' : 'Nenhuma campanha tem falha registrada.'}
      </p>
    ) : subTab === 'daily' ? (
      <div className="asf-grid">
        {campaigns.map(entry => (
          <CampaignFailureCard
            key={String(entry.campaign.id)}
            entry={entry}
            onOpen={() => setDrillCampaignId(entry.campaign.id)}
          />
        ))}
      </div>
    ) : (
      <>
        <div className="asf-hist-table-wrap">
          <table className="asf-hist-table">
            <thead>
              <tr>
                <th>Campanha</th>
                <th>Status</th>
                <th>Emissoras c/ falha</th>
                <th>Dias c/ falha</th>
                <th>Déficit</th>
                <th>Bonificada</th>
              </tr>
            </thead>
            <tbody>
              {campaigns.map(entry => (
                <CampaignFailureRow
                  key={String(entry.campaign.id)}
                  entry={entry}
                  onOpen={() => setDrillCampaignId(entry.campaign.id)}
                />
              ))}
            </tbody>
          </table>
        </div>
        {/* Simple pagination — prev/next based on Total */}
        {(campaignQ.data?.total ?? 0) > 50 && (
          <div className="asf-pager">
            <button
              disabled={historyPage <= 1}
              onClick={() => setHistoryPage(p => Math.max(1, p - 1))}
            >← Anterior</button>
            <span>
              página {historyPage} de {Math.max(1, Math.ceil((campaignQ.data?.total ?? 0) / 50))}
            </span>
            <button
              disabled={historyPage >= Math.ceil((campaignQ.data?.total ?? 0) / 50)}
              onClick={() => setHistoryPage(p => p + 1)}
            >Próxima →</button>
          </div>
        )}
      </>
    )}
  </>
)}

{drillCampaignId && (
  <CampaignFailureDrawer
    campaignId={drillCampaignId}
    onClose={() => setDrillCampaignId(null)}
  />
)}
```

> Note: `summary`/`incidents`/`hasData` derivations apply only to the station view. Make sure the masthead / hero ribbon code is also wrapped behind `isByStation`, OR keep them visible but driven by the same `summary`. If the existing hero is per-station, keep it inside the station branch only — render a simpler header for the campaign view.

For the campaign view header, replace the hero with a minimal one:

```jsx
{!isByStation && (
  <div className="asf-camp-hero">
    <h2 className="asf-camp-hero-title">
      {subTab === 'daily'
        ? `Campanhas que falharam em ${masthead.day} de ${masthead.month}`
        : 'Campanhas com falha registrada na vigência'}
    </h2>
    <p className="asf-camp-hero-meta">
      {campaignQ.data?.summary?.campaigns ?? 0} campanha(s) · clique pra ver o relatório completo
    </p>
  </div>
)}
```

- [ ] **Step 3: Add the toggle + table styles**

Append to `frontend/src/pages/AdminStationFailuresPage.css`:

```css
/* ── Mode toggle (Por emissora / Por campanha) ─────────────────── */
.asf-mode-toggle {
  display: inline-flex;
  gap: 2px;
  background: var(--surface-2, #f1f5f9);
  border-radius: 10px;
  padding: 2px;
  margin: 0 0 18px;
}
.asf-mode-btn {
  border: none; background: transparent;
  padding: 8px 16px;
  font-size: 13px; font-weight: 500;
  color: var(--text-2, #4b5563);
  border-radius: 8px;
  cursor: pointer;
  transition: background .12s ease, color .12s ease;
}
.asf-mode-btn--active {
  background: var(--surface-1, #fff);
  color: var(--action, #E81E75);
  box-shadow: 0 1px 3px rgba(0,0,0,.08);
}

/* ── Sub-tabs inside Por campanha ─────────────────────────────── */
.asf-subtabs {
  display: flex; gap: 4px;
  border-bottom: 1px solid var(--border, #e2e8f0);
  margin-bottom: 16px;
}
.asf-subtab {
  border: none; background: transparent;
  padding: 10px 14px;
  font-size: 13px; color: var(--text-2, #4b5563);
  cursor: pointer;
  border-bottom: 2px solid transparent;
  transition: color .12s ease, border-color .12s ease;
}
.asf-subtab--active {
  color: var(--action, #E81E75);
  border-bottom-color: var(--action, #E81E75);
  font-weight: 600;
}

/* ── Campaign hero (simpler than per-station hero) ─────────────── */
.asf-camp-hero {
  margin-bottom: 16px;
}
.asf-camp-hero-title { margin: 0; font-size: 18px; color: var(--text, #06055B); }
.asf-camp-hero-meta { margin: 4px 0 0; font-size: 12px; color: var(--text-3, #9ca3af); }

/* ── Historical table ─────────────────────────────────────────── */
.asf-hist-table-wrap {
  background: var(--surface-1, #fff);
  border: 1px solid var(--border, #e2e8f0);
  border-radius: 12px;
  overflow: hidden;
}
.asf-hist-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.asf-hist-table thead th {
  padding: 12px 14px;
  text-align: left;
  font-size: 11px; text-transform: uppercase; letter-spacing: .04em;
  color: var(--text-3, #9ca3af);
  border-bottom: 1px solid var(--border, #e2e8f0);
  background: var(--surface-2, #f9fafb);
}
.asf-hist-table thead th:nth-child(n+3) { text-align: right; }

.asf-pager {
  margin-top: 12px;
  display: flex; gap: 10px; align-items: center; justify-content: center;
  font-size: 12px; color: var(--text-2, #4b5563);
}
.asf-pager button {
  border: 1px solid var(--border, #e2e8f0);
  background: var(--surface-1, #fff);
  border-radius: 6px;
  padding: 5px 10px;
  font-size: 12px;
  cursor: pointer;
}
.asf-pager button:disabled { opacity: .4; cursor: not-allowed; }

.asf-state {
  padding: 60px 0;
  text-align: center;
  color: var(--text-3, #9ca3af);
}
```

- [ ] **Step 4: Verify build**

Run: `cd frontend && npx vite build 2>&1 | tail -15`
Expected: build succeeds.

- [ ] **Step 5: Smoke in the browser**

Start the frontend dev server (per project conventions) and the API. Navigate to `/admin/station-failures`:

1. Default view shows "Por emissora" tab active, existing UI intact.
2. Click "Por campanha" → grid of campaign cards (or empty state) appears.
3. Click "Por Campanha (histórico)" sub-tab → table appears, paginated if >50.
4. Click any card/row → drawer opens, KPIs + table render.
5. Click "Baixar PDF de cobrança" → PDF downloads with logo + tables + day-chips.
6. ESC closes the drawer; click outside also closes.
7. Toggle back to "Por emissora" — station-first view returns intact.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/AdminStationFailuresPage.jsx frontend/src/pages/AdminStationFailuresPage.css
git commit -m "feat(frontend): integrate Por campanha toggle + drill-in drawer in /admin/station-failures"
```

---

## Task 13 — Documentation

**Files:**
- Create: `docs/features/admin-campaign-failures.md`
- Modify: `docs/features/admin-station-failures.md`
- Modify: `CLAUDE.md` (Mapa de consulta)
- Modify: `docs/README.md` (if it has a features index)

- [ ] **Step 1: Create the feature doc**

`docs/features/admin-campaign-failures.md`:

```markdown
---
status: implementado
ultima-verificacao: 2026-05-25
codigo-relacionado:
  - workers/internal/catalog/campaign_failures.go
  - workers/internal/catalog/campaign_failures_test.go
  - workers/internal/api/handlers/admin_campaign_failures.go
  - workers/internal/api/handlers/admin_campaign_failures_test.go
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - frontend/src/pages/AdminStationFailuresPage.jsx
  - frontend/src/pages/AdminStationFailuresPage.css
  - frontend/src/components/CampaignFailureCard.jsx
  - frontend/src/components/CampaignFailureRow.jsx
  - frontend/src/components/CampaignFailureDrawer.jsx
  - frontend/src/components/CampaignFailureCard.css
  - frontend/src/utils/pdfCampaignFailure.js
  - frontend/src/api/hooks.js
---

# Admin → Falhas "Por campanha" (`/admin/station-failures` modo `Por campanha`)

Visão complementar de [admin-station-failures.md](admin-station-failures.md): mesma rota, mesmo gate de admin, mas pivota a leitura **por campanha** ao invés de por emissora. Internaliza a UX do app externo `Relatório Campanha`.

## Por que existe

A diretora executiva precisa de uma visão diária de **quais campanhas tiveram falha pra cobrar as emissoras envolvidas**. A visão station-first do `/admin/station-failures` (atual) responde a outra pergunta — *quais emissoras caíram e o que isso arrastou*. As duas convivem como dois modos do mesmo painel.

## O que mostra

| Sub-modo | Quando usar | Layout |
|----------|-------------|--------|
| **Falhas de [data]** | Cobrança diária ("o que falhou ontem") | Grid de cards, um por campanha, com emissoras dentro |
| **Por Campanha (histórico)** | Backlog ("o que ainda precisa cobrança") | Tabela paginada, todas campanhas com qualquer falha |

Click em qualquer card/linha → drawer lateral com **todas** as emissoras da campanha que falharam em **qualquer dia** da vigência, com chips de dias específicos. Dentro do drawer, botão **"Baixar PDF de cobrança"**.

## Quem pode ver

Admin only. Rota frontend gated via `<RequireRole roles={['admin']}>` (a rota é `/admin/station-failures`, não muda). Endpoints backend gated via `auth.RequireRole("admin")` no router.

## "Bonificada"

Uma emissora é marcada como **bonificada** (roxo) quando o total de execuções extras na campanha cobre o déficit total. Definição:

```
extras  = SUM(out_slot + out_date + bonus)  -- na campanha inteira
deficit = SUM(deficit)                       -- na campanha inteira, da view daily_play_summary
bonified = (extras >= deficit) AND extras > 0 AND deficit > 0
```

A view `daily_play_summary` já expõe todas as colunas necessárias — esta feature não toca em `detections` direto.

**Caveat:** em campanha ainda ativa, `bonified` é provisória — amanhã pode aparecer mais déficit. O drawer mostra banner amarelo discreto avisando.

## Endpoints

```
GET /v1/internal/admin/campaign-failures?date=YYYY-MM-DD       # modo dia (default)
GET /v1/internal/admin/campaign-failures?mode=historical&page=N&page_size=50
GET /v1/internal/admin/campaign-failures/{id}                  # drill-in
```

Auth: admin. Limites de `date`: today-90d a today (fora → 400). Combinar `mode=historical` com `date` → 400. Drill-in com campanha cancelada → 404.

Detalhes de response no spec [2026-05-25-campaign-failures-view-design.md](../superpowers/specs/2026-05-25-campaign-failures-view-design.md).

## Performance

3 queries SQL no modo dia (Q1 campanhas + Q2 stations do dia + Q3 agregados da campanha). 2 queries no histórico (lista paginada + count). 2 no drill-in. Sem cache (staleTime React Query 60s). Sem polling. Se virar gargalo, materializar a view `daily_play_summary` em snapshot diário.

## PDF de cobrança

Gerado no browser via jsPDF + jspdf-autotable em `frontend/src/utils/pdfCampaignFailure.js`. Layout: header E-monitor + cliente + período → 3 KPIs (Emissoras com falha · Dias · Déficit) → tabela `Emissora | Programado | Veiculou | Dias com falha | Status`. Bonificada marcada em roxo. Footer com `Gerado por E-monitor · DD/MM/YYYY HH:MM` + paginação.

Filename: `cobranca-{campanha-slug}-{YYYYMMDD}.pdf`.

Distinguir do PDF gerado pelo botão "Relatórios" em `/campaigns` (ver [campaign-reports.md](campaign-reports.md)): aquele é prestação de contas pro cliente, este é cobrança pra emissora.

## Edge cases mapeados

| Caso | Comportamento |
|------|---------------|
| `date` no futuro | 400 |
| `date` > 90d | 400 |
| `mode=historical` + `date` | 400 |
| Drill-in com campanha cancelada / inexistente | 404 → drawer mostra "campanha não encontrada" |
| Sem falhas no dia | Estado vazio `Nenhuma campanha falhou nesse dia` |
| Histórico vazio | Estado vazio `Nenhuma campanha tem falha registrada` |
| Logo cliente ausente | Fallback de inicial colorida (rosa) |
| Paginação além do total | `campaigns: []` no response, frontend mostra empty |
| Campanha 100% bonificada no histórico | Aparece com chip "100% bonificada" — não some |

## Não cobre (escopo intencionalmente fora)

- PDF agregado do dia (todas as campanhas em um só PDF)
- Envio do PDF por email/webhook
- Modelar "compensações" como entidades persistidas — `extras` é on-the-fly
- Auto-refresh em background — admin investiga sob demanda

## Spec arquitetural

[../superpowers/specs/2026-05-25-campaign-failures-view-design.md](../superpowers/specs/2026-05-25-campaign-failures-view-design.md)
```

- [ ] **Step 2: Update `docs/features/admin-station-failures.md`**

Find the "Não cobre" section (around line 127) and update — remove "perspectiva por campanha" if listed, OR add a small cross-reference at the top of "Não cobre":

```markdown
## Não cobre (escopo intencionalmente fora)

- Worker travado *sem* campanha agendada — usar `/admin/overview`
- Range de múltiplos dias — só dia único
- **Perspectiva campaign-first** — coberto pelo modo "Por campanha" da mesma página, ver [admin-campaign-failures.md](admin-campaign-failures.md)
- Exportação CSV/PDF do modo "Por emissora" — fora de escopo (o modo "Por campanha" tem PDF de cobrança)
- Alerta proativo (webhook/email) — pull-only
- Histórico de PCM por minuto — não persistimos; daí a heurística "silent-gap"
```

- [ ] **Step 3: Update `CLAUDE.md` (Mapa de consulta)**

Find the row about `/admin/station-failures` in the "Mapa de consulta" table. Insert immediately after it:

```markdown
| Modo "Por campanha" de `/admin/station-failures` (cards por campanha + drawer + PDF cobrança) | [docs/features/admin-campaign-failures.md](docs/features/admin-campaign-failures.md) |
```

- [ ] **Step 4: Update `docs/README.md` if it has a features index**

Check `docs/README.md`. If it has a `## Features` section with an alphabetical list, add:

```markdown
- [admin-campaign-failures.md](features/admin-campaign-failures.md) — Modo "Por campanha" em `/admin/station-failures`
```

(If `docs/README.md` doesn't have such an index, skip this step — the feature doc is reachable from CLAUDE.md.)

- [ ] **Step 5: Commit**

```bash
git add docs/features/admin-campaign-failures.md docs/features/admin-station-failures.md CLAUDE.md docs/README.md
git commit -m "docs: admin-campaign-failures feature page + cross-references"
```

---

## Task 14 — Final smoke test + sanity

- [ ] **Step 1: Build + test everything from scratch**

```bash
cd workers && go build ./... && go test ./...
cd ../frontend && npx vite build
```

Expected: all green.

- [ ] **Step 2: Manual smoke (admin user, dev DB)**

1. Start API + frontend; log in as admin.
2. Open `/admin/station-failures`. Confirm "Por emissora" works exactly like before (no regression).
3. Click "Por campanha". Land on "Falhas de [data]" sub-tab. Pick a date with known failures (use a date with `daily_play_summary` deficit data).
4. Confirm cards render: client logo, campaign name, emissoras with `id/programmed · %` + "faltam N" / "falhou, bonificada".
5. Click card → drawer opens. KPIs render. Tabela shows emissoras × dias-de-falha (chips).
6. Click "Baixar PDF de cobrança". PDF downloads. Open: header com logo + cliente + período, KPIs, tabela com chips inline e "Bonificada" em roxo.
7. Close drawer (X, ESC, click backdrop).
8. Toggle "Por Campanha (histórico)". Table renders. Pagination shows up if > 50 campanhas com falha.
9. Click a row → drawer abre. PDF works.
10. Trocar de data no picker em "Falhas de [data]" — atualiza.
11. Logout, login como `viewer`. Tentar acessar `/admin/station-failures` → redirect (RequireRole).
12. Como admin, hit `GET /v1/internal/admin/campaign-failures/{id-cancelado}` direto via curl → 404.

- [ ] **Step 3: Final cleanup commit (if needed)**

If anything was tweaked during smoke (style polish, copy fixes), commit:

```bash
git add <files>
git commit -m "chore: polish admin-campaign-failures after smoke test"
```

If nothing to fix, skip this step.

---

## Self-Review Checklist (for the writing agent — not the implementer)

- [x] **Spec coverage:** every section of the spec maps to one or more tasks:
  - Toggle UX → Task 12
  - Modo dia (cards + drill-in) → Tasks 1–6, 7, 8, 10, 12
  - Modo histórico (tabela + paginação) → Tasks 3, 7, 9, 12
  - Drill-in (drawer) → Tasks 4, 7, 10
  - PDF de cobrança → Task 11
  - `IsBonified` definition → Task 1 (test + impl)
  - Endpoint shapes → Tasks 2–5
  - Edge cases → Tasks 5 (handler validation), 10 (drawer 404), 12 (pager edge)
  - Docs → Task 13
- [x] **Placeholders:** None. All steps have concrete code or commands.
- [x] **Type consistency:** Method names (`ListForDate`, `ListHistorical`, `Get`), struct names (`CampaignInfo`, `CampaignFailureStation`, `DailyResult`, etc.) match across backend tasks. Frontend hook names (`useCampaignFailures`, `useCampaignFailureDetail`) match component imports.
- [x] **No-test convention:** Frontend doesn't have a test suite (`grep -l "\.test\." frontend/src/` returns nothing). Plan follows that convention — manual smoke is the verification.

---

## Notes for the implementer

- **Pre-existing `StationAvatar`**: used in the drawer + station rows. Already imported by other pages — should work as drop-in.
- **`ClientLogo`**: probably doesn't exist (it's referenced in Card task). Step 2 of Task 8 tells you to check and inline a minimal fallback if missing.
- **CSS variables**: tokens like `--action`, `--text`, `--text-2`, `--text-3`, `--border`, `--surface-1`, `--surface-2` should already exist in `frontend/src/index.css`. If a variable doesn't render, fall back to the hex literal already in the rule (`var(--action, #E81E75)`).
- **`writeJSON`** + **`auth.RequireRole`**: standard helpers used elsewhere in the API. No need to define new ones.
- **Performance is "good enough"**: see spec — if `daily_play_summary` aggregation becomes slow at scale (1000+ active campaigns), materialize the view via cron. Not in this plan.
