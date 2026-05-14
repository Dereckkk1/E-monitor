package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

	items, total, err := h.Repo.ListPaged(r.Context(), q.Get("q"), page, size)
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
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	w.WriteHeader(204)
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
