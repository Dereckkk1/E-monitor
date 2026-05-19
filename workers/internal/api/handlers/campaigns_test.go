package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// fakeSupervisor implements CampaignSupervisor for tests.
type fakeSupervisor struct {
	startCalls          atomic.Int32
	pauseCalls          atomic.Int32
	reload              atomic.Int32
	stopCalls           atomic.Int32
	updateStationsCalls atomic.Int32
	startErr            error
	pauseErr            error
	reloadErr           error
	updateStationsErr   error
	lastCancelArg       uuid.UUID
}

func (f *fakeSupervisor) Start(id uuid.UUID) error {
	f.startCalls.Add(1)
	return f.startErr
}
func (f *fakeSupervisor) Pause(id uuid.UUID) error {
	f.pauseCalls.Add(1)
	return f.pauseErr
}
func (f *fakeSupervisor) Reload(id uuid.UUID) error {
	f.reload.Add(1)
	return f.reloadErr
}
func (f *fakeSupervisor) UpdateStations(id uuid.UUID, stations []uuid.UUID) error {
	f.updateStationsCalls.Add(1)
	return f.updateStationsErr
}
func (f *fakeSupervisor) StopWorkersForCampaign(id uuid.UUID) {
	f.stopCalls.Add(1)
	f.lastCancelArg = id
}

// TestCampaigns_Pause_ReturnsGone verifies the deprecated /pause endpoint
// loud-fails with 410 (§18.2.1 — paused was collapsed into cancelada).
func TestCampaigns_Pause_ReturnsGone(t *testing.T) {
	h := &CampaignsHandler{}
	req := httptest.NewRequest(http.MethodPost, "/campaigns/anything/pause", nil)
	rec := httptest.NewRecorder()
	h.Pause(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] != "endpoint deprecated" {
		t.Fatalf("error field = %v", body["error"])
	}
	if !strings.Contains(body["message"].(string), "/cancel") {
		t.Fatalf("message should hint at /cancel: %v", body["message"])
	}
}

