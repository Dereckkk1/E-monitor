package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

type DistributionOverridesHandler struct {
	Repo *catalog.DistributionOverrides
}

type overridePayload struct {
	MaterialID    uuid.UUID `json:"material_id"`
	StationID     uuid.UUID `json:"station_id"`
	ForDate       string    `json:"for_date"` // YYYY-MM-DD
	PlaysExpected int16     `json:"plays_expected"`
	Reason        *string   `json:"reason,omitempty"`
}

func (h *DistributionOverridesHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	var p overridePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	date, err := time.Parse("2006-01-02", p.ForDate)
	if err != nil {
		http.Error(w, "for_date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Upsert(r.Context(), catalog.UpsertOverrideInput{
		CampaignID: campaignID, MaterialID: p.MaterialID, StationID: p.StationID,
		ForDate: date, PlaysExpected: p.PlaysExpected, Reason: p.Reason,
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DistributionOverridesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	var p overridePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	date, err := time.Parse("2006-01-02", p.ForDate)
	if err != nil {
		http.Error(w, "for_date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Delete(r.Context(), campaignID, p.MaterialID, p.StationID, date); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DistributionOverridesHandler) ListByDateRange(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		http.Error(w, "from must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		http.Error(w, "to must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	overrides, err := h.Repo.ListByCampaignAndDateRange(r.Context(), campaignID, from, to)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if overrides == nil {
		overrides = []catalog.DistributionOverride{}
	}
	writeJSON(w, http.StatusOK, overrides)
}
