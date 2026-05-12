package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestCampaignMaterialsHandler_Link_BadCampaignID(t *testing.T) {
	h := &CampaignMaterialsHandler{}
	r := chi.NewRouter()
	r.Post("/campaigns/{campaignID}/materials", h.Link)
	req := httptest.NewRequest("POST", "/campaigns/not-uuid/materials",
		strings.NewReader(`{"material_id":"`+uuid.New().String()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestCampaignMaterialsHandler_Link_BadJSON(t *testing.T) {
	h := &CampaignMaterialsHandler{}
	r := chi.NewRouter()
	r.Post("/campaigns/{campaignID}/materials", h.Link)
	req := httptest.NewRequest("POST", "/campaigns/"+uuid.New().String()+"/materials",
		strings.NewReader(`not-json`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestCampaignMaterialsHandler_ListByCampaign_BadID(t *testing.T) {
	h := &CampaignMaterialsHandler{}
	r := chi.NewRouter()
	r.Get("/campaigns/{campaignID}/materials", h.ListByCampaign)
	req := httptest.NewRequest("GET", "/campaigns/not-uuid/materials", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestCampaignMaterialsHandler_UpdateStations_BadIDs(t *testing.T) {
	h := &CampaignMaterialsHandler{}
	r := chi.NewRouter()
	r.Put("/campaigns/{campaignID}/materials/{materialID}/stations", h.UpdateStations)
	req := httptest.NewRequest("PUT",
		"/campaigns/not-uuid/materials/"+uuid.New().String()+"/stations",
		strings.NewReader(`{"target_stations":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestCampaignMaterialsHandler_Unlink_BadIDs(t *testing.T) {
	h := &CampaignMaterialsHandler{}
	r := chi.NewRouter()
	r.Delete("/campaigns/{campaignID}/materials/{materialID}", h.Unlink)
	req := httptest.NewRequest("DELETE",
		"/campaigns/not-uuid/materials/"+uuid.New().String(), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
