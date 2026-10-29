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

type CampaignMaterialsHandler struct {
	Repo *catalog.CampaignMaterials
	// CampaignRepo só é consultado pela checagem de scope viewer em
	// ListByCampaign (anti-oracle: 404 quando a campanha não pertence ao
	// cliente do JWT). Permanece nil-safe pra testes que não exercitam scope.
	CampaignRepo *catalog.Campaigns
	// Supervisor é opcional. Quando wired, qualquer mutação na vinculação
	// material↔campanha dispara Supervisor.Reload(campaignID) pra que os
	// workers da campanha recarreguem a lista de commercials imediatamente
	// (ao invés de esperar até 30s pelo reconciler). Sem isso, materiais
	// adicionados via Step 3 do wizard ficam "invisíveis" pra engine de
	// matching até o próximo tick — exatamente o sintoma do incidente
	// 2026-05-08 (workers cegos a target_stations editado).
	Supervisor CampaignSupervisor
}

// servico monta as ações de vínculo sobre o repositório e o supervisor deste
// handler. A regra (gravar e recarregar a campanha, logando a falha do reload)
// mora em internal/acoes: o MCP chama as mesmas funções.
func (h *CampaignMaterialsHandler) servico() *acoes.Servico {
	s := &acoes.Servico{MateriaisCampanha: h.Repo}
	if h.Supervisor != nil {
		s.Supervisor = h.Supervisor
	}
	return s
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
	if err := h.servico().VincularMaterial(r.Context(), campaignID, p.MaterialID, p.TargetStations); err != nil {
		if !escreveRecusa(w, err) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
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
	// Viewer scope: 404 quando a campanha não pertence ao cliente do JWT
	// (anti-oracle: não revela existência de campanhas alheias).
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
	if err := h.servico().TrocarEmissorasMaterial(r.Context(), campaignID, materialID, p.TargetStations); err != nil {
		// Sem vínculo é 204 (idempotente), como sempre: a ação devolve
		// nao_encontrado para o MCP não dizer "feito" quando nada foi feito.
		if errors.Is(err, acoes.ErrAlvoInexistente) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !escreveRecusa(w, err) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
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
	if err := h.servico().DesvincularMaterial(r.Context(), campaignID, materialID); err != nil {
		// Sem vínculo é 204 (idempotente), como sempre: a ação devolve
		// nao_encontrado para o MCP não dizer "feito" quando nada foi feito.
		if errors.Is(err, acoes.ErrAlvoInexistente) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !escreveRecusa(w, err) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
