package handlers

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"radiocheck/internal/catalog"
	"radiocheck/internal/storage"
)

type DetectionsHandler struct {
	Repo    *catalog.Detections
	Storage *storage.Client
}

func (h *DetectionsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := catalog.ListFilter{}
	if v := q.Get("campaign_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			http.Error(w, "invalid campaign_id", 400)
			return
		}
		f.CampaignID = &id
	}
	if v := q.Get("station_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			http.Error(w, "invalid station_id", 400)
			return
		}
		f.StationID = &id
	}
	if v := q.Get("start_date"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid start_date (use RFC3339)", 400)
			return
		}
		f.StartDate = &t
	}
	if v := q.Get("end_date"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid end_date (use RFC3339)", 400)
			return
		}
		f.EndDate = &t
	}
	if v := q.Get("limit"); v != "" {
		n, _ := strconv.Atoi(v)
		f.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, _ := strconv.Atoi(v)
		f.Offset = n
	}
	items, err := h.Repo.List(r.Context(), f)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"data": items})
}

func (h *DetectionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	det, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	writeJSON(w, 200, det)
}

func (h *DetectionsHandler) Evidence(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	det, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	if det.EvidenceStatus != "available" || det.EvidenceKey == nil {
		http.Error(w, "evidence not available", 404)
		return
	}
	body, ct, _, err := h.Storage.Get(r.Context(), *det.EvidenceKey)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	if ct == "" {
		ct = "audio/mp4"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", "inline; filename=\""+id.String()+".m4a\"")
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, id.String()+".m4a", time.Time{}, bytes.NewReader(data))
}
