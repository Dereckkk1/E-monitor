package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
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
	// IsCurrentlyDown indicates the most recent event is an unresolved 'down'
	// (no matching 'up' yet). True iff the last event is type='down' AND its
	// duration_seconds is still NULL (i.e., RecordUp hasn't closed it).
	IsCurrentlyDown bool `json:"is_currently_down"`
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

// ─── Pure helpers ─────────────────────────────────────────────────────────────

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
			} else {
				// open outage: count elapsed time to now
				elapsed := int(time.Since(ev.EventAt).Seconds())
				totalDowntime += elapsed
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

	// Events come ordered ASC by event_at. Station is "currently down" iff the
	// last event is an unresolved 'down' (RecordUp closes one by writing an
	// 'up' AND backfilling duration_seconds on the matching 'down').
	isCurrentlyDown := false
	if len(events) > 0 {
		last := events[len(events)-1]
		if last.EventType == "down" && last.DurationSeconds == nil {
			isCurrentlyDown = true
		}
	}

	return &StationHealthSummary{
		StationID:       stationID,
		UptimePct:       uptimePct,
		IncidentCount:   incidentCount,
		LastIncidentAt:  lastIncidentAt,
		DailySummary:    buildDailySummary(events, days),
		IsCurrentlyDown: isCurrentlyDown,
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

