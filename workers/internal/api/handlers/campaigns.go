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

type CampaignsHandler struct {
	Repo       *catalog.Campaigns
	Supervisor CampaignSupervisor
}

// CampaignSupervisor is implemented in Task 25.
type CampaignSupervisor interface {
	Start(campaignID uuid.UUID) error
	Pause(campaignID uuid.UUID) error
}

func (h *CampaignsHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.Repo.List(r.Context())
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"data": items})
}

func (h *CampaignsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in catalog.CreateCampaignInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if in.Name == "" || in.ClientID == uuid.Nil {
		http.Error(w, "name and client_id are required", 400)
		return
	}
	out, err := h.Repo.Create(r.Context(), in)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 201, out)
}

func (h *CampaignsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	out, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	writeJSON(w, 200, out)
}

func (h *CampaignsHandler) Start(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	if h.Supervisor == nil {
		http.Error(w, "supervisor not configured", 503)
		return
	}
	if err := h.Supervisor.Start(id); err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.WriteHeader(204)
}

func (h *CampaignsHandler) Pause(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	if h.Supervisor == nil {
		http.Error(w, "supervisor not configured", 503)
		return
	}
	if err := h.Supervisor.Pause(id); err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.WriteHeader(204)
}
