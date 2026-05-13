package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
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
	Log        *zap.Logger // optional; used to surface Pause/Start/Reload failures
}

// CampaignSupervisor is the subset of supervisor.Supervisor used by API handlers.
type CampaignSupervisor interface {
	Start(campaignID uuid.UUID) error
	Pause(campaignID uuid.UUID) error
	Reload(campaignID uuid.UUID) error
	// UpdateStations replaces target_stations on a campaign and reconciles
	// running workers without bouncing the campaign through 'cancelada' as
	// the legacy Pause+Start dance did.
	UpdateStations(campaignID uuid.UUID, newStations []uuid.UUID) error
	StopWorkersForCampaign(campaignID uuid.UUID)
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

// Financials returns the per-campaign aggregate of investimento + total
// inserções, usado pelo badge de CPM em /campaigns. Calculado em uma query
// só (CTE) pra evitar N+1 chamadas no frontend.
func (h *CampaignsHandler) Financials(w http.ResponseWriter, r *http.Request) {
	out, err := h.Repo.FinancialsByCampaign(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Update edits the basic data trio (name, start_date, end_date) of an existing
// campaign — what the wizard's Step 1 surfaces in edit mode. client_id stays
// locked because Step 3 is hydrated against the client's material library.
//
// Returns:
//   - 200 + updated campaign on success.
//   - 400 on invalid JSON, missing required fields, or end_date <= start_date.
//   - 404 if the id does not exist.
func (h *CampaignsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in struct {
		Name      string    `json:"name"`
		StartDate time.Time `json:"start_date"`
		EndDate   time.Time `json:"end_date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		http.Error(w, "name is required", 400)
		return
	}
	if in.StartDate.IsZero() || in.EndDate.IsZero() {
		http.Error(w, "start_date and end_date are required", 400)
		return
	}
	if in.EndDate.Before(in.StartDate) {
		http.Error(w, "end_date must be on or after start_date", 400)
		return
	}
	out, err := h.Repo.UpdateBasic(r.Context(), id, catalog.UpdateBasicInput{
		Name:      strings.TrimSpace(in.Name),
		StartDate: in.StartDate,
		EndDate:   in.EndDate,
	})
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
		if perr := h.Supervisor.Pause(id); perr != nil && h.Log != nil {
			h.Log.Error("campaigns.Delete: supervisor pause failed",
				zap.String("campaign_id", id.String()),
				zap.Error(perr),
			)
		}
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
	// Stop workers for stations that no longer have any active campaign.
	// CancelCampaign already flipped the status, so we go straight to the
	// worker-stop path without an extra DB round-trip via Pause().
	if h.Supervisor != nil {
		h.Supervisor.StopWorkersForCampaign(id)
	}
	w.WriteHeader(204)
}

// Pause is deprecated as of 2026-05-07 (§18.2.1). The 'paused' state was
// collapsed into 'cancelada' under the new lifecycle model, and silently
// aliasing /pause → cancel was a contract break. This handler now returns
// HTTP 410 Gone so callers fail loud and migrate to POST /campaigns/{id}/cancel.
//
// Deprecated: use Cancel via POST /campaigns/{id}/cancel.
func (h *CampaignsHandler) Pause(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusGone, map[string]any{
		"error":   "endpoint deprecated",
		"message": "use POST /campaigns/{id}/cancel instead",
		"since":   "2026-05-07",
	})
}

// UpdateStations replaces the target_stations list for a campaign.
//
// When a supervisor is wired, the DB write and the per-station worker
// reconciliation happen inside Supervisor.UpdateStations as a single path —
// the campaign never transitions through 'cancelada' (which is what the
// previous Pause+Start dance did). When no supervisor is wired (test mode),
// we fall back to a plain DB update.
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

	if h.Supervisor != nil {
		if err := h.Supervisor.UpdateStations(id, in.TargetStations); err != nil {
			if h.Log != nil {
				h.Log.Error("campaigns.UpdateStations: supervisor failed",
					zap.String("campaign_id", id.String()),
					zap.Error(err),
				)
			}
			if strings.Contains(err.Error(), "campaign not found") {
				http.Error(w, "not found", 404)
				return
			}
			http.Error(w, "internal error", 500)
			return
		}
		w.WriteHeader(204)
		return
	}

	// No supervisor (test setup) — plain DB update.
	if _, err := h.Repo.Get(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	if err := h.Repo.UpdateTargetStations(r.Context(), id, in.TargetStations); err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	w.WriteHeader(204)
}
