package handlers

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

// LiveMapRepo é a dependência mínima do handler. Mockável nos testes sem pool
// real (mesmo padrão de InsightsHandler).
type LiveMapRepo interface {
	Get(ctx context.Context, scope *uuid.UUID) (catalog.LiveMapResult, error)
}

type LiveMapHandler struct {
	Repo LiveMapRepo
}

func NewLiveMapHandler(repo LiveMapRepo) *LiveMapHandler {
	return &LiveMapHandler{Repo: repo}
}

// Get GET /live-map — admin vê tudo; viewer fica restrito ao próprio client_id
// via auth.ClientScopeFromContext (nil = sem scope = admin/operator).
func (h *LiveMapHandler) Get(w http.ResponseWriter, r *http.Request) {
	scope := auth.ClientScopeFromContext(r.Context())
	out, err := h.Repo.Get(r.Context(), scope)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
