package handlers

import (
	"encoding/json"
	"net/http"

	"radiocheck/internal/catalog"
)

type ClientsHandler struct {
	Repo *catalog.Clients
}

func (h *ClientsHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.Repo.List(r.Context())
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"data": items})
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
