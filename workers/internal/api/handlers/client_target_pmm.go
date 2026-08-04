package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

// ClientTargetPmmHandler serve o cadastro de PMM no target por cliente.
//
// GET é viewer-friendly com scope-check (o cliente logado só enxerga o próprio
// cadastro) porque a grid de /detections e o dashboard precisam do mapa para
// renderizar os impactos no target. PUT é admin-only — quem cadastra é o
// operador interno.
type ClientTargetPmmHandler struct {
	Repo *catalog.ClientStationPMM
}

// List devolve as emissoras-alvo do cliente com PMM global e target.
// GET /clients/{clientID}/target-pmm
func (h *ClientTargetPmmHandler) List(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "clientID"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	// Anti-oracle: viewer pedindo cliente fora da carteira recebe 404, não
	// 403 — não confirma a existência do id. Mesmo padrão de /campaigns/{id}.
	if !auth.ScopeAllows(r.Context(), id) {
		http.Error(w, "not found", 404)
		return
	}
	rows, err := h.Repo.ListForClient(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}

type bulkTargetPmmRequest struct {
	Entries []catalog.TargetPMMEntry `json:"entries"`
}

// Bulk aplica o lote de cadastro. PUT /clients/{clientID}/target-pmm
func (h *ClientTargetPmmHandler) Bulk(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "clientID"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in bulkTargetPmmRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	// Guarda de sanidade: a UI manda só as linhas alteradas, então um lote
	// gigante indica bug de cliente, não uso legítimo.
	if len(in.Entries) > 5000 {
		http.Error(w, "too many entries", 400)
		return
	}
	for _, e := range in.Entries {
		if e.PMMTarget != nil && *e.PMMTarget < 0 {
			http.Error(w, "pmm_target must be >= 0", 400)
			return
		}
	}
	updated, deleted, err := h.Repo.BulkUpsert(r.Context(), id, in.Entries)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"updated": updated, "deleted": deleted})
}
