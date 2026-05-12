package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

type DistributionRulesHandler struct {
	Repo *catalog.DistributionRules
}

type rulePayload struct {
	MaterialID  uuid.UUID   `json:"material_id"`
	StationIDs  []uuid.UUID `json:"station_ids"`
	StartDate   string      `json:"start_date"` // YYYY-MM-DD
	EndDate     string      `json:"end_date"`
	WeekdayMask int16       `json:"weekday_mask"`
	TimeStart   string      `json:"time_start"` // HH:MM
	TimeEnd     string      `json:"time_end"`
	PlaysPerDay int16       `json:"plays_per_day"`
}

func (p *rulePayload) toInput(campaignID uuid.UUID) (catalog.CreateDistributionRuleInput, error) {
	start, err := time.Parse("2006-01-02", p.StartDate)
	if err != nil {
		return catalog.CreateDistributionRuleInput{}, err
	}
	end, err := time.Parse("2006-01-02", p.EndDate)
	if err != nil {
		return catalog.CreateDistributionRuleInput{}, err
	}
	return catalog.CreateDistributionRuleInput{
		CampaignID: campaignID, MaterialID: p.MaterialID,
		StationIDs: p.StationIDs,
		StartDate:  start, EndDate: end,
		WeekdayMask: p.WeekdayMask,
		TimeStart:   p.TimeStart, TimeEnd: p.TimeEnd,
		PlaysPerDay: p.PlaysPerDay,
	}, nil
}

func (h *DistributionRulesHandler) Create(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	var p rulePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	in, err := p.toInput(campaignID)
	if err != nil {
		http.Error(w, "invalid dates: "+err.Error(), http.StatusBadRequest)
		return
	}
	rule, err := h.Repo.Create(r.Context(), in)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Recategoriza detections existentes que possam ser afetadas (async, best-effort)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = h.Repo.RecategorizeForRule(ctx, rule.ID)
	}()
	writeJSON(w, http.StatusCreated, rule)
}

func (h *DistributionRulesHandler) ListByCampaign(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	rules, err := h.Repo.ListByCampaign(r.Context(), campaignID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rules == nil {
		rules = []catalog.DistributionRule{}
	}
	writeJSON(w, http.StatusOK, rules)
}

func (h *DistributionRulesHandler) Update(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	ruleID, err := uuid.Parse(chi.URLParam(r, "ruleID"))
	if err != nil {
		http.Error(w, "invalid ruleID", http.StatusBadRequest)
		return
	}
	var p rulePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	in, err := p.toInput(campaignID)
	if err != nil {
		http.Error(w, "invalid dates: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.Repo.Update(r.Context(), ruleID, in); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = h.Repo.RecategorizeForRule(ctx, ruleID)
	}()
	w.WriteHeader(http.StatusNoContent)
}

func (h *DistributionRulesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	ruleID, err := uuid.Parse(chi.URLParam(r, "ruleID"))
	if err != nil {
		http.Error(w, "invalid ruleID", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Delete(r.Context(), ruleID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Após delete o scope se perde — recategoriza toda a campanha
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = h.Repo.RecategorizeForCampaign(ctx, campaignID)
	}()
	w.WriteHeader(http.StatusNoContent)
}
