package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
	"radiocheck/internal/catalog"
	"radiocheck/internal/events"
)

type CommercialsHandler struct {
	Repo        *catalog.Commercials
	NATS        *nats.Conn
	MastersPath string
	Supervisor  CampaignSupervisor // optional; used to reload workers after station assignment changes
	Log         *zap.Logger        // optional; used to surface Reload failures (silent failure caused 2026-05-08 missed detections)
}

func (h *CommercialsHandler) List(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(r.URL.Query().Get("campaign_id"))
	if err != nil {
		http.Error(w, "campaign_id required", 400)
		return
	}
	coms, err := h.Repo.ListByCampaign(r.Context(), campaignID)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	if coms == nil {
		coms = []catalog.Commercial{}
	}
	writeJSON(w, 200, map[string]any{"data": coms})
}

func (h *CommercialsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	com, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	writeJSON(w, 200, com)
}

func (h *CommercialsHandler) Upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	campaignIDStr := r.FormValue("campaign_id")
	title := r.FormValue("title")
	cutLabel := r.FormValue("cut_label")
	if campaignIDStr == "" || title == "" {
		http.Error(w, "campaign_id and title are required", 400)
		return
	}
	campaignID, err := uuid.Parse(campaignIDStr)
	if err != nil {
		http.Error(w, "invalid campaign_id", 400)
		return
	}
	file, header, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "audio file required", 400)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".wav" && ext != ".mp3" && ext != ".m4a" && ext != ".aac" && ext != ".mpeg" {
		http.Error(w, "unsupported audio format (use wav/mp3/m4a/aac/mpeg)", 400)
		return
	}

	if err := os.MkdirAll(h.MastersPath, 0755); err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	tmpName := uuid.NewString() + ext
	tmpPath := filepath.Join(h.MastersPath, tmpName)
	f, err := os.Create(tmpPath)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	hasher := sha256.New()
	mw := io.MultiWriter(f, hasher)
	if _, err := io.Copy(mw, file); err != nil {
		f.Close()
		os.Remove(tmpPath)
		http.Error(w, "internal error", 500)
		return
	}
	f.Close()
	sha := hex.EncodeToString(hasher.Sum(nil))

	duration, err := probeDuration(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		http.Error(w, "could not probe audio duration: "+err.Error(), 400)
		return
	}

	var cutPtr *string
	if cutLabel != "" {
		cutPtr = &cutLabel
	}
	com, err := h.Repo.Create(r.Context(), catalog.CreateCommercialInput{
		CampaignID:        campaignID,
		Title:             title,
		CutLabel:          cutPtr,
		DurationSeconds:   duration,
		MasterStoragePath: tmpPath,
		MasterSHA256:      sha,
	})
	if err != nil {
		os.Remove(tmpPath)
		http.Error(w, "internal error", 500)
		return
	}

	payload, _ := json.Marshal(map[string]string{"commercial_id": com.ID.String()})
	if err := h.NATS.Publish(events.SubjectFingerprintGenerate, payload); err != nil {
		fmt.Printf("nats publish failed: %v\n", err)
	}

	writeJSON(w, 202, com)
}

// Delete removes a commercial and its fingerprint hashes. The master file on
// disk is also unlinked. Refuses if detections already exist for the commercial.
func (h *CommercialsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	campaignID, masterPath, err := h.Repo.Delete(r.Context(), id)
	if err != nil {
		if errors.Is(err, catalog.ErrCommercialHasDetections) {
			http.Error(w, "commercial has detection history; archive instead", 409)
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
			return
		}
		http.Error(w, "internal error", 500)
		return
	}

	if masterPath != "" {
		_ = os.Remove(masterPath)
	}

	if h.Supervisor != nil {
		if err := h.Supervisor.Reload(campaignID); err != nil && h.Log != nil {
			h.Log.Error("commercials.Delete: supervisor reload failed",
				zap.String("campaign_id", campaignID.String()),
				zap.String("commercial_id", id.String()),
				zap.Error(err),
			)
		}
	}

	w.WriteHeader(204)
}

// UpdateStations sets which stations a commercial should be detected on.
// An empty list means the commercial is inactive (runs on no station).
func (h *CommercialsHandler) UpdateStations(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in struct {
		TargetStations []uuid.UUID `json:"target_stations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if in.TargetStations == nil {
		in.TargetStations = []uuid.UUID{}
	}

	com, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}

	if err := h.Repo.UpdateStations(r.Context(), id, in.TargetStations); err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	// Reload workers for the campaign so the new station assignment takes effect immediately.
	// A silent failure here is exactly what masked the 2026-05-08 missed
	// detections — the supervisor's per-worker reconciler is the safety net,
	// but the operator should still see the failure so they can dig in.
	if h.Supervisor != nil {
		if err := h.Supervisor.Reload(com.CampaignID); err != nil && h.Log != nil {
			h.Log.Error("commercials.UpdateStations: supervisor reload failed",
				zap.String("campaign_id", com.CampaignID.String()),
				zap.String("commercial_id", id.String()),
				zap.Error(err),
			)
		}
	}

	w.WriteHeader(204)
}

// Audio streams the master audio file for a commercial.
// Supports HTTP Range requests for in-browser playback with seek.
// The `?download=1` query param forces an attachment Content-Disposition.
func (h *CommercialsHandler) Audio(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	com, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}

	f, err := os.Open(com.MasterStoragePath)
	if err != nil {
		http.Error(w, "audio file not available", 404)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	ext := strings.ToLower(filepath.Ext(com.MasterStoragePath))
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		switch ext {
		case ".m4a", ".aac":
			ct = "audio/mp4"
		case ".mp3", ".mpeg":
			ct = "audio/mpeg"
		case ".wav":
			ct = "audio/wav"
		default:
			ct = "application/octet-stream"
		}
	}

	safeTitle := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == '"' || r < 32 {
			return '_'
		}
		return r
	}, com.Title)
	filename := safeTitle + ext

	disposition := "inline"
	if r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", disposition+"; filename=\""+filename+"\"")
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, filename, stat.ModTime(), f)
}

func probeDuration(path string) (float64, error) {
	cmd := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(out))
	return strconv.ParseFloat(s, 64)
}
