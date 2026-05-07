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
		UptimePct      float64                `json:"uptime_pct"`
		IncidentCount  int                    `json:"incident_count"`
		LastIncidentAt *time.Time             `json:"last_incident_at,omitempty"`
		DailySummary   []catalog.DailySummary `json:"daily_summary"`
	}

	result := make([]row, 0, len(stations))
	for _, st := range stations {
		entry := row{Station: st, UptimePct: 100.0, DailySummary: make([]catalog.DailySummary, 0)}
		if s, ok := summaries[st.ID]; ok {
			entry.UptimePct = s.UptimePct
			entry.IncidentCount = s.IncidentCount
			entry.LastIncidentAt = s.LastIncidentAt
			entry.DailySummary = s.DailySummary
		}
		result = append(result, entry)
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

	evts, err := h.HealthEvents.GetForStation(ctx, id, days)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if evts == nil {
		evts = []catalog.HealthEvent{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"station_id":   id,
		"period_start": time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339),
		"period_end":   time.Now().UTC().Format(time.RFC3339),
		"events":       evts,
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
