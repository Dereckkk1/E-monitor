package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"radiocheck/internal/catalog"
)

type StationsHandler struct {
	Repo *catalog.Stations
}

func (h *StationsHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.Repo.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"data": items})
}

func (h *StationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in catalog.CreateStationInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if in.Name == "" || in.Band == "" || in.StreamURL == "" {
		http.Error(w, "name, band, and stream_url are required", 400)
		return
	}
	if in.Band != "AM" && in.Band != "FM" {
		http.Error(w, "band must be AM or FM", 400)
		return
	}
	out, err := h.Repo.Create(r.Context(), in)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 201, out)
}

func (h *StationsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	st, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, 200, st)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
