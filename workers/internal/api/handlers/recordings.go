package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/recording"
)

// RecordingService é o que o handler usa do recording.Manager.
type RecordingService interface {
	Create(ctx context.Context, in recording.CreateInput) (*recording.Recording, error)
	List(ctx context.Context, limit, offset int) ([]recording.Recording, int, error)
	Cancel(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
	Audio(ctx context.Context, id uuid.UUID) (io.ReadCloser, *recording.Recording, int64, error)
}

// RecordingsHandler serve /recordings. Doc: docs/features/scheduled-recording.md
type RecordingsHandler struct {
	Service RecordingService
	Log     *zap.Logger
}

func (h *RecordingsHandler) fail(w http.ResponseWriter, err error) {
	var ve *recording.ValidationError
	switch {
	case errors.As(err, &ve):
		http.Error(w, ve.Msg, http.StatusBadRequest)
	case errors.Is(err, recording.ErrOverlap), errors.Is(err, recording.ErrBusy):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, recording.ErrNotFound), errors.Is(err, recording.ErrNoAudio):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		if h.Log != nil {
			h.Log.Error("recordings: erro interno", zap.Error(err))
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type createRecordingRequest struct {
	StationID string `json:"station_id"`
	Day       string `json:"day"`
	Start     string `json:"start"`
	End       string `json:"end"`
}

// Create agenda uma gravação.
func (h *RecordingsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createRecordingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	stationID, err := uuid.Parse(req.StationID)
	if err != nil {
		http.Error(w, "emissora inválida", http.StatusBadRequest)
		return
	}
	in := recording.CreateInput{StationID: stationID, Day: req.Day, Start: req.Start, End: req.End}
	if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
		in.CreatedBy = &claims.UserID
	}
	rec, err := h.Service.Create(r.Context(), in)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// List pagina as gravações.
func (h *RecordingsHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	items, total, err := h.Service.List(r.Context(), limit, offset)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// Cancel encerra uma gravação programada ou em andamento.
func (h *RecordingsHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.Service.Cancel(r.Context(), id); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Delete apaga áudio e linha.
func (h *RecordingsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.Service.Delete(r.Context(), id); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Audio entrega os bytes pela API: URL pré-assinada do MinIO não funciona no
// navegador em prod (docs/features/evidence-presigned-urls.md).
func (h *RecordingsHandler) Audio(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	// WithoutCancel: o middleware.Timeout do router cancela r.Context() em 60s, e
	// esse contexto governa a leitura do S3. Um arquivo de 340 MB seria cortado no
	// meio. A queda do cliente segue detectada pelo erro de escrita do io.Copy.
	body, _, size, err := h.Service.Audio(context.WithoutCancel(r.Context()), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", "audio/mp4")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s.m4a"`, id))
	io.Copy(w, body) //nolint:errcheck
}
