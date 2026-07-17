package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrCampaignNotFound = errors.New("campaign not found or cancelled")

// IsBonified reports whether enough non-strict-in-slot plays exist to cover
// the remaining deficit. Pure function — used by all 3 endpoints (daily,
// historical, drill-in) to keep the rule consistent.
//
// Definition mirrors the external "Relatório Campanha" semantics:
//
//	extras    = out_slot + out_date + bonus aggregated across campaign period
//	deficit   = sum of daily_play_summary.deficit across campaign period
//	bonified  = (extras >= deficit) AND extras > 0 AND deficit > 0
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
	Station           StationFailureInfo `json:"station"`
	Programmed        int                `json:"programmed"`
	Identified        int                `json:"identified"`
	Deficit           int                `json:"deficit"`
	Extras            int                `json:"extras"`
	IsBonified        bool               `json:"is_bonified"`
	FailureDaysOnDate []string           `json:"failure_days_on_date,omitempty"` // only in modo dia
	FailureDays       []string           `json:"failure_days,omitempty"`         // only in drill-in
}

type CampaignDailyFailure struct {
	Campaign CampaignInfo             `json:"campaign"`
	Stations []CampaignFailureStation `json:"stations"`
}

type CampaignDailySummary struct {
	Campaigns    int `json:"campaigns"`
	Stations     int `json:"stations"`
	TotalDeficit int `json:"total_deficit"`
}

type DailyResult struct {
	Mode      string                 `json:"mode"` // "by_date"
	Date      string                 `json:"date"`
	Summary   CampaignDailySummary   `json:"summary"`
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

// failureHorizonClause restringe qualquer agregação de daily_play_summary a
// dias JÁ ENCERRADOS (estritamente antes de "hoje" em America/Sao_Paulo).
//
// A view daily_play_summary não é materializada: ela emite uma linha de déficit
// pra CADA dia agendado até o end_date da regra (CROSS JOIN generate_series na
// migration 0041). Dias >= hoje têm expected>0 e in_slot=0 → deficit=expected,
// mas um dia que ainda não chegou não pode ser "falha". Sem este corte, o drawer
// e o histórico de /admin/station-failures contavam amanhã (e o resto da
// vigência) como dia com falha. Regra do produto: "de ontem pra trás".
//
// Fica no frame local do Brasil porque for_date já vive nesse frame (detections
// convertidas via AT TIME ZONE 'America/Sao_Paulo' na migration 0041; lado
// "expected" é date puro do generate_series). Consumidores single-day
// (station_failures.go, ListForDate Q1/Q2) já limitam a data ≤ hoje no handler
// e não precisam disto.
const failureHorizonClause = `dps.for_date < (now() AT TIME ZONE 'America/Sao_Paulo')::date`

// NOTE (Task 13, safe subset): ListForDate's Q3 and Get() still read the
// daily_play_summary VIEW, not daily_play_summary_for(). Both queries have
// NO lower bound on for_date (only failureHorizonClause caps the upper end),
// which the view supports because its `actual` CTE is unbounded history. The
// function requires p_from/p_to NOT NULL, so migrating these two forces
// fabricating a p_from. Two candidates were tried and both PROVEN (against
// synthetic fixtures on rc-test-pg) to silently drop legitimate 'out_date'
// rows — detections attributed to the campaign but outside [start_date,
// end_date] (see distribution_rules.go recatClassifiedCTE) — which feed
// `extras` and therefore IsBonified:
//   - Q3: p_from = MIN(start_date) across the selected campaigns clips
//     out_date detections that occurred BEFORE that minimum start_date.
//   - Get: p_from/p_to = campaign.start_date/LEAST(end_date, hoje-1) clips
//     out_date detections outside the campaign's own [start_date, end_date]
//     — which is exactly the set out_date exists to capture (e.g. material
//     still airing after the contract ended).
// Both are pricing-adjacent (IsBonified feeds "extras >= deficit" credit
// decisions), so per CLAUDE.md byte-identical-output gate they were left on
// the view rather than shipped with a proven divergence. Follow-up: either
// accept staying on the view permanently for these two, or widen p_from to a
// provably-safe bound (e.g. LEAST(campaign MIN(start_date), MIN(detected_at)
// from detection_campaigns for those campaigns)) and re-prove parity.

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
	// As of migration 0052 (Task 13) reads go through daily_play_summary_for
	// with p_from=p_to=$1 (single-day pushdown) instead of the view.
	rows1, err := r.pool.Query(ctx, `
SELECT DISTINCT c.id, c.name, c.start_date, c.end_date, c.status,
                cl.id, COALESCE(cl.name, '—') AS client_name,
                COALESCE(cl.logo_url, '') AS client_logo_url
FROM daily_play_summary_for($1::date, $1::date, NULL) dps
JOIN campaigns c ON c.id = dps.campaign_id
LEFT JOIN clients cl ON cl.id = c.client_id
WHERE dps.deficit > 0
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
	// As of migration 0052 (Task 13): campaign_id filter moves into the
	// function's p_campaigns arg (pushdown) instead of a WHERE on the view.
	rows2, err := r.pool.Query(ctx, `
SELECT dps.campaign_id, dps.station_id,
       s.name, COALESCE(s.band, '') AS band,
       COALESCE(to_char(s.frequency_mhz, 'FM999990.0'), '') AS freq,
       COALESCE(s.city, '') AS city, COALESCE(s.logo_url, '') AS logo_url
FROM daily_play_summary_for($1::date, $1::date, $2::uuid[]) dps
JOIN stations s ON s.id = dps.station_id
WHERE dps.deficit > 0
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
  AND ` + failureHorizonClause + `
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

	result.Summary = CampaignDailySummary{
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
  WHERE ` + failureHorizonClause + `
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
  WHERE ` + failureHorizonClause + `
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
  AND ` + failureHorizonClause + `
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
