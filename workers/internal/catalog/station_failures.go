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

// ListForDate returns stations that lost campaign inserções (deficit > 0)
// on the given local-day, with the campaigns whose slots were lost.
// Stations with only downtime (no deficit) are excluded — they were noise
// for the operator (spec 2026-05-25). minDownSeconds still filters tiny
// down events (default 60) when computing the down_sec of qualifying
// stations.
func (r *StationFailures) ListForDate(ctx context.Context, day time.Time, minDownSeconds int) (*Result, error) {
	dayStr := day.Format("2006-01-02")
	result := &Result{
		Date:     dayStr,
		Stations: []StationFailure{},
	}

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
  -- Exclude cancelled campaigns: a campaign manually ended early must not be
  -- reported as "lost slots" (it would bill inserções for a campaign the
  -- client cancelled). Mirrors campaign_failures.go's c.status <> 'cancelada'
  -- filter so the two failure surfaces agree for the same day.
  SELECT dps.station_id, COUNT(DISTINCT dps.campaign_id) AS aff_camp
  FROM daily_play_summary dps
  JOIN campaigns c ON c.id = dps.campaign_id
  WHERE dps.for_date = $1::date AND dps.deficit > 0
    AND c.status <> 'cancelada'
  GROUP BY dps.station_id
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
WHERE df.station_id IS NOT NULL
ORDER BY COALESCE(d.down_sec, 0) DESC, COALESCE(df.aff_camp, 0) DESC, s.name ASC`,
		dayStr, minDownSeconds)
	if err != nil {
		return nil, fmt.Errorf("query 1 stations: %w", err)
	}
	defer rows.Close()

	type stationAcc struct {
		info    StationInfo
		downSec int
		affCamp int
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
  AND c.status <> 'cancelada'
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
