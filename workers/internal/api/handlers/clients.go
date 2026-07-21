package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

type ClientsHandler struct {
	Repo *catalog.Clients
}

// List supports two modes:
//   - Legacy/unpaged (no ?page or ?page_size): returns the full list, ordered
//     by name. Compatible with callers that need the entire catalog (e.g. the
//     campaign dropdown's client map).
//   - Paged (?page + ?page_size): returns {data, total, total_pages, page,
//     page_size}. Optional ?q filters by name/city/state/cnpj/email/contact.
func (h *ClientsHandler) List(w http.ResponseWriter, r *http.Request) {
	// Viewer scope: cliente só enxerga o próprio registro. Devolvemos no
	// mesmo envelope {data: [...]} pra UI tratar igual à lista global — sem
	// gates de role no frontend. Paginação não se aplica (lista de 1).
	if scope := auth.ClientScopeFromContext(r.Context()); scope != nil {
		cli, err := h.Repo.Get(r.Context(), *scope)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, 200, map[string]any{"data": []catalog.Client{}})
				return
			}
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"data": []catalog.Client{*cli}})
		return
	}

	q := r.URL.Query()
	pageStr := q.Get("page")
	sizeStr := q.Get("page_size")
	if pageStr == "" && sizeStr == "" && q.Get("q") == "" {
		items, err := h.Repo.List(r.Context())
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"data": items})
		return
	}

	page, _ := strconv.Atoi(pageStr)
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(sizeStr)
	if size < 1 {
		size = 20
	}
	if size > 200 {
		size = 200
	}

	// include_inactive=1|true: management page's "mostrar inativos" toggle.
	// Default false hides deactivated clients from the list.
	includeInactive := q.Get("include_inactive") == "1" || q.Get("include_inactive") == "true"
	items, total, err := h.Repo.ListPaged(r.Context(), q.Get("q"), page, size, includeInactive)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	totalPages := (total + size - 1) / size
	if totalPages < 1 {
		totalPages = 1
	}
	writeJSON(w, 200, map[string]any{
		"data":        items,
		"total":       total,
		"total_pages": totalPages,
		"page":        page,
		"page_size":   size,
	})
}

// maxTargetLabelLen é o teto defensivo do rótulo de público-alvo gravado em
// clients.target_label. A UI sugere 60 chars; aqui só barramos o absurdo (o
// campo é TEXT no banco, sem CHECK).
const maxTargetLabelLen = 200

// normalizeTargetLabel apara espaços e colapsa "" em nil (NULL no banco =
// cliente sem rótulo, que é o mesmo significado de string vazia). Devolve
// false quando o rótulo excede maxTargetLabelLen — contado em runes, pra um
// rótulo acentuado não ser rejeitado por causa de bytes UTF-8.
func normalizeTargetLabel(p **string) bool {
	if *p == nil {
		return true
	}
	s := strings.TrimSpace(**p)
	if s == "" {
		*p = nil
		return true
	}
	if utf8.RuneCountInString(s) > maxTargetLabelLen {
		return false
	}
	*p = &s
	return true
}

func (h *ClientsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in catalog.CreateClientInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if in.Name == "" {
		http.Error(w, "name is required", 400)
		return
	}
	if !normalizeTargetLabel(&in.TargetLabel) {
		http.Error(w, "target_label must be at most 200 characters", 400)
		return
	}
	out, err := h.Repo.Create(r.Context(), in)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 201, out)
}

func (h *ClientsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	if err := h.Repo.Delete(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			http.Error(w, "not found", 404)
		case errors.Is(err, catalog.ErrClientHasDependents):
			// Hard-delete refused by a foreign key. Tell the operator exactly
			// what's blocking so the UI can offer the reversible "deactivate"
			// path instead of a silent 500. Counts are best-effort: if the
			// breakdown query fails we still return 409 (the conflict is real).
			counts, cerr := h.Repo.CountDependents(r.Context(), id)
			if cerr != nil {
				counts = catalog.DependentCounts{}
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":     "client_has_dependents",
				"campaigns": counts.Campaigns,
				"materials": counts.Materials,
				"users":     counts.Users,
			})
		default:
			http.Error(w, "internal error", 500)
		}
		return
	}
	w.WriteHeader(204)
}

// Deactivate flips is_active=false (reversible "Desativar"). The client stays
// in the DB with all its campaigns/materials/users intact, disappears from the
// management list by default, and its users can no longer log in.
func (h *ClientsHandler) Deactivate(w http.ResponseWriter, r *http.Request) {
	h.setActive(w, r, false)
}

// Activate flips is_active=true (reactivate a previously deactivated client).
func (h *ClientsHandler) Activate(w http.ResponseWriter, r *http.Request) {
	h.setActive(w, r, true)
}

func (h *ClientsHandler) setActive(w http.ResponseWriter, r *http.Request, active bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	out, err := h.Repo.SetActive(r.Context(), id, active)
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

func (h *ClientsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in catalog.UpdateClientInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if in.Name == "" {
		http.Error(w, "name is required", 400)
		return
	}
	if !normalizeTargetLabel(&in.TargetLabel) {
		http.Error(w, "target_label must be at most 200 characters", 400)
		return
	}
	out, err := h.Repo.Update(r.Context(), id, in)
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
