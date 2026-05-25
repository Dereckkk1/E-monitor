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
