package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestDistributionRulesHandler_Create_BadCampaignID(t *testing.T) {
	h := &DistributionRulesHandler{}
	r := chi.NewRouter()
	r.Post("/campaigns/{campaignID}/distribution-rules", h.Create)
	req := httptest.NewRequest("POST", "/campaigns/not-uuid/distribution-rules", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rr.Code)
	}
}

func TestDistributionRulesHandler_Create_BadJSON(t *testing.T) {
	h := &DistributionRulesHandler{}
	r := chi.NewRouter()
	r.Post("/campaigns/{campaignID}/distribution-rules", h.Create)
	req := httptest.NewRequest("POST",
		"/campaigns/"+uuid.New().String()+"/distribution-rules",
		strings.NewReader(`not-json`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rr.Code)
	}
}

func TestDistributionRulesHandler_Create_BadDate(t *testing.T) {
	h := &DistributionRulesHandler{}
	r := chi.NewRouter()
	r.Post("/campaigns/{campaignID}/distribution-rules", h.Create)
	// Valid JSON but invalid date format
	body := `{"type_id":"` + uuid.New().String() + `","station_ids":[],"start_date":"not-a-date","end_date":"2026-06-30","weekday_mask":62,"time_start":"08:00","time_end":"10:00","plays_per_day":3}`
	req := httptest.NewRequest("POST",
		"/campaigns/"+uuid.New().String()+"/distribution-rules",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rr.Code)
	}
}

func TestDistributionRulesHandler_ListByCampaign_BadID(t *testing.T) {
	h := &DistributionRulesHandler{}
	r := chi.NewRouter()
	r.Get("/campaigns/{campaignID}/distribution-rules", h.ListByCampaign)
	req := httptest.NewRequest("GET", "/campaigns/not-uuid/distribution-rules", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rr.Code)
	}
}

func TestDistributionRulesHandler_Update_BadIDs(t *testing.T) {
	h := &DistributionRulesHandler{}
	r := chi.NewRouter()
	r.Put("/campaigns/{campaignID}/distribution-rules/{ruleID}", h.Update)
	req := httptest.NewRequest("PUT",
		"/campaigns/not-uuid/distribution-rules/"+uuid.New().String(),
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rr.Code)
	}
}

func TestDistributionRulesHandler_Delete_BadIDs(t *testing.T) {
	h := &DistributionRulesHandler{}
	r := chi.NewRouter()
	r.Delete("/campaigns/{campaignID}/distribution-rules/{ruleID}", h.Delete)
	req := httptest.NewRequest("DELETE",
		"/campaigns/not-uuid/distribution-rules/"+uuid.New().String(), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rr.Code)
	}
}
