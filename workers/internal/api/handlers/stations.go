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

type StationsHandler struct {
	Repo *catalog.Stations
}

func (h *StationsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := catalog.ListInput{
		Q:     q.Get("q"),
		Band:  q.Get("band"),
		State: q.Get("state"),
	}
	if p, _ := strconv.Atoi(q.Get("page")); p > 0 {
		in.Page = p
	}
	if l, _ := strconv.Atoi(q.Get("limit")); l > 0 && l <= 2000 {
		in.Limit = l
	}

	out, err := h.Repo.List(r.Context(), in)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, out)
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
		http.Error(w, "internal error", 500)
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
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	writeJSON(w, 200, st)
}

func (h *StationsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in catalog.UpdateStationInput
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
	st, err := h.Repo.Update(r.Context(), id, in)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	writeJSON(w, 200, st)
}

// GetThreshold returns the calibration status (min_hashes, days elapsed, mode)
// for a station — surface the fase2 calibration job state for operators.
func (h *StationsHandler) GetThreshold(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid station id", http.StatusBadRequest)
		return
	}
	status, err := h.Repo.GetCalibrationStatus(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
