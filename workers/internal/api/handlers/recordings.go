package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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
	Get(ctx context.Context, id uuid.UUID) (*recording.Recording, error)
	Peaks(ctx context.Context, id uuid.UUID) ([]byte, error)
	Clip(ctx context.Context, id uuid.UUID, c recording.ClipSpec, fade bool, out recording.ClipWriter) error
	Clips(ctx context.Context, id uuid.UUID, clips []recording.ClipSpec, fade bool, out recording.ClipWriter) error
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
	case errors.Is(err, recording.ErrEditorBusy):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
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
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
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
	// Acima do máximo devolve o máximo, que é o que a doc promete: cair no padrão
	// de 50 mostraria menos do que se pediu sem avisar.
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	switch {
	case limit <= 0:
		limit = 50
	case limit > 200:
		limit = 200
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
	if items == nil {
		items = []recording.Recording{} // o front itera items; null quebraria a tela
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

// Editor de trechos. Ver docs/features/recording-clip-editor.md.
//
// Peaks e cortes rodam ffmpeg sobre até 6h de áudio, e o middleware.Timeout de
// 60s do router cortaria no meio: usam WithoutCancel com prazo próprio. Cliente
// que desiste derruba o ffmpeg pela escrita que falha (pipe fechado).
const (
	peaksTimeout = 10 * time.Minute
	clipsTimeout = 30 * time.Minute
)

// Get devolve uma gravação.
func (h *RecordingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	rec, err := h.Service.Get(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// Peaks entrega a forma de onda em binário: um byte por 1/X-Peaks-Per-Second s.
func (h *RecordingsHandler) Peaks(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), peaksTimeout)
	defer cancel()
	peaks, err := h.Service.Peaks(ctx, id)
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Peaks-Per-Second", strconv.Itoa(recording.PeaksPerSecond))
	w.Header().Set("Content-Length", strconv.Itoa(len(peaks)))
	w.Write(peaks) //nolint:errcheck
}

func parseSeconds(v string) (float64, bool) {
	f, err := strconv.ParseFloat(v, 64)
	return f, err == nil
}

// Clip entrega um trecho em MP3: ?from=&to= em segundos do arquivo, &fade=1.
func (h *RecordingsHandler) Clip(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	from, ok1 := parseSeconds(q.Get("from"))
	to, ok2 := parseSeconds(q.Get("to"))
	if !ok1 || !ok2 {
		http.Error(w, "informe início e fim do trecho", http.StatusBadRequest)
		return
	}
	spec := recording.ClipSpec{Name: q.Get("name"), From: from, To: to}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), clipsTimeout)
	defer cancel()
	started := false
	err = h.Service.Clip(ctx, id, spec, q.Get("fade") == "1", func(*recording.Recording) io.Writer {
		started = true
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Content-Disposition", attachment(spec.Name, "trecho", ".mp3"))
		return w
	})
	h.finishStream(w, started, err)
}

type clipsRequest struct {
	Clips []recording.ClipSpec `json:"clips"`
	Fade  bool                 `json:"fade"`
	Name  string               `json:"name"`
}

// Clips entrega um .zip com um MP3 por trecho.
func (h *RecordingsHandler) Clips(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req clipsRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), clipsTimeout)
	defer cancel()
	started := false
	err = h.Service.Clips(ctx, id, req.Clips, req.Fade, func(*recording.Recording) io.Writer {
		started = true
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", attachment(req.Name, "trechos", ".zip"))
		return w
	})
	h.finishStream(w, started, err)
}

// finishStream: antes do primeiro byte o erro ainda vira status HTTP; depois, só
// log, e o download chega truncado (o cliente vê a falha).
func (h *RecordingsHandler) finishStream(w http.ResponseWriter, started bool, err error) {
	if err == nil {
		return
	}
	if !started {
		h.fail(w, err)
		return
	}
	if h.Log != nil {
		h.Log.Warn("recordings: corte interrompido", zap.Error(err))
	}
}

// attachment monta o Content-Disposition com nome ASCII de reserva e o nome
// real em UTF-8 (RFC 5987), que é o que os navegadores usam.
func attachment(name, fallback, ext string) string {
	safe := recording.SafeFileName(name)
	if safe == "" {
		safe = fallback
	}
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, safe)
	return fmt.Sprintf(`attachment; filename="%s%s"; filename*=UTF-8''%s`, ascii, ext, url.PathEscape(safe+ext))
}
