package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestDetectionsHandler_DailySummary_BadCampaignID(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/campaigns/{campaignID}/daily-summary", h.DailySummary)

	req := httptest.NewRequest("GET",
		"/campaigns/not-uuid/daily-summary?from=2026-06-01&to=2026-06-30", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestDetectionsHandler_DailySummary_BadFromDate(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/campaigns/{campaignID}/daily-summary", h.DailySummary)
	req := httptest.NewRequest("GET",
		"/campaigns/"+uuid.New().String()+"/daily-summary?from=bad&to=2026-06-30", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for bad from", rr.Code)
	}
}

func TestDetectionsHandler_DailySummary_BadToDate(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/campaigns/{campaignID}/daily-summary", h.DailySummary)
	req := httptest.NewRequest("GET",
		"/campaigns/"+uuid.New().String()+"/daily-summary?from=2026-06-01&to=bad", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for bad to", rr.Code)
	}
}

// ── Paginated list (airtime report) validation ──────────────────────

func TestDetectionsHandler_List_Paged_BadPage(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/detections", h.List)

	req := httptest.NewRequest("GET", "/detections?page=0", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for page=0", rr.Code)
	}
}

func TestDetectionsHandler_List_Paged_BadPageSize(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/detections", h.List)

	req := httptest.NewRequest("GET", "/detections?page=1&page_size=999", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for page_size=999", rr.Code)
	}
}

func TestDetectionsHandler_List_Paged_BadFromDate(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/detections", h.List)

	req := httptest.NewRequest("GET", "/detections?page=1&from=not-rfc3339", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for bad from", rr.Code)
	}
}

func TestDetectionsHandler_List_Paged_BadCampaignID(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/detections", h.List)

	req := httptest.NewRequest("GET", "/detections?page=1&campaign_id=not-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for bad campaign_id", rr.Code)
	}
}
