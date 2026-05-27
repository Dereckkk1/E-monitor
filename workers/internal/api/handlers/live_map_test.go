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
	gotScope    *uuid.UUID
	scopeCalled bool
	result      catalog.LiveMapResult
}

func (f *fakeLiveMapRepo) Get(ctx context.Context, scope *uuid.UUID) (catalog.LiveMapResult, error) {
	f.scopeCalled = true
	f.gotScope = scope
	return f.result, nil
}

func TestLiveMapHandler_Admin_ScopeNil(t *testing.T) {
	fake := &fakeLiveMapRepo{}
	h := &LiveMapHandler{Repo: fake}
	req := httptest.NewRequest("GET", "/live-map", nil)
	req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{Role: "admin"}))
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !fake.scopeCalled {
		t.Fatal("repo.Get não foi chamado")
	}
	if fake.gotScope != nil {
		t.Errorf("scope = %v, want nil para admin", fake.gotScope)
	}
}

func TestLiveMapHandler_Viewer_ScopeIsClientID(t *testing.T) {
	cid := uuid.New()
	fake := &fakeLiveMapRepo{}
	h := &LiveMapHandler{Repo: fake}
	req := httptest.NewRequest("GET", "/live-map", nil)
	req = req.WithContext(auth.ContextWithClaims(req.Context(),
		&auth.Claims{Role: "viewer", ClientID: &cid}))
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if fake.gotScope == nil || *fake.gotScope != cid {
		t.Errorf("scope = %v, want %v", fake.gotScope, cid)
	}
}

func TestLiveMapHandler_JSONShape(t *testing.T) {
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
	req := httptest.NewRequest("GET", "/live-map", nil)
	req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{Role: "admin"}))
	rr := httptest.NewRecorder()
	h.Get(rr, req)

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
