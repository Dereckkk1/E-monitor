package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

// OverrideRepo é a fatia de catalog.DistributionOverrides que o handler usa —
// interface p/ permitir mock no teste sem DB.
type OverrideRepo interface {
	Upsert(ctx context.Context, in catalog.UpsertOverrideInput) error
	Delete(ctx context.Context, campaignID, typeID, stationID uuid.UUID, forDate time.Time) error
	ListByCampaignAndDateRange(ctx context.Context, campaignID uuid.UUID, from, to time.Time) ([]catalog.DistributionOverride, error)
}

// OverrideRecategorizer redispara a recat de uma célula após mudança de override.
// Satisfeita por *catalog.DistributionRules.
type OverrideRecategorizer interface {
	RecategorizeForOverride(ctx context.Context, campaignID, typeID, stationID uuid.UUID, forDate time.Time) error
}

type DistributionOverridesHandler struct {
	Repo  OverrideRepo
	Recat OverrideRecategorizer
}

type overridePayload struct {
	TypeID        uuid.UUID `json:"type_id"`
	StationID     uuid.UUID `json:"station_id"`
	ForDate       string    `json:"for_date"` // YYYY-MM-DD
	PlaysExpected int16     `json:"plays_expected"`
	TimeStart     string    `json:"time_start"` // "HH:MM"
	TimeEnd       string    `json:"time_end"`   // "HH:MM"
	Reason        *string   `json:"reason,omitempty"`
}

// hhmmPattern aceita 00:00 até 23:59. Validação no handler (não só no DB)
// pra dar erro 400 amigável antes de bater no Postgres.
var hhmmPattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

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
	if !hhmmPattern.MatchString(p.TimeStart) {
		http.Error(w, "time_start must be HH:MM (00:00-23:59)", http.StatusBadRequest)
		return
	}
	if !hhmmPattern.MatchString(p.TimeEnd) {
		http.Error(w, "time_end must be HH:MM (00:00-23:59)", http.StatusBadRequest)
		return
	}
	// String compare é seguro porque o regex força HH:MM com zero-padding.
	if p.TimeEnd <= p.TimeStart {
		http.Error(w, "time_end must be greater than time_start", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Upsert(r.Context(), catalog.UpsertOverrideInput{
		CampaignID: campaignID, TypeID: p.TypeID, StationID: p.StationID,
		ForDate: date, PlaysExpected: p.PlaysExpected,
		TimeStart: p.TimeStart, TimeEnd: p.TimeEnd,
		Reason: p.Reason,
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Recategoriza as detections da célula afetada.
	if h.Recat != nil {
		// Síncrono (NÃO goroutine): o frontend refetcha detections/daily-summary no
		// onSuccess deste 204, então a recat precisa ter commitado ANTES da resposta
		// — senão o refetch pinta a categoria velha (race). Escopo de célula = poucas
		// linhas, latência desprezível. Best-effort: o override já foi gravado, então
		// erro na recat não falha o request.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		recordRecatFailure("override_upsert", h.Recat.RecategorizeForOverride(ctx, campaignID, p.TypeID, p.StationID, date))
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
	if err := h.Repo.Delete(r.Context(), campaignID, p.TypeID, p.StationID, date); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Recategoriza as detections da célula afetada.
	if h.Recat != nil {
		// Síncrono (NÃO goroutine): o frontend refetcha detections/daily-summary no
		// onSuccess deste 204, então a recat precisa ter commitado ANTES da resposta
		// — senão o refetch pinta a categoria velha (race). Escopo de célula = poucas
		// linhas, latência desprezível. Best-effort: o override já foi gravado, então
		// erro na recat não falha o request.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		recordRecatFailure("override_delete", h.Recat.RecategorizeForOverride(ctx, campaignID, p.TypeID, p.StationID, date))
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
