package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestDistributionOverridesHandler_Upsert_BadCampaignID(t *testing.T) {
	h := &DistributionOverridesHandler{}
	r := chi.NewRouter()
	r.Put("/campaigns/{campaignID}/distribution-overrides", h.Upsert)
	req := httptest.NewRequest("PUT", "/campaigns/not-uuid/distribution-overrides",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestDistributionOverridesHandler_Upsert_BadDate(t *testing.T) {
	h := &DistributionOverridesHandler{}
	r := chi.NewRouter()
	r.Put("/campaigns/{campaignID}/distribution-overrides", h.Upsert)
	body := `{"material_id":"` + uuid.New().String() + `","station_id":"` + uuid.New().String() + `","for_date":"not-a-date","plays_expected":3}`
	req := httptest.NewRequest("PUT",
		"/campaigns/"+uuid.New().String()+"/distribution-overrides",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestDistributionOverridesHandler_Delete_BadCampaignID(t *testing.T) {
	h := &DistributionOverridesHandler{}
	r := chi.NewRouter()
	r.Delete("/campaigns/{campaignID}/distribution-overrides", h.Delete)
	req := httptest.NewRequest("DELETE", "/campaigns/not-uuid/distribution-overrides",
		strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestDistributionOverridesHandler_List_BadFromDate(t *testing.T) {
	h := &DistributionOverridesHandler{}
	r := chi.NewRouter()
	r.Get("/campaigns/{campaignID}/distribution-overrides", h.ListByDateRange)
	req := httptest.NewRequest("GET",
		"/campaigns/"+uuid.New().String()+"/distribution-overrides?from=bad&to=2026-06-30", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
