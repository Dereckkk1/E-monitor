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
	Repo        *catalog.Detections
	Storage     *storage.Client
	SummaryRepo *catalog.DailySummaryRepo
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

// EvidenceURL returns a short-lived presigned GET URL for the detection's
// evidence clip. Used by the internal frontend so <audio> / <a download>
// tags — which cannot send the Authorization header — can fetch the clip
// directly from object storage. The TTL is short (5 min) because the URL
// inherits no auth once issued; the frontend should re-fetch it close to
// expiry. Returns 404 with the same semantics as Evidence (detection not
// found, or clip not yet available).
func (h *DetectionsHandler) EvidenceURL(w http.ResponseWriter, r *http.Request) {
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
	const ttl = 5 * time.Minute
	url, expiresAt, err := h.Storage.PresignGet(r.Context(), *det.EvidenceKey, ttl)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{
		"url":        url,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
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

func (h *DetectionsHandler) DailySummary(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		http.Error(w, "from must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		http.Error(w, "to must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	rows, err := h.SummaryRepo.ListByCampaign(r.Context(), campaignID, from, to)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []catalog.DailySummaryRow{}
	}
	writeJSON(w, http.StatusOK, rows)
}
