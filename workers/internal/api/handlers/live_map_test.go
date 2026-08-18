package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

type fakeLiveMapRepo struct {
	gotCampaigns []uuid.UUID
	gotScope     []uuid.UUID
	gotOpts      catalog.LiveMapOpts
	called       bool
	result       catalog.LiveMapResult
	err          error
}

func (f *fakeLiveMapRepo) Get(ctx context.Context, campaignIDs []uuid.UUID, scope []uuid.UUID, opts catalog.LiveMapOpts) (catalog.LiveMapResult, error) {
	f.called = true
	f.gotCampaigns = campaignIDs
	f.gotScope = scope
	f.gotOpts = opts
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

func TestLiveMapHandler_InvalidCampaignsCSV_400(t *testing.T) {
	h := &LiveMapHandler{Repo: &fakeLiveMapRepo{}}
	rr := httptest.NewRecorder()
	h.Get(rr, newLiveMapReq("?campaigns="+uuid.NewString()+",nope", &auth.Claims{Role: "admin"}))
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
	if len(fake.gotCampaigns) != 1 || fake.gotCampaigns[0] != camp {
		t.Errorf("campaignIDs = %v, want [%v]", fake.gotCampaigns, camp)
	}
}

// Seleção múltipla: o csv `campaigns` chega inteiro e na ordem no repo.
func TestLiveMapHandler_CampaignsCSV_ChegaNoRepo(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	fake := &fakeLiveMapRepo{}
	h := &LiveMapHandler{Repo: fake}
	rr := httptest.NewRecorder()
	h.Get(rr, newLiveMapReq("?campaigns="+strings.Join([]string{a.String(), b.String(), c.String()}, ","),
		&auth.Claims{Role: "admin"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(fake.gotCampaigns) != 3 ||
		fake.gotCampaigns[0] != a || fake.gotCampaigns[1] != b || fake.gotCampaigns[2] != c {
		t.Errorf("campaignIDs = %v, want [%v %v %v]", fake.gotCampaigns, a, b, c)
	}
}

func TestLiveMapHandler_TooManyCampaigns_400(t *testing.T) {
	ids := make([]string, liveMapMaxCampaigns+1)
	for i := range ids {
		ids[i] = uuid.NewString()
	}
	fake := &fakeLiveMapRepo{}
	h := &LiveMapHandler{Repo: fake}
	rr := httptest.NewRecorder()
	h.Get(rr, newLiveMapReq("?campaigns="+strings.Join(ids, ","), &auth.Claims{Role: "admin"}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if fake.called {
		t.Error("repo não deveria ser chamado acima do teto")
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
	if len(fake.gotScope) != 1 || fake.gotScope[0] != cid {
		t.Errorf("scope = %v, want [%v]", fake.gotScope, cid)
	}
}

// O pós-venda é documento histórico: precisa do mapa da campanha mesmo quando
// ela já terminou em "cancelada" (a tela /live-map continua devolvendo 404).
func TestLiveMapHandler_IncludeTerminal_ChegaNoRepo(t *testing.T) {
	camp := uuid.New()

	fake := &fakeLiveMapRepo{}
	h := &LiveMapHandler{Repo: fake}
	h.Get(httptest.NewRecorder(),
		newLiveMapReq("?campaign_id="+camp.String(), &auth.Claims{Role: "admin"}))
	if fake.gotOpts.IncludeTerminal {
		t.Error("sem o parâmetro, IncludeTerminal tem que ser false")
	}

	fake = &fakeLiveMapRepo{}
	h = &LiveMapHandler{Repo: fake}
	h.Get(httptest.NewRecorder(),
		newLiveMapReq("?campaign_id="+camp.String()+"&include_terminal=1", &auth.Claims{Role: "admin"}))
	if !fake.gotOpts.IncludeTerminal {
		t.Error("include_terminal=1 não chegou no repo")
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
	// Uma campanha só: a linha não carrega campaign_id/campaign_name (o pós-venda
	// depende dessa foto continuar igual).
	if _, ok := body.RecentDetections[0]["campaign_name"]; ok {
		t.Error("campaign_name não deveria aparecer com campanha única")
	}
	if _, ok := body.RecentDetections[0]["campaign_id"]; ok {
		t.Error("campaign_id não deveria aparecer com campanha única")
	}
}
