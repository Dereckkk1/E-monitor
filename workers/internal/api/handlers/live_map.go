package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

// LiveMapRepo é a dependência mínima do handler. Mockável nos testes sem pool
// real (mesmo padrão de InsightsHandler).
type LiveMapRepo interface {
	Get(ctx context.Context, campaignID uuid.UUID, scope *uuid.UUID) (catalog.LiveMapResult, error)
}

type LiveMapHandler struct {
	Repo LiveMapRepo
}

func NewLiveMapHandler(repo LiveMapRepo) *LiveMapHandler {
	return &LiveMapHandler{Repo: repo}
}

// Get GET /live-map?campaign_id=UUID — emissoras-alvo da campanha (com
// coordenada) + as veiculações dela. Viewer fica restrito às campanhas do
// próprio client_id via auth.ClientScopeFromContext (404 anti-oracle quando a
// campanha é de outro cliente).
func (h *LiveMapHandler) Get(w http.ResponseWriter, r *http.Request) {
	cid := r.URL.Query().Get("campaign_id")
	if cid == "" {
		http.Error(w, "campaign_id required", http.StatusBadRequest)
		return
	}
	campaignID, err := uuid.Parse(cid)
	if err != nil {
		http.Error(w, "invalid campaign_id", http.StatusBadRequest)
		return
	}

	scope := auth.ClientScopeFromContext(r.Context())
	out, err := h.Repo.Get(r.Context(), campaignID, scope)
	if err != nil {
		if errors.Is(err, catalog.ErrCampaignNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
