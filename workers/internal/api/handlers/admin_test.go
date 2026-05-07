package handlers

import (
	"bytes"
	"context"
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

// fakeTiering implements TieringRunner for tests.
type fakeTiering struct {
	calls atomic.Int32
	err   error
}

func (f *fakeTiering) Run(ctx context.Context) error {
	f.calls.Add(1)
	return f.err
}

// fakeThreshold implements ThresholdRefresher for tests.
type fakeThreshold struct {
	called atomic.Int32
	err    error
}

func (f *fakeThreshold) RefreshThreshold(uuid.UUID) error {
	f.called.Add(1)
	return f.err
}

// fakeCalibration implements CalibrationRunner for tests.
type fakeCalibration struct {
	full           atomic.Int32
	station        atomic.Int32
	count          int
	fullErr        error
	stationErr     error
	lastStationArg uuid.UUID
}

func (f *fakeCalibration) RunOnce(ctx context.Context) (int, error) {
	f.full.Add(1)
	return f.count, f.fullErr
}

func (f *fakeCalibration) RunOnceForStation(ctx context.Context, id uuid.UUID) error {
	f.station.Add(1)
	f.lastStationArg = id
	return f.stationErr
}

func TestAdmin_RunTiering_NoRunnerReturns503(t *testing.T) {
	h := &AdminHandler{}
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/admin/tiering/run", nil)
	rec := httptest.NewRecorder()
	h.RunTiering(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestAdmin_RunTiering_OkPath(t *testing.T) {
	tier := &fakeTiering{}
	h := &AdminHandler{Tiering: tier}
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/admin/tiering/run", nil)
	rec := httptest.NewRecorder()
	h.RunTiering(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if tier.calls.Load() != 1 {
		t.Fatalf("expected exactly one Run() call, got %d", tier.calls.Load())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status field = %v, want ok", body["status"])
	}
	if _, ok := body["duration_ms"]; !ok {
		t.Fatalf("missing duration_ms field; body=%v", body)
	}
}

func TestAdmin_RunTiering_ErrorPath(t *testing.T) {
	tier := &fakeTiering{err: errors.New("S3 down")}
	h := &AdminHandler{Tiering: tier}
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/admin/tiering/run", nil)
	rec := httptest.NewRecorder()
	h.RunTiering(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "S3 down") {
		t.Fatalf("body should surface inner error: %s", rec.Body.String())
	}
}

func TestAdmin_RefreshThreshold_NoRefresher(t *testing.T) {
	h := &AdminHandler{}
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	rec := httptest.NewRecorder()
	h.RefreshThreshold(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestAdmin_RefreshThreshold_InvalidID(t *testing.T) {
	thr := &fakeThreshold{}
	h := &AdminHandler{Threshold: thr}

	r := chi.NewRouter()
	r.Post("/stations/{id}/threshold/refresh", h.RefreshThreshold)

	req := httptest.NewRequest(http.MethodPost, "/stations/not-a-uuid/threshold/refresh", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if thr.called.Load() != 0 {
		t.Fatalf("RefreshThreshold should not be called on invalid id")
	}
}

func TestAdmin_RefreshThreshold_NotFoundFromSupervisor(t *testing.T) {
	thr := &fakeThreshold{err: errors.New("no worker running")}
	h := &AdminHandler{Threshold: thr}

	r := chi.NewRouter()
	r.Post("/stations/{id}/threshold/refresh", h.RefreshThreshold)

	id := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/stations/"+id.String()+"/threshold/refresh", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdmin_RefreshThreshold_OK(t *testing.T) {
	thr := &fakeThreshold{}
	h := &AdminHandler{Threshold: thr}

	r := chi.NewRouter()
	r.Post("/stations/{id}/threshold/refresh", h.RefreshThreshold)

	id := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/stations/"+id.String()+"/threshold/refresh", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if thr.called.Load() != 1 {
		t.Fatalf("expected exactly one RefreshThreshold call, got %d", thr.called.Load())
	}
}

func TestAdmin_RunCalibration_NoRunner(t *testing.T) {
	h := &AdminHandler{}
	req := httptest.NewRequest(http.MethodPost, "/calibration/run", nil)
	rec := httptest.NewRecorder()
	h.RunCalibration(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestAdmin_RunCalibration_FullPassOK(t *testing.T) {
	cal := &fakeCalibration{count: 17}
	h := &AdminHandler{Calibration: cal}
	req := httptest.NewRequest(http.MethodPost, "/calibration/run", nil)
	rec := httptest.NewRecorder()
	h.RunCalibration(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cal.full.Load() != 1 {
		t.Fatalf("RunOnce calls = %d, want 1", cal.full.Load())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("status = %v", body["status"])
	}
	if got, _ := body["stations"].(float64); int(got) != 17 {
		t.Fatalf("stations = %v, want 17", body["stations"])
	}
}

func TestAdmin_RunCalibration_FullPassError(t *testing.T) {
	cal := &fakeCalibration{fullErr: errors.New("DB borked")}
	h := &AdminHandler{Calibration: cal}
	req := httptest.NewRequest(http.MethodPost, "/calibration/run", nil)
	rec := httptest.NewRecorder()
	h.RunCalibration(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestAdmin_RunCalibration_StationOK(t *testing.T) {
	cal := &fakeCalibration{}
	h := &AdminHandler{Calibration: cal}
	id := uuid.New()
	body, _ := json.Marshal(map[string]string{"station_id": id.String()})
	req := httptest.NewRequest(http.MethodPost, "/calibration/run", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	rec := httptest.NewRecorder()
	h.RunCalibration(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cal.station.Load() != 1 {
		t.Fatalf("RunOnceForStation calls = %d, want 1", cal.station.Load())
	}
	if cal.lastStationArg != id {
		t.Fatalf("station id arg = %v, want %v", cal.lastStationArg, id)
	}
	if cal.full.Load() != 0 {
		t.Fatalf("RunOnce should NOT have been called when station_id is set")
	}
}

func TestAdmin_RunCalibration_StationInvalidUUID(t *testing.T) {
	cal := &fakeCalibration{}
	h := &AdminHandler{Calibration: cal}
	body := []byte(`{"station_id":"not-a-uuid"}`)
	req := httptest.NewRequest(http.MethodPost, "/calibration/run", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	rec := httptest.NewRecorder()
	h.RunCalibration(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if cal.station.Load() != 0 || cal.full.Load() != 0 {
		t.Fatalf("calibration must not be invoked on bad station_id")
	}
}

func TestAdmin_RunCalibration_BadJSON(t *testing.T) {
	cal := &fakeCalibration{}
	h := &AdminHandler{Calibration: cal}
	body := []byte(`{not json`)
	req := httptest.NewRequest(http.MethodPost, "/calibration/run", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	rec := httptest.NewRecorder()
	h.RunCalibration(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAdmin_RunCalibration_StationError(t *testing.T) {
	cal := &fakeCalibration{stationErr: errors.New("redis fault")}
	h := &AdminHandler{Calibration: cal}
	id := uuid.New()
	body, _ := json.Marshal(map[string]string{"station_id": id.String()})
	req := httptest.NewRequest(http.MethodPost, "/calibration/run", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	rec := httptest.NewRecorder()
	h.RunCalibration(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
}
