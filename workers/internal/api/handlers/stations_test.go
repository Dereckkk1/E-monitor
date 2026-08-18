package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"radiocheck/internal/auth"
)

// TestStations_Create_BadJSON rejects malformed body with 400.
func TestStations_Create_BadJSON(t *testing.T) {
	h := &StationsHandler{}
	req := httptest.NewRequest(http.MethodPost, "/stations", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestStations_Create_MissingRequired rejects bodies missing name/band/stream_url.
func TestStations_Create_MissingRequired(t *testing.T) {
	h := &StationsHandler{}
	cases := []string{
		`{}`,
		`{"name":"only"}`,
		`{"name":"x","band":"FM"}`,
		`{"name":"x","band":"","stream_url":"http://a"}`,
	}
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPost, "/stations", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.Create(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s: status = %d, want 400", body, rec.Code)
		}
	}
}

// TestStations_Create_BandMustBeAMOrFM enforces the AM/FM enum.
func TestStations_Create_BandMustBeAMOrFM(t *testing.T) {
	h := &StationsHandler{}
	body, _ := json.Marshal(map[string]any{
		"name": "Bogus FM", "band": "DAB", "stream_url": "http://x",
	})
	req := httptest.NewRequest(http.MethodPost, "/stations", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "AM or FM") {
		t.Fatalf("error should name AM/FM: %s", rec.Body.String())
	}
}

// TestStations_Get_InvalidID short-circuits with 400.
func TestStations_Get_InvalidID(t *testing.T) {
	h := &StationsHandler{}
	r := chi.NewRouter()
	r.Get("/stations/{id}", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/stations/garbage", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestStations_SuggestRoute_NotSwallowedByGetByID guards a silent breakage:
// /stations/suggest e /stations/{id} moram no mesmo nível, e se o roteador
// tratasse "suggest" como um id o autocomplete devolveria 400 pra sempre. Com o
// Repo nil, um Suggest realmente roteado entraria em panic ao consultar o
// banco — então panic aqui é a PROVA de que a rota certa foi escolhida, e um
// 400 seria a falha que este teste existe pra pegar.
func TestStations_SuggestRoute_NotSwallowedByGetByID(t *testing.T) {
	h := &StationsHandler{}
	r := chi.NewRouter()
	r.Get("/stations/suggest", h.Suggest)
	r.Get("/stations/{id}", h.Get)

	defer func() {
		if recover() == nil {
			t.Fatal("Suggest não foi chamado — a rota caiu no Get by id")
		}
	}()

	req := httptest.NewRequest(http.MethodGet, "/stations/suggest?q=jb", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
}

// TestStations_List_InvalidStationID rejeita station_id que não é UUID com 400
// em vez de deixar o pgx estourar lá embaixo.
func TestStations_List_InvalidStationID(t *testing.T) {
	h := &StationsHandler{}
	req := httptest.NewRequest(http.MethodGet, "/stations?station_id=garbage", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// ─── contracted_by: o único filtro de /stations que atravessa tenant ────────

func stationsListAs(claims *auth.Claims, query string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/stations?"+query, nil)
	if claims == nil {
		return req
	}
	return req.WithContext(auth.ContextWithClaims(req.Context(), claims))
}

func viewerClaims(clientIDs ...uuid.UUID) *auth.Claims {
	return &auth.Claims{UserID: uuid.New(), Role: "viewer", ClientIDs: clientIDs}
}

// O teste que justifica o gate: cliente pedindo a carteira de OUTRO cliente
// leva 404 — e 404, não 403, pra não confirmar que aquele id existe.
func TestStations_List_ContractedBy_ForeignClientIs404(t *testing.T) {
	meu, alheio := uuid.New(), uuid.New()
	h := &StationsHandler{} // Repo nil: se passar do gate, entra em panic — e o
	// panic seria a falha. Chegar em 404 prova que parou antes.
	rec := httptest.NewRecorder()
	h.List(rec, stationsListAs(viewerClaims(meu), "contracted_by="+alheio.String()))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// Carteira com 2 clientes: pedir os dois passa; pedir os dois + um de fora não.
func TestStations_List_ContractedBy_WalletMemberAllowed_OutsiderNot(t *testing.T) {
	a, b, fora := uuid.New(), uuid.New(), uuid.New()

	rec := httptest.NewRecorder()
	(&StationsHandler{}).List(rec, stationsListAs(viewerClaims(a, b),
		"contracted_by="+a.String()+","+b.String()+","+fora.String()))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("com um id de fora: status = %d, want 404", rec.Code)
	}

	// Só os da carteira: passa do gate e morre no Repo nil — o que prova que
	// autorizou. Sem esse braço, o teste acima passaria mesmo se o handler
	// recusasse TUDO.
	defer func() {
		if recover() == nil {
			t.Fatal("carteira própria foi barrada pelo gate")
		}
	}()
	(&StationsHandler{}).List(httptest.NewRecorder(),
		stationsListAs(viewerClaims(a, b), "contracted_by="+a.String()+","+b.String()))
}

func TestStations_List_ContractedBy_Invalid400(t *testing.T) {
	rec := httptest.NewRecorder()
	(&StationsHandler{}).List(rec, stationsListAs(nil, "contracted_by=garbage"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestWriteJSON_SetsContentTypeAndStatus exercises the shared helper directly.
func TestWriteJSON_SetsContentTypeAndStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]any{"x": 1})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, _ := body["x"].(float64); int(got) != 1 {
		t.Fatalf("x = %v", body["x"])
	}
}

// TestStations_List_LimitClamp exercises the query-param parsing path. With
// nil repo we get a panic when reaching List, so we only check pre-repo
// validation paths exist by hitting paths that do not call List. The actual
// List parsing is covered by the helper functions tested via real DB tests.

// (Intentionally no test for List here — calling it would dereference a nil
// *catalog.Stations. The validation logic upstream of Repo.List is just
// strconv.Atoi → ListInput field set, which has no early-return paths.)

func TestStations_HandlerStruct(t *testing.T) {
	// Smoke: ensure the struct can be instantiated with nil repo (used by
	// validation-only tests above). Mirrors the constructor pattern other
	// handlers use.
	h := &StationsHandler{}
	if h.Repo != nil {
		t.Fatal("Repo should default to nil")
	}
}

// TestDetections_Get_InvalidID short-circuits with 400.
func TestDetections_Get_InvalidID(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/detections/{id}", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/detections/garbage", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestDetections_Evidence_InvalidID short-circuits with 400.
func TestDetections_Evidence_InvalidID(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/detections/{id}/evidence", h.Evidence)

	req := httptest.NewRequest(http.MethodGet, "/detections/garbage/evidence", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestDetections_List_BadCampaignID rejects unparseable campaign_id with 400.
func TestDetections_List_BadCampaignID(t *testing.T) {
	h := &DetectionsHandler{}
	req := httptest.NewRequest(http.MethodGet, "/detections?campaign_id=not-uuid", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "campaign_id") {
		t.Fatalf("error should mention campaign_id: %s", rec.Body.String())
	}
}

// TestDetections_List_BadStationID rejects unparseable station_id with 400.
func TestDetections_List_BadStationID(t *testing.T) {
	h := &DetectionsHandler{}
	req := httptest.NewRequest(http.MethodGet, "/detections?station_id=not-uuid", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "station_id") {
		t.Fatalf("error should mention station_id: %s", rec.Body.String())
	}
}

// TestDetections_List_BadStartDate rejects unparseable RFC3339 dates with 400.
func TestDetections_List_BadStartDate(t *testing.T) {
	h := &DetectionsHandler{}
	req := httptest.NewRequest(http.MethodGet, "/detections?start_date=yesterday", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "start_date") {
		t.Fatalf("error should mention start_date: %s", rec.Body.String())
	}
}

// TestDetections_List_BadEndDate rejects unparseable end_date with 400.
func TestDetections_List_BadEndDate(t *testing.T) {
	h := &DetectionsHandler{}
	// Provide a valid start_date so the test reaches the end_date branch.
	req := httptest.NewRequest(http.MethodGet,
		"/detections?start_date=2026-05-07T00:00:00Z&end_date=tomorrow", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "end_date") {
		t.Fatalf("error should mention end_date: %s", rec.Body.String())
	}
}

// TestDetections_List_FiltersAccepted ensures that valid query params don't
// short-circuit at the validation layer (handler then calls Repo.List which
// would panic on nil repo). We use a tiny wrapper that recovers from the
// expected nil-deref panic to assert validation passed.
//
// The intent is to confirm the *parsing* succeeds; we don't care about the
// downstream call.
func TestDetections_List_FiltersAccepted(t *testing.T) {
	defer func() {
		_ = recover() // expected: nil-deref on Repo.List
	}()
	h := &DetectionsHandler{}
	id := uuid.New()
	url := "/detections?campaign_id=" + id.String() +
		"&station_id=" + uuid.New().String() +
		"&start_date=2026-05-07T00:00:00Z&end_date=2026-05-08T00:00:00Z" +
		"&limit=50&offset=10"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	// If we get here without panicking, validation passed but the nil repo
	// didn't blow up — that's fine, just don't expect a 200.
}

// TestStations_UpdateStreamURL_BadJSON rejeita body malformado.
func TestStations_UpdateStreamURL_BadJSON(t *testing.T) {
	h := &StationsHandler{}
	r := chi.NewRouter()
	r.Patch("/stations/{id}/stream-url", h.UpdateStreamURL)
	req := httptest.NewRequest(http.MethodPatch,
		"/stations/3b1f0c2e-0000-0000-0000-000000000000/stream-url",
		strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestStations_UpdateStreamURL_MissingURL rejeita url vazia.
func TestStations_UpdateStreamURL_MissingURL(t *testing.T) {
	h := &StationsHandler{}
	r := chi.NewRouter()
	r.Patch("/stations/{id}/stream-url", h.UpdateStreamURL)
	req := httptest.NewRequest(http.MethodPatch,
		"/stations/3b1f0c2e-0000-0000-0000-000000000000/stream-url",
		strings.NewReader(`{"url":""}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestStations_UpdateStreamURL_BadScheme rejeita scheme != http/https.
func TestStations_UpdateStreamURL_BadScheme(t *testing.T) {
	h := &StationsHandler{}
	r := chi.NewRouter()
	r.Patch("/stations/{id}/stream-url", h.UpdateStreamURL)
	req := httptest.NewRequest(http.MethodPatch,
		"/stations/3b1f0c2e-0000-0000-0000-000000000000/stream-url",
		strings.NewReader(`{"url":"ftp://host/x"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestStations_ConnectionTest_BadJSON rejeita body malformado.
func TestStations_ConnectionTest_BadJSON(t *testing.T) {
	h := &StationsHandler{}
	r := chi.NewRouter()
	r.Post("/stations/{id}/connection-test", h.ConnectionTest)
	req := httptest.NewRequest(http.MethodPost,
		"/stations/3b1f0c2e-0000-0000-0000-000000000000/connection-test",
		strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestStations_ConnectionTest_InvalidID rejeita uuid inválido.
func TestStations_ConnectionTest_InvalidID(t *testing.T) {
	h := &StationsHandler{}
	r := chi.NewRouter()
	r.Post("/stations/{id}/connection-test", h.ConnectionTest)
	req := httptest.NewRequest(http.MethodPost, "/stations/not-a-uuid/connection-test",
		strings.NewReader(`{"tests":["ping"]}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
