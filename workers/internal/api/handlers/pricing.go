package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"radiocheck/internal/catalog"
)

// PricingHandler expõe pricing por (campaign × station). Step 5 do wizard de
// campanha consome List (carregar estado atual) + Upsert (salvar mudanças).
// O resumo de /detections lê List também pra calcular valor/impactos.
type PricingHandler struct {
	Repo *catalog.Pricing
}

// ListByCampaign — GET /v1/internal/campaigns/{campaignID}/pricing
func (h *PricingHandler) ListByCampaign(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	out, err := h.Repo.ListByCampaign(r.Context(), campaignID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// upsertPayload aceita JSON simples — wizard envia consolidated_value (number)
// OU per_type (array). Mutuamente exclusivos por contrato (validado no repo).
type upsertPayload struct {
	Mode              string                `json:"mode"`
	ConsolidatedValue *float64              `json:"consolidated_value"`
	PerType           []catalog.TypePricing `json:"per_type"`
}

// Upsert — PUT /v1/internal/campaigns/{campaignID}/pricing/{stationID}
func (h *PricingHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	stationID, err := uuid.Parse(chi.URLParam(r, "stationID"))
	if err != nil {
		http.Error(w, "invalid stationID", http.StatusBadRequest)
		return
	}
	var p upsertPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// `per_type` nil é tratado como slice vazia pelo repo.
	if p.PerType == nil {
		p.PerType = []catalog.TypePricing{}
	}

	out, err := h.Repo.Upsert(r.Context(), catalog.UpsertInput{
		CampaignID:        campaignID,
		StationID:         stationID,
		Mode:              p.Mode,
		ConsolidatedValue: p.ConsolidatedValue,
		PerType:           p.PerType,
	})
	if err != nil {
		// Erros de validação viram 422 com mensagem; o resto é 500.
		switch {
		case errors.Is(err, catalog.ErrPricingInvalidMode),
			errors.Is(err, catalog.ErrPricingConsolidatedRequiresValue),
			errors.Is(err, catalog.ErrPricingPerInsertionRequiresTypes),
			errors.Is(err, catalog.ErrPricingMixedFields),
			errors.Is(err, catalog.ErrPricingNegativeValue):
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		default:
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete — DELETE /v1/internal/campaigns/{campaignID}/pricing/{stationID}
// Útil pra "desconfigurar" uma emissora que foi removida do target_stations.
func (h *PricingHandler) Delete(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	stationID, err := uuid.Parse(chi.URLParam(r, "stationID"))
	if err != nil {
		http.Error(w, "invalid stationID", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Delete(r.Context(), campaignID, stationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
