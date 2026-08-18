package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestDetectionsHandler_DailySummary_BadCampaignID(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/campaigns/{campaignID}/daily-summary", h.DailySummary)

	req := httptest.NewRequest("GET",
		"/campaigns/not-uuid/daily-summary?from=2026-06-01&to=2026-06-30", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestDetectionsHandler_DailySummary_BadFromDate(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/campaigns/{campaignID}/daily-summary", h.DailySummary)
	req := httptest.NewRequest("GET",
		"/campaigns/"+uuid.New().String()+"/daily-summary?from=bad&to=2026-06-30", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for bad from", rr.Code)
	}
}

func TestDetectionsHandler_DailySummary_BadToDate(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/campaigns/{campaignID}/daily-summary", h.DailySummary)
	req := httptest.NewRequest("GET",
		"/campaigns/"+uuid.New().String()+"/daily-summary?from=2026-06-01&to=bad", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for bad to", rr.Code)
	}
}

// ── Paginated list (airtime report) validation ──────────────────────

func TestDetectionsHandler_List_Paged_BadPage(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/detections", h.List)

	req := httptest.NewRequest("GET", "/detections?page=0", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for page=0", rr.Code)
	}
}

func TestDetectionsHandler_List_Paged_BadPageSize(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/detections", h.List)

	req := httptest.NewRequest("GET", "/detections?page=1&page_size=999", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for page_size=999", rr.Code)
	}
}

func TestDetectionsHandler_List_Paged_BadFromDate(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/detections", h.List)

	req := httptest.NewRequest("GET", "/detections?page=1&from=not-rfc3339", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for bad from", rr.Code)
	}
}

func TestDetectionsHandler_List_Paged_BadCampaignID(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/detections", h.List)

	req := httptest.NewRequest("GET", "/detections?page=1&campaign_id=not-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for bad campaign_id", rr.Code)
	}
}

// ── Aggregate-by-material validation ─────────────────────────────────

func TestDetectionsHandler_Aggregate_RequiresCampaignID(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/aggregate-by-material", h.AggregateByMaterial)

	req := httptest.NewRequest("GET", "/aggregate-by-material", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for missing campaign_id", rr.Code)
	}
}

func TestDetectionsHandler_Aggregate_BadCampaignID(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/aggregate-by-material", h.AggregateByMaterial)

	req := httptest.NewRequest("GET", "/aggregate-by-material?campaign_id=not-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for bad campaign_id", rr.Code)
	}
}

// ── Export CSV validation ───────────────────────────────────────────

func TestDetectionsHandler_Export_BadCampaignID(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/export", h.Export)

	req := httptest.NewRequest("GET", "/export?campaign_id=not-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for bad campaign_id", rr.Code)
	}
}

func TestDetectionsHandler_Export_BadFromDate(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/export", h.Export)

	req := httptest.NewRequest("GET", "/export?from=bad", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for bad from", rr.Code)
	}
}

func TestDetectionsHandler_Aggregate_BadFromDate(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/aggregate-by-material", h.AggregateByMaterial)

	req := httptest.NewRequest("GET",
		"/aggregate-by-material?campaign_id="+uuid.New().String()+"&from=bad", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for bad from", rr.Code)
	}
}

// TestParseCampaignIDs cobre os dois formatos que /detections aceita: o CSV
// `campaigns` (seleção múltipla de /reports/airtime, mesmo formato de
// /insights e /management) e o `campaign_id` legado de deep-links antigos.
func TestParseCampaignIDs(t *testing.T) {
	a, b := uuid.New(), uuid.New()

	cases := []struct {
		name    string
		query   string
		want    int
		wantErr bool
	}{
		{"vazio devolve nil", "", 0, false},
		{"campaign_id legado", "campaign_id=" + a.String(), 1, false},
		{"campaigns csv", "campaigns=" + a.String() + "," + b.String(), 2, false},
		{"campaigns com espaços", "campaigns=" + a.String() + ", " + b.String(), 2, false},
		{"campaigns ganha do legado", "campaigns=" + a.String() + "&campaign_id=" + b.String(), 1, false},
		{"uuid inválido", "campaigns=nao-e-uuid", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatalf("query de teste inválida: %v", err)
			}
			got, err := parseCampaignIDs(q)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("esperava erro, veio %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCampaignIDs: %v", err)
			}
			if len(got) != tc.want {
				t.Errorf("len = %d, want %d", len(got), tc.want)
			}
		})
	}

	// nil ≠ slice vazio: nil é "sem recorte por campanha" no filtro SQL.
	q, _ := url.ParseQuery("")
	got, _ := parseCampaignIDs(q)
	if got != nil {
		t.Errorf("sem param, parseCampaignIDs = %v, want nil", got)
	}
}
