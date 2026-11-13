package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"radiocheck/internal/acoes"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

// PricingHandler expõe pricing por (campaign × station). Step 5 do wizard de
// campanha consome List (carregar estado atual) + Upsert (salvar mudanças).
// O resumo de /detections lê List também pra calcular valor/impactos.
type PricingHandler struct {
	Repo *catalog.Pricing
	// CampaignRepo: só usado por ListByCampaign pra scope check (404
	// anti-oracle quando viewer pede pricing de campanha de outro cliente).
	// Nil-safe — handlers admin/operator que só fazem writes não precisam.
	CampaignRepo *catalog.Campaigns
}

// servico monta as ações de preço sobre o repositório deste handler.
func (h *PricingHandler) servico() *acoes.Servico {
	return &acoes.Servico{Precos: h.Repo}
}

// ListByCampaign — GET /v1/internal/campaigns/{campaignID}/pricing
func (h *PricingHandler) ListByCampaign(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	// Viewer scope: 404 quando a campanha não pertence ao cliente do JWT.
	if auth.ClientScopesFromContext(r.Context()) != nil && h.CampaignRepo != nil {
		camp, err := h.CampaignRepo.Get(r.Context(), campaignID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
			} else {
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		if !auth.ScopeAllows(r.Context(), camp.ClientID) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
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

	saida, err := h.servico().DefinirPrecos(r.Context(), campaignID, []catalog.EntradaPreco{{
		StationID:         stationID,
		Mode:              p.Mode,
		ConsolidatedValue: p.ConsolidatedValue,
		PerType:           p.PerType,
	}})
	if err != nil {
		// Erros de validação viram 422 com a mensagem do catálogo; recusa de
		// referência inexistente segue o mapa comum; o resto é 500.
		switch {
		case errors.Is(err, catalog.ErrPricingInvalidMode),
			errors.Is(err, catalog.ErrPricingConsolidatedRequiresValue),
			errors.Is(err, catalog.ErrPricingPerInsertionRequiresTypes),
			errors.Is(err, catalog.ErrPricingMixedFields),
			errors.Is(err, catalog.ErrPricingNegativeValue):
			// Recusa.Mensagem guarda o texto original do catálogo (err.Error()
			// traria o sufixo [código] da Recusa).
			var rec *acoes.Recusa
			if errors.As(err, &rec) {
				http.Error(w, rec.Mensagem, http.StatusUnprocessableEntity)
			} else {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			}
		case escreveRecusa(w, err):
		default:
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	out := saida[0]
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
