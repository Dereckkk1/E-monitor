package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"radiocheck/internal/catalog"
	"radiocheck/internal/supervisor"
)

// fakeWorkers satisfaz workerLister (health.go) — devolve um snapshot com as
// estações dadas todas ativas.
type fakeWorkers struct{ active []string }

func (f *fakeWorkers) WorkerStatuses() []supervisor.WorkerStatus {
	out := make([]supervisor.WorkerStatus, 0, len(f.active))
	for _, id := range f.active {
		out = append(out, supervisor.WorkerStatus{StationID: id, Active: true})
	}
	return out
}

type fakeMgmtRepo struct {
	got    catalog.ManagementParams
	called bool
	result catalog.ManagementResult
	err    error
}

func (f *fakeMgmtRepo) Get(ctx context.Context, p catalog.ManagementParams) (catalog.ManagementResult, error) {
	f.called = true
	f.got = p
	return f.result, f.err
}

func newMgmtReq(query string) *http.Request {
	return httptest.NewRequest("GET", "/management-overview"+query, nil)
}

func TestMgmtHandler_Defaults(t *testing.T) {
	fake := &fakeMgmtRepo{}
	h := &ManagementOverviewHandler{Repo: fake}
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq(""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !fake.called {
		t.Fatal("repo.Get não foi chamado")
	}
	if fake.got.ClientID != nil {
		t.Errorf("ClientID = %v, want nil", fake.got.ClientID)
	}
	if len(fake.got.CampaignIDs) != 0 {
		t.Errorf("CampaignIDs = %v, want vazio", fake.got.CampaignIDs)
	}
	if fake.got.Status != "" {
		t.Errorf("Status = %q, want vazio", fake.got.Status)
	}
	now := time.Now().UTC()
	if fake.got.From.Year() != now.Year() || fake.got.From.Month() != time.January || fake.got.From.Day() != 1 {
		t.Errorf("From = %v, want 01/01 do ano corrente", fake.got.From)
	}
}

func TestMgmtHandler_ParsesFilters(t *testing.T) {
	fake := &fakeMgmtRepo{}
	h := &ManagementOverviewHandler{Repo: fake}
	cid := uuid.New()
	camp := uuid.New()
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq("?client_id="+cid.String()+"&campaigns="+camp.String()+"&status=ativa&from=2026-02-01&to=2026-03-01"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if fake.got.ClientID == nil || *fake.got.ClientID != cid {
		t.Errorf("ClientID = %v, want %v", fake.got.ClientID, cid)
	}
	if len(fake.got.CampaignIDs) != 1 || fake.got.CampaignIDs[0] != camp {
		t.Errorf("CampaignIDs = %v, want [%v]", fake.got.CampaignIDs, camp)
	}
	if fake.got.Status != "ativa" {
		t.Errorf("Status = %q, want ativa", fake.got.Status)
	}
}

func TestMgmtHandler_InvalidClientID_400(t *testing.T) {
	h := &ManagementOverviewHandler{Repo: &fakeMgmtRepo{}}
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq("?client_id=not-uuid"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestMgmtHandler_InvalidStatus_400(t *testing.T) {
	h := &ManagementOverviewHandler{Repo: &fakeMgmtRepo{}}
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq("?status=bogus"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestMgmtHandler_ToBeforeFrom_400(t *testing.T) {
	h := &ManagementOverviewHandler{Repo: &fakeMgmtRepo{}}
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq("?from=2026-03-01&to=2026-02-01"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestMgmtHandler_RepoError_500(t *testing.T) {
	h := &ManagementOverviewHandler{Repo: &fakeMgmtRepo{err: context.DeadlineExceeded}}
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq(""))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}

func TestMgmtHandler_StationsLive_FromSupervisor(t *testing.T) {
	s1, s2, s3 := uuid.New(), uuid.New(), uuid.New()
	fake := &fakeMgmtRepo{result: catalog.ManagementResult{
		MonitoredStationIDs: []uuid.UUID{s1, s2, s3},
	}}
	// s1 e s3 ativos + um worker de estação fora do recorte (ignorado).
	workers := &fakeWorkers{active: []string{s1.String(), s3.String(), uuid.NewString()}}
	h := &ManagementOverviewHandler{Repo: fake, Workers: workers}
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq(""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body struct {
		KPIs map[string]any `json:"kpis"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.KPIs["stations_live"].(float64) != 2 {
		t.Errorf("stations_live = %v, want 2 (s1+s3)", body.KPIs["stations_live"])
	}
}

func TestMgmtHandler_JSONShape(t *testing.T) {
	fake := &fakeMgmtRepo{result: catalog.ManagementResult{
		KPIs:             catalog.ManagementKPIs{StationsMonitored: 3, StationsLive: 2},
		Stations:         []catalog.LiveStation{{ID: uuid.New(), Name: "40 Graus", Band: "FM"}},
		RecentDetections: []catalog.LiveDetection{{ID: uuid.New(), StationName: "40 Graus", CommercialName: "PILECCO"}},
	}}
	h := &ManagementOverviewHandler{Repo: fake}
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq(""))

	var body struct {
		KPIs             map[string]any   `json:"kpis"`
		Stations         []map[string]any `json:"stations"`
		RecentDetections []map[string]any `json:"recent_detections"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.KPIs["stations_monitored"].(float64) != 3 {
		t.Errorf("stations_monitored = %v, want 3", body.KPIs["stations_monitored"])
	}
	if len(body.Stations) != 1 || len(body.RecentDetections) != 1 {
		t.Fatalf("stations=%d recent=%d, want 1/1", len(body.Stations), len(body.RecentDetections))
	}
}
