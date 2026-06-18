package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

func TestDigestFromDaily(t *testing.T) {
	idA, idB := uuid.New(), uuid.New()
	res := &catalog.DailyResult{
		Mode: "by_date",
		Date: "2026-06-17",
		Summary: catalog.CampaignDailySummary{
			Campaigns: 2, Stations: 3, TotalDeficit: 9,
		},
		Campaigns: []catalog.CampaignDailyFailure{
			{
				Campaign: catalog.CampaignInfo{
					ID: idA, Name: "Verão 2026",
					ClientName: "Cliente A", ClientLogoURL: "logoA",
				},
				Stations: []catalog.CampaignFailureStation{{}, {}}, // 2 emissoras
			},
			{
				Campaign: catalog.CampaignInfo{
					ID: idB, Name: "Liquida Inverno",
					ClientName: "Cliente B", ClientLogoURL: "",
				},
				Stations: []catalog.CampaignFailureStation{{}}, // 1 emissora
			},
		},
	}

	out := digestFromDaily(res, true)

	if out.Date != "2026-06-17" {
		t.Errorf("Date = %q, want 2026-06-17", out.Date)
	}
	if !out.Seen {
		t.Error("Seen = false, want true")
	}
	if out.Summary.Campaigns != 2 || out.Summary.Stations != 3 {
		t.Errorf("Summary = %+v, want {Campaigns:2 Stations:3}", out.Summary)
	}
	if len(out.Campaigns) != 2 {
		t.Fatalf("len(Campaigns) = %d, want 2", len(out.Campaigns))
	}
	if out.Campaigns[0].ID != idA || out.Campaigns[0].Name != "Verão 2026" {
		t.Errorf("Campaigns[0] id/name wrong: %+v", out.Campaigns[0])
	}
	if out.Campaigns[0].ClientName != "Cliente A" || out.Campaigns[0].ClientLogoURL != "logoA" {
		t.Errorf("Campaigns[0] client wrong: %+v", out.Campaigns[0])
	}
	if out.Campaigns[0].StationsFailed != 2 {
		t.Errorf("Campaigns[0].StationsFailed = %d, want 2", out.Campaigns[0].StationsFailed)
	}
	if out.Campaigns[1].StationsFailed != 1 {
		t.Errorf("Campaigns[1].StationsFailed = %d, want 1", out.Campaigns[1].StationsFailed)
	}
}

func TestDigestFromDaily_Empty(t *testing.T) {
	res := &catalog.DailyResult{
		Mode: "by_date", Date: "2026-06-17",
		Summary:   catalog.CampaignDailySummary{},
		Campaigns: []catalog.CampaignDailyFailure{},
	}
	out := digestFromDaily(res, false)
	if out.Campaigns == nil {
		t.Error("Campaigns is nil, want non-nil empty slice (JSON [])")
	}
	if len(out.Campaigns) != 0 {
		t.Errorf("len(Campaigns) = %d, want 0", len(out.Campaigns))
	}
}

// ─── Fakes ───────────────────────────────────────────────────────────

type fakeDigestCampaigns struct {
	res    *catalog.DailyResult
	err    error
	gotDay time.Time
}

func (f *fakeDigestCampaigns) ListForDate(ctx context.Context, day time.Time) (*catalog.DailyResult, error) {
	f.gotDay = day
	return f.res, f.err
}

type fakeDigestSeen struct {
	seen       bool
	markedUser uuid.UUID
	markedKeys []string
}

func (f *fakeDigestSeen) HasRead(ctx context.Context, userID uuid.UUID, key string) (bool, error) {
	return f.seen, nil
}

func (f *fakeDigestSeen) MarkRead(ctx context.Context, userID uuid.UUID, keys []string) (int, error) {
	f.markedUser = userID
	f.markedKeys = append(f.markedKeys, keys...)
	return len(keys), nil
}

func reqWithAdmin(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	return r.WithContext(auth.WithClaims(r.Context(), &auth.Claims{
		UserID: uuid.New(), Role: "admin",
	}))
}

func sampleDaily() *catalog.DailyResult {
	return &catalog.DailyResult{
		Mode: "by_date", Date: "2026-06-17",
		Summary: catalog.CampaignDailySummary{Campaigns: 1, Stations: 2},
		Campaigns: []catalog.CampaignDailyFailure{
			{
				Campaign: catalog.CampaignInfo{ID: uuid.New(), Name: "Verão", ClientName: "Cli"},
				Stations: []catalog.CampaignFailureStation{{}, {}},
			},
		},
	}
}

// ─── GET ─────────────────────────────────────────────────────────────

func TestDigest_Get_Unseen(t *testing.T) {
	h := &DailyFailuresDigestHandler{
		Campaigns: &fakeDigestCampaigns{res: sampleDaily()},
		Seen:      &fakeDigestSeen{seen: false},
	}
	w := httptest.NewRecorder()
	h.Get(w, reqWithAdmin("GET", "/admin/daily-failures-digest"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body digestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body.Seen {
		t.Error("Seen = true, want false")
	}
	if len(body.Campaigns) != 1 || body.Campaigns[0].StationsFailed != 2 {
		t.Errorf("campaigns wrong: %+v", body.Campaigns)
	}
}

func TestDigest_Get_Seen(t *testing.T) {
	h := &DailyFailuresDigestHandler{
		Campaigns: &fakeDigestCampaigns{res: sampleDaily()},
		Seen:      &fakeDigestSeen{seen: true},
	}
	w := httptest.NewRecorder()
	h.Get(w, reqWithAdmin("GET", "/admin/daily-failures-digest"))

	var body digestResponse
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !body.Seen {
		t.Error("Seen = false, want true")
	}
}

func TestDigest_Get_Unauthorized(t *testing.T) {
	h := &DailyFailuresDigestHandler{
		Campaigns: &fakeDigestCampaigns{res: sampleDaily()},
		Seen:      &fakeDigestSeen{},
	}
	w := httptest.NewRecorder()
	// httptest.NewRequest sem claims injetadas → userIDFromReq falha.
	h.Get(w, httptest.NewRequest("GET", "/admin/daily-failures-digest", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// ─── ACK ─────────────────────────────────────────────────────────────

func TestDigest_Ack_MarksYesterdayKey(t *testing.T) {
	seen := &fakeDigestSeen{}
	h := &DailyFailuresDigestHandler{
		Campaigns: &fakeDigestCampaigns{res: sampleDaily()},
		Seen:      seen,
	}
	w := httptest.NewRecorder()
	h.Ack(w, reqWithAdmin("POST", "/admin/daily-failures-digest/ack"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	wantKey := "daily_failures_digest:" + time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if len(seen.markedKeys) != 1 || seen.markedKeys[0] != wantKey {
		t.Errorf("markedKeys = %v, want [%s]", seen.markedKeys, wantKey)
	}
}
