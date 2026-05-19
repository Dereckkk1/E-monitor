package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/catalog"
)

// StationFailuresRepo abstracts the catalog repo so the handler is testable
// without a live DB.
type StationFailuresRepo interface {
	ListForDate(ctx context.Context, day time.Time, minDownSeconds int) (*catalog.Result, error)
}

// StationFailuresHandler powers /v1/internal/admin/station-failures. It cross-
// references stream-down events with campaign-deficit data for a given local
// day and returns the affected emissoras + campanhas.
//
// Auth: admin-only (mounted under the admin group in router.go).
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

	minDown := 60
	if v := q.Get("min_down_seconds"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 3600 {
			minDown = n
		}
	}

	if h.Repo == nil {
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
