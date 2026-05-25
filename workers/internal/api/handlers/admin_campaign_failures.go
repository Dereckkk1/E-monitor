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
