package handlers

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Audio streams a material's master audio file with byte-range support.
// Mirror of CommercialsHandler.Audio. Used by the similarity warning modal
// players (frontend/src/components/SimilarityWarningModal.jsx).
func (h *MaterialsHandler) Audio(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	mat, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	f, err := os.Open(mat.MasterStoragePath)
	if err != nil {
		http.Error(w, "audio file not available", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	ct := "application/octet-stream"
	switch strings.ToLower(filepath.Ext(mat.MasterStoragePath)) {
	case ".m4a", ".aac":
		ct = "audio/mp4"
	case ".mp3", ".mpeg":
		ct = "audio/mpeg"
	case ".wav":
		ct = "audio/wav"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, filepath.Base(mat.MasterStoragePath), info.ModTime(), f)
}
