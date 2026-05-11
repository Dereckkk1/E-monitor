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

// MaterialTypesHandler handles CRUD for the material_types lookup table.
// Router wiring is done in Task 19.
type MaterialTypesHandler struct {
	Repo *catalog.MaterialTypes
}

func (h *MaterialTypesHandler) List(w http.ResponseWriter, r *http.Request) {
	types, err := h.Repo.List(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if types == nil {
		types = []catalog.MaterialType{}
	}
	writeJSON(w, http.StatusOK, types)
}

type materialTypePayload struct {
	Name        string  `json:"name"`
	Color       string  `json:"color"`
	Description *string `json:"description,omitempty"`
}

func (h *MaterialTypesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var p materialTypePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if p.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	mt, err := h.Repo.Create(r.Context(), catalog.CreateMaterialTypeInput{
		Name:        p.Name,
		Color:       p.Color,
		Description: p.Description,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, mt)
}

func (h *MaterialTypesHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var p materialTypePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	mt, err := h.Repo.Update(r.Context(), id, catalog.CreateMaterialTypeInput{
		Name:        p.Name,
		Color:       p.Color,
		Description: p.Description,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, mt)
}

func (h *MaterialTypesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Delete(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