// TestCampaigns_Get_InvalidID returns 400 before touching the repo.
func TestCampaigns_Get_InvalidID(t *testing.T) {
	h := &CampaignsHandler{}
	r := chi.NewRouter()
	r.Get("/campaigns/{id}", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/campaigns/not-a-uuid", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestCampaigns_Start_NoSupervisorReturns503 verifies the 503 path when the
// supervisor wasn't wired into the handler.
func TestCampaigns_Start_NoSupervisorReturns503(t *testing.T) {
	h := &CampaignsHandler{}
	r := chi.NewRouter()
	r.Post("/campaigns/{id}/start", h.Start)

	id := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/campaigns/"+id.String()+"/start", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// TestCampaigns_Start_OK verifies that a valid id + supervisor returns 204
// and the supervisor's Start is called.
func TestCampaigns_Start_OK(t *testing.T) {
	sup := &fakeSupervisor{}
	h := &CampaignsHandler{Supervisor: sup}
	r := chi.NewRouter()
	r.Post("/campaigns/{id}/start", h.Start)

	id := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/campaigns/"+id.String()+"/start", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if sup.startCalls.Load() != 1 {
		t.Fatalf("Start calls = %d, want 1", sup.startCalls.Load())
	}
}

// TestCampaigns_Start_InvalidID rejects bad uuids before calling the supervisor.
func TestCampaigns_Start_InvalidID(t *testing.T) {
	sup := &fakeSupervisor{}
	h := &CampaignsHandler{Supervisor: sup}
	r := chi.NewRouter()
	r.Post("/campaigns/{id}/start", h.Start)

	req := httptest.NewRequest(http.MethodPost, "/campaigns/garbage/start", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if sup.startCalls.Load() != 0 {
		t.Fatalf("supervisor must not be called on bad id")
	}
}

// TestCampaigns_Start_SupervisorError surfaces 500 when supervisor errors.
func TestCampaigns_Start_SupervisorError(t *testing.T) {
	sup := &fakeSupervisor{startErr: errors.New("boom")}
	h := &CampaignsHandler{Supervisor: sup}
	r := chi.NewRouter()
	r.Post("/campaigns/{id}/start", h.Start)

	id := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/campaigns/"+id.String()+"/start", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// TestCampaigns_Cancel_InvalidID short-circuits before touching the repo.
func TestCampaigns_Cancel_InvalidID(t *testing.T) {
	h := &CampaignsHandler{}
	r := chi.NewRouter()
	r.Post("/campaigns/{id}/cancel", h.Cancel)

	req := httptest.NewRequest(http.MethodPost, "/campaigns/not-uuid/cancel", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestCampaigns_Delete_InvalidID short-circuits with 400.
func TestCampaigns_Delete_InvalidID(t *testing.T) {
	h := &CampaignsHandler{}
	r := chi.NewRouter()
	r.Delete("/campaigns/{id}", h.Delete)

	req := httptest.NewRequest(http.MethodDelete, "/campaigns/not-uuid", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestCampaigns_UpdateStations_InvalidID short-circuits before parsing body.
func TestCampaigns_UpdateStations_InvalidID(t *testing.T) {
	h := &CampaignsHandler{}
	r := chi.NewRouter()
	r.Put("/campaigns/{id}/stations", h.UpdateStations)

	req := httptest.NewRequest(http.MethodPut, "/campaigns/garbage/stations",
		strings.NewReader(`{"target_stations":[]}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestCampaigns_UpdateStations_DelegatesToSupervisor verifies that, with a
// supervisor wired in, the handler delegates to Supervisor.UpdateStations and
// does NOT bounce through Pause+Start (the legacy dance that briefly flipped
// the campaign through 'cancelada' — see incident 2026-05-08 / docs/worker-
// commercial-reconciler.md).
func TestCampaigns_UpdateStations_DelegatesToSupervisor(t *testing.T) {
	sup := &fakeSupervisor{}
	h := &CampaignsHandler{Supervisor: sup}
	r := chi.NewRouter()
	r.Put("/campaigns/{id}/stations", h.UpdateStations)

	id := uuid.New()
	body := `{"target_stations":["` + uuid.New().String() + `"]}`
	req := httptest.NewRequest(http.MethodPut, "/campaigns/"+id.String()+"/stations",
		strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	if got := sup.updateStationsCalls.Load(); got != 1 {
		t.Errorf("UpdateStations calls = %d, want 1", got)
	}
	if got := sup.pauseCalls.Load(); got != 0 {
		t.Errorf("Pause calls = %d, want 0 (Pause+Start dance must be gone)", got)
	}
	if got := sup.startCalls.Load(); got != 0 {
		t.Errorf("Start calls = %d, want 0 (Pause+Start dance must be gone)", got)
	}
}

// TestCampaigns_UpdateStations_NotFoundFromSupervisor maps the supervisor's
// "campaign not found" sentinel to a 404 response.
func TestCampaigns_UpdateStations_NotFoundFromSupervisor(t *testing.T) {
	sup := &fakeSupervisor{updateStationsErr: errors.New("supervisor.UpdateStations: campaign not found: x")}
	h := &CampaignsHandler{Supervisor: sup}
	r := chi.NewRouter()
	r.Put("/campaigns/{id}/stations", h.UpdateStations)

	req := httptest.NewRequest(http.MethodPut, "/campaigns/"+uuid.New().String()+"/stations",
		strings.NewReader(`{"target_stations":[]}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 body=%s", rec.Code, rec.Body.String())
	}
}

// TestCampaigns_List_BadStatus rejects unknown filter values without touching
// the repo.
func TestCampaigns_List_BadStatus(t *testing.T) {
	h := &CampaignsHandler{}
	r := chi.NewRouter()
	r.Get("/campaigns", h.List)

	req := httptest.NewRequest(http.MethodGet, "/campaigns?status=programada,bogus", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bogus") {
		t.Fatalf("error should name the bad value: %s", rec.Body.String())
	}
}

// TestValidStatuses pins the four lifecycle states accepted by the filter.
func TestValidStatuses(t *testing.T) {
	for _, s := range []string{"programada", "ativa", "concluida", "cancelada"} {
		if _, ok := validStatuses[s]; !ok {
			t.Errorf("expected %s in validStatuses", s)
		}
	}
	if _, ok := validStatuses["paused"]; ok {
		t.Errorf("'paused' was collapsed into 'cancelada' — must not appear in validStatuses")
	}
}

// TestCampaigns_Create_BadJSON rejects malformed body with 400.
func TestCampaigns_Create_BadJSON(t *testing.T) {
	h := &CampaignsHandler{}
	req := httptest.NewRequest(http.MethodPost, "/campaigns", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestCampaigns_Create_MissingRequired rejects bodies without name or
// client_id with 400.
func TestCampaigns_Create_MissingRequired(t *testing.T) {
	h := &CampaignsHandler{}
	cases := []string{
		`{}`,
		`{"name":""}`,
		`{"name":"only-name"}`,
		`{"client_id":"00000000-0000-0000-0000-000000000000"}`,
	}
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPost, "/campaigns", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.Create(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s: status = %d, want 400", body, rec.Code)
		}
	}
}
