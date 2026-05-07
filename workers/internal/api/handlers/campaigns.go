package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"radiocheck/internal/catalog"
)

// validStatuses lists the four lifecycle states accepted by the ?status= filter.
var validStatuses = map[string]struct{}{
	"programada": {},
	"ativa":      {},
	"concluida":  {},
	"cancelada":  {},
}

type CampaignsHandler struct {
	Repo       *catalog.Campaigns
	Supervisor CampaignSupervisor
}

// CampaignSupervisor is the subset of supervisor.Supervisor used by API handlers.
type CampaignSupervisor interface {
	Start(campaignID uuid.UUID) error
	Pause(campaignID uuid.UUID) error
	Reload(campaignID uuid.UUID) error
}

func (h *CampaignsHandler) List(w http.ResponseWriter, r *http.Request) {
	// ?status=programada,ativa,concluida,cancelada — CSV, optional.
	var statuses []string
	if raw := r.URL.Query().Get("status"); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if _, ok := validStatuses[s]; !ok {
				http.Error(w, "invalid status filter: "+s, 400)
				return
			}
			statuses = append(statuses, s)
		}
	}
	items, err := h.Repo.ListFiltered(r.Context(), statuses)
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

func (h *CampaignsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	if h.Supervisor != nil {
		_ = h.Supervisor.Pause(id)
	}
	if err := h.Repo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	w.WriteHeader(204)
}

// Cancel transitions a campaign from programada/ativa to cancelada (§18.2.1).
// Returns:
//   - 204 on success.
//   - 404 when the id does not exist.
//   - 409 when the campaign is already in a terminal state (concluida/cancelada).
//
// Workers, if any, are stopped after the DB transition succeeds.
func (h *CampaignsHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	changed, prev, err := h.Repo.CancelCampaign(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
			return
		}
		http.Error(w, "internal error", 500)
		return
	}
	if !changed {
		writeJSON(w, 409, map[string]any{
			"error":          "campaign already in terminal state",
			"current_status": prev,
		})
		return
	}
	// Stop workers via the existing Pause path. Pause() will rerun the status
	// flip ('cancelada' → 'cancelada') which is a harmless no-op, then halt
	// workers for stations that no longer have any active campaign.
	if h.Supervisor != nil {
		if err := h.Supervisor.Pause(id); err != nil {
			// Log only — the cancellation itself is durable.
			_ = err
		}
	}
	w.WriteHeader(204)
}

// Pause is kept for backward compatibility. It is functionally equivalent to
// Cancel under the new lifecycle model — see §18.2.1.
//
// Deprecated: use Cancel.
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

// UpdateStations replaces the target_stations list for a campaign.
// If the campaign is active, workers are paused and restarted with the new station list.
func (h *CampaignsHandler) UpdateStations(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in struct {
		TargetStations []uuid.UUID `json:"target_stations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if in.TargetStations == nil {
		in.TargetStations = []uuid.UUID{}
	}

	camp, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	wasActive := camp.Status == "ativa"

	// Pause current workers so removed stations get stopped cleanly.
	if wasActive && h.Supervisor != nil {
		_ = h.Supervisor.Pause(id)
	}

	if err := h.Repo.UpdateTargetStations(r.Context(), id, in.TargetStations); err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	// Restart with the new station list.
	if wasActive && h.Supervisor != nil {
		_ = h.Supervisor.Start(id)
	}

	w.WriteHeader(204)
}
