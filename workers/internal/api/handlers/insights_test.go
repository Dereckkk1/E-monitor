package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"radiocheck/internal/catalog"
)

type fakeInsightsRepo struct {
	out  *catalog.InsightsPayload
	err  error
	got  catalog.InsightsParams
	hits int
}

func (f *fakeInsightsRepo) Compute(_ context.Context, p catalog.InsightsParams) (*catalog.InsightsPayload, error) {
	f.got = p
	f.hits++
	return f.out, f.err
}

func TestInsights_Get_RequiresClientID(t *testing.T) {
	h := &InsightsHandler{Repo: &fakeInsightsRepo{}}
	req := httptest.NewRequest("GET", "/insights?campaigns="+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestInsights_Get_RequiresCampaigns(t *testing.T) {
	h := &InsightsHandler{Repo: &fakeInsightsRepo{}}
	req := httptest.NewRequest("GET", "/insights?client_id="+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestInsights_Get_RejectsInvalidUUID(t *testing.T) {
	h := &InsightsHandler{Repo: &fakeInsightsRepo{}}
	req := httptest.NewRequest("GET", "/insights?client_id=not-a-uuid&campaigns="+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestInsights_Get_HappyPath(t *testing.T) {
	want := &catalog.InsightsPayload{
		Period: catalog.PeriodSpec{From: "2026-06-01", To: "2026-06-30", Granularity: "day"},
		KPIs:   catalog.InsightsKPIs{Impactos: 12345},
	}
	repo := &fakeInsightsRepo{out: want}
	h := &InsightsHandler{Repo: repo}
	url := "/insights?client_id=" + uuid.NewString() + "&campaigns=" + uuid.NewString()
	req := httptest.NewRequest("GET", url, nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	var got catalog.InsightsPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v", err)
	}
	if got.KPIs.Impactos != 12345 {
		t.Fatalf("impactos = %d", got.KPIs.Impactos)
	}
	if repo.hits != 1 {
		t.Fatalf("hits = %d", repo.hits)
	}
}

func TestInsights_Get_DefaultPeriodIsCurrentMonth(t *testing.T) {
	repo := &fakeInsightsRepo{out: &catalog.InsightsPayload{}}
	h := &InsightsHandler{Repo: repo}
	url := "/insights?client_id=" + uuid.NewString() + "&campaigns=" + uuid.NewString()
	req := httptest.NewRequest("GET", url, nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	now := time.Now().UTC()
	wantFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if !repo.got.From.Equal(wantFrom) {
		t.Fatalf("from = %v, want %v", repo.got.From, wantFrom)
	}
}

// O handler deve injetar Today = "hoje" no fuso America/Sao_Paulo (date-only),
// pra que aggregateInvestment/computeCPM não contem dias futuros no fill-ratio
// consolidado. Ver docs/features/insights-dashboard.md.
func TestInsights_Get_SetsTodayInSaoPaulo(t *testing.T) {
	repo := &fakeInsightsRepo{out: &catalog.InsightsPayload{}}
	h := &InsightsHandler{Repo: repo}
	url := "/insights?client_id=" + uuid.NewString() + "&campaigns=" + uuid.NewString()
	req := httptest.NewRequest("GET", url, nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	spNow := time.Now().In(loc)
	wantToday := time.Date(spNow.Year(), spNow.Month(), spNow.Day(), 0, 0, 0, 0, time.UTC)
	if !repo.got.Today.Equal(wantToday) {
		t.Fatalf("today = %v, want %v (date-only em America/Sao_Paulo)", repo.got.Today, wantToday)
	}
}

func TestInsights_Get_CrossClientErrorReturns403(t *testing.T) {
	repo := &fakeInsightsRepo{err: errors.New("insights: 2 campaigns requested, 1 found for client (cross-client or invalid id)")}
	h := &InsightsHandler{Repo: repo}
	url := "/insights?client_id=" + uuid.NewString() + "&campaigns=" + uuid.NewString()
	req := httptest.NewRequest("GET", url, nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}
