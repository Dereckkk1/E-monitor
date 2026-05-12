package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

type CampaignMaterialsHandler struct {
	Repo *catalog.CampaignMaterials
}

type linkPayload struct {
	MaterialID     uuid.UUID   `json:"material_id"`
	TargetStations []uuid.UUID `json:"target_stations"`
}

func (h *CampaignMaterialsHandler) Link(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	var p linkPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Link(r.Context(), campaignID, p.MaterialID, p.TargetStations); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *CampaignMaterialsHandler) ListByCampaign(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	links, err := h.Repo.ListByCampaign(r.Context(), campaignID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if links == nil {
		links = []catalog.CampaignMaterial{}
	}
	writeJSON(w, http.StatusOK, links)
}

func (h *CampaignMaterialsHandler) UpdateStations(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	materialID, err := uuid.Parse(chi.URLParam(r, "materialID"))
	if err != nil {
		http.Error(w, "invalid materialID", http.StatusBadRequest)
		return
	}
	var p struct {
		TargetStations []uuid.UUID `json:"target_stations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.Repo.UpdateStations(r.Context(), campaignID, materialID, p.TargetStations); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *CampaignMaterialsHandler) Unlink(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	materialID, err := uuid.Parse(chi.URLParam(r, "materialID"))
	if err != nil {
		http.Error(w, "invalid materialID", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Unlink(r.Context(), campaignID, materialID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
