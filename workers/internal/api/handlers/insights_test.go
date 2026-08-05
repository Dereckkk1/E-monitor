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
	"radiocheck/internal/auth"
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

// ── Escopo multi-cliente (agências) ──────────────────────────────────────
//
// A tela é POR CLIENTE por construção (catalog.InsightsParams.ClientID é um
// uuid, não lista). Regra fixada aqui — ver spec §6.7:
//   - admin (scopes == nil): client_id obrigatório na query;
//   - viewer com carteira de 1: client_id FORÇADO pelo JWT, query ignorada;
//   - viewer com carteira de N: client_id obrigatório e tem que estar na
//     carteira (400 sem ele, 403 se for de fora).

// insightsClaims monta claims de viewer com a carteira informada.
func insightsClaims(wallet ...uuid.UUID) *auth.Claims {
	return &auth.Claims{Role: "viewer", ClientID: &wallet[0], ClientIDs: wallet}
}

// insightsReqAs devolve um GET /insights já com as claims montadas no context.
func insightsReqAs(claims *auth.Claims, query string) *http.Request {
	req := httptest.NewRequest("GET", "/insights?"+query, nil)
	return req.WithContext(auth.ContextWithClaims(req.Context(), claims))
}

// Agência com 2 clientes: sem client_id na query o handler não tem como
// escolher — 400, igual ao admin.
func TestInsights_MultiClientScope_RequiresClientID(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	repo := &fakeInsightsRepo{out: &catalog.InsightsPayload{}}
	h := &InsightsHandler{Repo: repo}
	req := insightsReqAs(insightsClaims(a, b), "campaigns="+uuid.NewString())
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if repo.hits != 0 {
		t.Fatalf("hits = %d, want 0 (repo não pode ser chamado sem cliente resolvido)", repo.hits)
	}
}

// client_id fora da carteira é 403 — e o repo não pode nem ser chamado.
func TestInsights_MultiClientScope_RejectsForeignClient(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	foreign := uuid.New()
	repo := &fakeInsightsRepo{out: &catalog.InsightsPayload{}}
	h := &InsightsHandler{Repo: repo}
	req := insightsReqAs(insightsClaims(a, b),
		"client_id="+foreign.String()+"&campaigns="+uuid.NewString())
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if repo.hits != 0 {
		t.Fatalf("hits = %d, want 0 (cliente de fora não pode chegar no repo)", repo.hits)
	}
}

// client_id dentro da carteira é aceito e é o que desce pro repo.
func TestInsights_MultiClientScope_AcceptsWalletClient(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	repo := &fakeInsightsRepo{out: &catalog.InsightsPayload{}}
	h := &InsightsHandler{Repo: repo}
	// Pede o SEGUNDO da carteira: prova que o handler respeita a escolha em vez
	// de cair no primeiro/principal.
	req := insightsReqAs(insightsClaims(a, b),
		"client_id="+b.String()+"&campaigns="+uuid.NewString())
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if repo.hits != 1 {
		t.Fatalf("hits = %d, want 1", repo.hits)
	}
	if repo.got.ClientID != b {
		t.Fatalf("ClientID = %s, want %s", repo.got.ClientID, b)
	}
}

// Um cliente só: o client_id continua sendo FORÇADO pelo JWT, ignorando a
// query. É o comportamento anterior à feature e não pode regredir.
func TestInsights_SingleClientScope_IgnoresQueryClientID(t *testing.T) {
	mine := uuid.New()
	foreign := uuid.New()
	repo := &fakeInsightsRepo{out: &catalog.InsightsPayload{}}
	h := &InsightsHandler{Repo: repo}
	req := insightsReqAs(insightsClaims(mine),
		"client_id="+foreign.String()+"&campaigns="+uuid.NewString())
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if repo.got.ClientID != mine {
		t.Fatalf("ClientID = %s, want %s (query não pode sobrescrever o escopo)",
			repo.got.ClientID, mine)
	}
}

// Token legado (só ClientID, sem ClientIDs) se comporta como carteira de 1:
// ClientScopesFromContext traduz pra lista de um elemento.
func TestInsights_LegacyToken_BehavesAsSingleClient(t *testing.T) {
	mine := uuid.New()
	foreign := uuid.New()
	repo := &fakeInsightsRepo{out: &catalog.InsightsPayload{}}
	h := &InsightsHandler{Repo: repo}
	legacy := &auth.Claims{Role: "viewer", ClientID: &mine} // sem ClientIDs
	req := insightsReqAs(legacy,
		"client_id="+foreign.String()+"&campaigns="+uuid.NewString())
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if repo.got.ClientID != mine {
		t.Fatalf("ClientID = %s, want %s (token legado = carteira de 1)",
			repo.got.ClientID, mine)
	}
}
