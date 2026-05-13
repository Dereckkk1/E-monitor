package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
)

type CampaignMaterialsHandler struct {
	Repo *catalog.CampaignMaterials
	// Supervisor é opcional. Quando wired, qualquer mutação na vinculação
	// material↔campanha dispara Supervisor.Reload(campaignID) pra que os
	// workers da campanha recarreguem a lista de commercials imediatamente
	// (ao invés de esperar até 30s pelo reconciler). Sem isso, materiais
	// adicionados via Step 3 do wizard ficam "invisíveis" pra engine de
	// matching até o próximo tick — exatamente o sintoma do incidente
	// 2026-05-08 (workers cegos a target_stations editado).
	Supervisor CampaignSupervisor
	Log        *zap.Logger
}

type linkPayload struct {
	MaterialID     uuid.UUID   `json:"material_id"`
	TargetStations []uuid.UUID `json:"target_stations"`
}

// reloadOrLog dispara Supervisor.Reload e loga o erro sem propagar pro caller —
// o DB já foi mutado, o cliente precisa receber sucesso. Reconciler de 30s é
// o safety net se o reload sofrer.
func (h *CampaignMaterialsHandler) reloadOrLog(campaignID uuid.UUID, op string) {
	if h.Supervisor == nil {
		return
	}
	if err := h.Supervisor.Reload(campaignID); err != nil && h.Log != nil {
		h.Log.Error("campaign_materials: supervisor reload failed",
			zap.String("op", op),
			zap.String("campaign_id", campaignID.String()),
			zap.Error(err),
		)
	}
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
	h.reloadOrLog(campaignID, "link")
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
	h.reloadOrLog(campaignID, "update_stations")
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
	h.reloadOrLog(campaignID, "unlink")
	w.WriteHeader(http.StatusNoContent)
}
