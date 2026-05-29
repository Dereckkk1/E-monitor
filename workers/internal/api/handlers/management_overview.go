package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"radiocheck/internal/catalog"
)

// ManagementOverviewRepo é a dependência mínima do handler (mockável em teste).
type ManagementOverviewRepo interface {
	Get(ctx context.Context, p catalog.ManagementParams) (catalog.ManagementResult, error)
}

type ManagementOverviewHandler struct {
	Repo ManagementOverviewRepo
}

func NewManagementOverviewHandler(repo ManagementOverviewRepo) *ManagementOverviewHandler {
	return &ManagementOverviewHandler{Repo: repo}
}

var mgmtValidStatus = map[string]bool{
	"programada": true, "ativa": true, "concluida": true, "cancelada": true,
}

// Get GET /management-overview — visão da operação inteira. Admin/operator-only
// (gating no router; sem scope de viewer). Todos os params são opcionais:
//   - client_id (uuid): default = todos os clientes
//   - campaigns (csv de uuids): default = todas
//   - status (programada|ativa|concluida|cancelada): default = todos
//   - from, to (YYYY-MM-DD): default = ano corrente (01/01 → hoje)
func (h *ManagementOverviewHandler) Get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var clientID *uuid.UUID
	if cid := q.Get("client_id"); cid != "" {
		parsed, err := uuid.Parse(cid)
		if err != nil {
			http.Error(w, "invalid client_id", http.StatusBadRequest)
			return
		}
		clientID = &parsed
	}

	camps, err := parseUUIDList(q.Get("campaigns"))
	if err != nil {
		http.Error(w, "invalid campaigns: "+err.Error(), http.StatusBadRequest)
		return
	}
	if camps == nil {
		camps = []uuid.UUID{}
	}
	if len(camps) > 200 {
		http.Error(w, "campaigns max=200", http.StatusBadRequest)
		return
	}

	status := q.Get("status")
	if status != "" && !mgmtValidStatus[status] {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	defaultFrom := time.Date(now.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	from := parseDateOr(q.Get("from"), defaultFrom)
	to := parseDateOr(q.Get("to"), now)
	if to.Before(from) {
		http.Error(w, "to must be on or after from", http.StatusBadRequest)
		return
	}

	out, err := h.Repo.Get(r.Context(), catalog.ManagementParams{
		ClientID:    clientID,
		CampaignIDs: camps,
		Status:      status,
		From:        from,
		To:          to,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
