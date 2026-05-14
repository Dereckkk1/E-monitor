package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"radiocheck/internal/catalog"
	"radiocheck/internal/events"
)

// MaterialsHandler handles CRUD and upload for /materials and
// /clients/{clientID}/materials routes.
type MaterialsHandler struct {
	Repo        *catalog.Materials
	MastersPath string
	NATS        *nats.Conn
}

// ListByClient returns all materials for a given client.
// Optional query param ?q= filters by case-insensitive title substring.
func (h *MaterialsHandler) ListByClient(w http.ResponseWriter, r *http.Request) {
	clientID, err := uuid.Parse(chi.URLParam(r, "clientID"))
	if err != nil {
		http.Error(w, "invalid clientID", http.StatusBadRequest)
		return
	}
	q := r.URL.Query().Get("q")
	mats, err := h.Repo.ListByClient(r.Context(), clientID, q)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if mats == nil {
		mats = []catalog.Material{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": mats})
}

// Get returns a single material by UUID.
func (h *MaterialsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	m, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// Upload accepts a multipart/form-data POST with fields:
//   - client_id  (UUID, required)
//   - title      (string, required)
//   - type_id    (UUID, optional)
//   - audio      (file, required) — wav/mp3/m4a/aac/mpeg
//
// The file is SHA-256 hashed and stored under MastersPath.
// A fingerprint-generate event is published to NATS on success.
func (h *MaterialsHandler) Upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "invalid multipart", http.StatusBadRequest)
		return
	}

	clientIDStr := r.FormValue("client_id")
	title := r.FormValue("title")
	typeIDStr := r.FormValue("type_id")

	clientID, err := uuid.Parse(clientIDStr)
	if err != nil {
		http.Error(w, "client_id is required and must be a valid UUID", http.StatusBadRequest)
		return
	}
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	var typeID *uuid.UUID
	if typeIDStr != "" {
		tid, err := uuid.Parse(typeIDStr)
		if err != nil {
			http.Error(w, "type_id must be a valid UUID", http.StatusBadRequest)
			return
		}
		typeID = &tid
	}

	file, header, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "audio file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".wav" && ext != ".mp3" && ext != ".m4a" && ext != ".aac" && ext != ".mpeg" {
		http.Error(w, "unsupported audio format (use wav/mp3/m4a/aac/mpeg)", http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(h.MastersPath, 0755); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Write to a temp path while hashing; rename to sha-based final name.
	tmpPath := filepath.Join(h.MastersPath, uuid.NewString()+ext)
	f, err := os.Create(tmpPath)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, hasher), file); err != nil {
		f.Close()
		os.Remove(tmpPath)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	f.Close()

	sha := hex.EncodeToString(hasher.Sum(nil))
	finalName := sha + ext
	finalPath := filepath.Join(h.MastersPath, finalName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// TODO F-88: probe actual duration via ffprobe (extract existing probeDuration
	// helper from commercials.go into a shared location).
	// Stubbed at 30.0 until that refactor lands.
	duration := 30.0

	mat, err := h.Repo.Create(r.Context(), catalog.CreateMaterialInput{
		ClientID:          clientID,
		Title:             title,
		TypeID:            typeID,
		DurationSeconds:   duration,
		MasterStoragePath: finalPath,
		MasterSHA256:      sha,
	})
	if err != nil {
		os.Remove(finalPath)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Publish fingerprint-generate event (best-effort; nil NATS is safe).
	if h.NATS != nil {
		payload, _ := json.Marshal(map[string]any{
			"material_id": mat.ID.String(),
		})
		_ = h.NATS.Publish(events.SubjectFingerprintGenerate, payload)
	}

	writeJSON(w, http.StatusCreated, mat)
}

// UpdateType sets or clears the type_id for a material.
// Body: {"type_id": "<uuid>" | null}
func (h *MaterialsHandler) UpdateType(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var p struct {
		TypeID *uuid.UUID `json:"type_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.Repo.UpdateType(r.Context(), id, p.TypeID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Delete removes a material by UUID.
func (h *MaterialsHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

// Acknowledge marks the material's similarity warning as resolved.
// POST /materials/{id}/similarity/acknowledge → 204.
func (h *MaterialsHandler) Acknowledge(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Acknowledge(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
