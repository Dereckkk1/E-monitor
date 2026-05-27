package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

type fakeLiveMapRepo struct {
	gotCampaign uuid.UUID
	gotScope    *uuid.UUID
	called      bool
	result      catalog.LiveMapResult
	err         error
}

func (f *fakeLiveMapRepo) Get(ctx context.Context, campaignID uuid.UUID, scope *uuid.UUID) (catalog.LiveMapResult, error) {
	f.called = true
	f.gotCampaign = campaignID
	f.gotScope = scope
	return f.result, f.err
}

func newLiveMapReq(query string, claims *auth.Claims) *http.Request {
	req := httptest.NewRequest("GET", "/live-map"+query, nil)
	if claims != nil {
		req = req.WithContext(auth.ContextWithClaims(req.Context(), claims))
	}
	return req
}

func TestLiveMapHandler_MissingCampaignID_400(t *testing.T) {
	h := &LiveMapHandler{Repo: &fakeLiveMapRepo{}}
	rr := httptest.NewRecorder()
	h.Get(rr, newLiveMapReq("", &auth.Claims{Role: "admin"}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestLiveMapHandler_InvalidCampaignID_400(t *testing.T) {
	h := &LiveMapHandler{Repo: &fakeLiveMapRepo{}}
	rr := httptest.NewRecorder()
	h.Get(rr, newLiveMapReq("?campaign_id=not-uuid", &auth.Claims{Role: "admin"}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestLiveMapHandler_Admin_ScopeNil(t *testing.T) {
	camp := uuid.New()
	fake := &fakeLiveMapRepo{}
	h := &LiveMapHandler{Repo: fake}
	rr := httptest.NewRecorder()
	h.Get(rr, newLiveMapReq("?campaign_id="+camp.String(), &auth.Claims{Role: "admin"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !fake.called {
		t.Fatal("repo.Get não foi chamado")
	}
	if fake.gotScope != nil {
		t.Errorf("scope = %v, want nil para admin", fake.gotScope)
	}
	if fake.gotCampaign != camp {
		t.Errorf("campaignID = %v, want %v", fake.gotCampaign, camp)
	}
}

func TestLiveMapHandler_Viewer_ScopeIsClientID(t *testing.T) {
	camp := uuid.New()
	cid := uuid.New()
	fake := &fakeLiveMapRepo{}
	h := &LiveMapHandler{Repo: fake}
	rr := httptest.NewRecorder()
	h.Get(rr, newLiveMapReq("?campaign_id="+camp.String(),
		&auth.Claims{Role: "viewer", ClientID: &cid}))
	if fake.gotScope == nil || *fake.gotScope != cid {
		t.Errorf("scope = %v, want %v", fake.gotScope, cid)
	}
}

func TestLiveMapHandler_CampaignNotFound_404(t *testing.T) {
	camp := uuid.New()
	fake := &fakeLiveMapRepo{err: catalog.ErrCampaignNotFound}
	h := &LiveMapHandler{Repo: fake}
	rr := httptest.NewRecorder()
	h.Get(rr, newLiveMapReq("?campaign_id="+camp.String(), &auth.Claims{Role: "admin"}))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestLiveMapHandler_JSONShape(t *testing.T) {
	camp := uuid.New()
	freq := 102.5
	fake := &fakeLiveMapRepo{result: catalog.LiveMapResult{
		Stations: []catalog.LiveStation{{
			ID: uuid.New(), Name: "40 Graus", Band: "FM",
			FrequencyMHz: &freq, Latitude: -20.8, Longitude: -49.3,
		}},
		RecentDetections: []catalog.LiveDetection{{
			ID: uuid.New(), StationName: "40 Graus", CommercialName: "PILECCO",
		}},
	}}
	h := &LiveMapHandler{Repo: fake}
	rr := httptest.NewRecorder()
	h.Get(rr, newLiveMapReq("?campaign_id="+camp.String(), &auth.Claims{Role: "admin"}))

	var body struct {
		Stations         []map[string]any `json:"stations"`
		RecentDetections []map[string]any `json:"recent_detections"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Stations) != 1 {
		t.Fatalf("stations = %d, want 1", len(body.Stations))
	}
	if len(body.RecentDetections) != 1 {
		t.Fatalf("recent_detections = %d, want 1", len(body.RecentDetections))
	}
}
