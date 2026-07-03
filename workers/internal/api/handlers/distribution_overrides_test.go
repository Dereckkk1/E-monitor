package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/catalog"
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
	body := `{"type_id":"` + uuid.New().String() + `","station_id":"` + uuid.New().String() + `","for_date":"not-a-date","plays_expected":3}`
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

type mockOverrideStore struct{ upserted, deleted bool }

func (m *mockOverrideStore) Upsert(ctx context.Context, in catalog.UpsertOverrideInput) error {
	m.upserted = true
	return nil
}
func (m *mockOverrideStore) Delete(ctx context.Context, c, t, s uuid.UUID, d time.Time) error {
	m.deleted = true
	return nil
}
func (m *mockOverrideStore) ListByCampaignAndDateRange(ctx context.Context, campaignID uuid.UUID, from, to time.Time) ([]catalog.DistributionOverride, error) {
	return nil, nil
}

type recatCall struct {
	campaign, typ, station uuid.UUID
	date                   time.Time
}
type mockOverrideRecat struct{ ch chan recatCall }

func (m *mockOverrideRecat) RecategorizeForOverride(ctx context.Context, c, t, s uuid.UUID, d time.Time) error {
	m.ch <- recatCall{c, t, s, d}
	return nil
}

func TestDistributionOverridesHandler_Upsert_FiresRecat(t *testing.T) {
	campID := uuid.New()
	typeID := uuid.New()
	statID := uuid.New()
	recat := &mockOverrideRecat{ch: make(chan recatCall, 1)}
	h := &DistributionOverridesHandler{Repo: &mockOverrideStore{}, Recat: recat}

	r := chi.NewRouter()
	r.Put("/campaigns/{campaignID}/distribution-overrides", h.Upsert)
	body := `{"type_id":"` + typeID.String() + `","station_id":"` + statID.String() +
		`","for_date":"2026-07-01","plays_expected":13,"time_start":"00:00","time_end":"23:59"}`
	req := httptest.NewRequest("PUT", "/campaigns/"+campID.String()+"/distribution-overrides",
		strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	select {
	case c := <-recat.ch:
		if c.campaign != campID || c.typ != typeID || c.station != statID {
			t.Fatalf("recat chamado com escopo errado: %+v", c)
		}
		if c.date.Format("2006-01-02") != "2026-07-01" {
			t.Fatalf("recat chamado com data errada: %s", c.date.Format("2006-01-02"))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Upsert não disparou RecategorizeForOverride")
	}
}

func TestDistributionOverridesHandler_Delete_FiresRecat(t *testing.T) {
	campID := uuid.New()
	typeID := uuid.New()
	statID := uuid.New()
	recat := &mockOverrideRecat{ch: make(chan recatCall, 1)}
	h := &DistributionOverridesHandler{Repo: &mockOverrideStore{}, Recat: recat}

	r := chi.NewRouter()
	r.Delete("/campaigns/{campaignID}/distribution-overrides", h.Delete)
	body := `{"type_id":"` + typeID.String() + `","station_id":"` + statID.String() +
		`","for_date":"2026-07-01","plays_expected":13,"time_start":"00:00","time_end":"23:59"}`
	req := httptest.NewRequest("DELETE", "/campaigns/"+campID.String()+"/distribution-overrides",
		strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	select {
	case c := <-recat.ch:
		if c.campaign != campID || c.typ != typeID || c.station != statID {
			t.Fatalf("recat chamado com escopo errado: %+v", c)
		}
		if c.date.Format("2006-01-02") != "2026-07-01" {
			t.Fatalf("recat chamado com data errada: %s", c.date.Format("2006-01-02"))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Delete não disparou RecategorizeForOverride")
	}
}
