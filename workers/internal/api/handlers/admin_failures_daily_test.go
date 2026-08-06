package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"radiocheck/internal/catalog"
)

// fakeDailyRepo grava o range que o handler resolveu, pra que os testes possam
// afirmar sobre a NORMALIZAÇÃO (que é onde mora a lógica), não sobre SQL.
type fakeDailyRepo struct {
	gotFrom, gotTo time.Time
	gotMinDown     int
	called         bool
	err            error
}

func (f *fakeDailyRepo) ListDaily(_ context.Context, from, to time.Time, minDown int) (*catalog.FailuresDailyResult, error) {
	f.called = true
	f.gotFrom, f.gotTo, f.gotMinDown = from, to, minDown
	if f.err != nil {
		return nil, f.err
	}
	return &catalog.FailuresDailyResult{
		From: from.Format("2006-01-02"),
		To:   to.Format("2006-01-02"),
		Days: []catalog.DailyPoint{},
	}, nil
}

func doDaily(t *testing.T, repo FailuresDailyRepo, query string) *httptest.ResponseRecorder {
	t.Helper()
	h := &FailuresDailyHandler{Repo: repo}
	req := httptest.NewRequest(http.MethodGet, "/admin/failures-daily?"+query, nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	return rec
}

func iso(t time.Time) string { return t.Format("2006-01-02") }

func todayLocal() time.Time {
	n := time.Now().In(time.Local)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.Local)
}

// Sem params: últimos 30 dias terminando hoje (30 dias inclusive = hoje-29).
func TestFailuresDaily_DefaultRangeIsLast30Days(t *testing.T) {
	repo := &fakeDailyRepo{}
	rec := doDaily(t, repo, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperado 200", rec.Code)
	}
	today := todayLocal()
	if iso(repo.gotTo) != iso(today) {
		t.Errorf("to = %s, esperado hoje (%s)", iso(repo.gotTo), iso(today))
	}
	if want := iso(today.AddDate(0, 0, -29)); iso(repo.gotFrom) != want {
		t.Errorf("from = %s, esperado %s", iso(repo.gotFrom), want)
	}
	if repo.gotMinDown != 60 {
		t.Errorf("min_down_seconds = %d, esperado 60", repo.gotMinDown)
	}
}

// "Este mês" na UI manda o último dia do mês corrente. Isso é uso normal, não
// erro: o handler corta em hoje em vez de responder 400.
func TestFailuresDaily_FutureToIsClampedToToday(t *testing.T) {
	repo := &fakeDailyRepo{}
	today := todayLocal()
	future := today.AddDate(0, 0, 20)
	rec := doDaily(t, repo, fmt.Sprintf("from=%s&to=%s", iso(today.AddDate(0, 0, -5)), iso(future)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperado 200 (clamp, não erro)", rec.Code)
	}
	if iso(repo.gotTo) != iso(today) {
		t.Errorf("to = %s, esperado clamp em hoje (%s)", iso(repo.gotTo), iso(today))
	}
}

func TestFailuresDaily_RejectsFromAfterTo(t *testing.T) {
	repo := &fakeDailyRepo{}
	today := todayLocal()
	rec := doDaily(t, repo, fmt.Sprintf("from=%s&to=%s", iso(today.AddDate(0, 0, -2)), iso(today.AddDate(0, 0, -9))))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, esperado 400", rec.Code)
	}
	if repo.called {
		t.Error("repo não devia ser chamado com range invertido")
	}
}

// O teto de 90 dias espelha o /admin/station-failures: clicar numa barra tem
// que cair numa data que o input de data da aba "Por emissora" aceita.
func TestFailuresDaily_RejectsFromOlderThan90Days(t *testing.T) {
	repo := &fakeDailyRepo{}
	today := todayLocal()
	rec := doDaily(t, repo, fmt.Sprintf("from=%s&to=%s", iso(today.AddDate(0, 0, -120)), iso(today.AddDate(0, 0, -100))))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, esperado 400", rec.Code)
	}
	if repo.called {
		t.Error("repo não devia ser chamado fora da janela de 90 dias")
	}
}

// 90 dias cravados (o preset "Últimos 90 dias") tem que passar — o limite é
// inclusive, senão o próprio botão da UI dá 400.
func TestFailuresDaily_Accepts90DayBoundaryExactly(t *testing.T) {
	repo := &fakeDailyRepo{}
	today := todayLocal()
	rec := doDaily(t, repo, fmt.Sprintf("from=%s&to=%s", iso(today.AddDate(0, 0, -89)), iso(today)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperado 200 no limite exato", rec.Code)
	}
	if !repo.called {
		t.Error("repo devia ter sido chamado")
	}
}

func TestFailuresDaily_RejectsMalformedDate(t *testing.T) {
	for _, q := range []string{"from=18/05/2026", "to=nope", "from=2026-13-45"} {
		repo := &fakeDailyRepo{}
		rec := doDaily(t, repo, q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%q: status = %d, esperado 400", q, rec.Code)
		}
	}
}

// min_down_seconds fora da faixa 0–3600 é ignorado (mantém o default), igual ao
// /admin/station-failures — a UI e a API discordarem sobre a severidade faria
// as duas abas mostrarem números diferentes pro mesmo dia.
func TestFailuresDaily_MinDownSecondsOutOfRangeFallsBackToDefault(t *testing.T) {
	for _, q := range []string{"min_down_seconds=-5", "min_down_seconds=99999", "min_down_seconds=abc"} {
		repo := &fakeDailyRepo{}
		if rec := doDaily(t, repo, q); rec.Code != http.StatusOK {
			t.Fatalf("%q: status = %d", q, rec.Code)
		}
		if repo.gotMinDown != 60 {
			t.Errorf("%q: min_down = %d, esperado fallback 60", q, repo.gotMinDown)
		}
	}
	repo := &fakeDailyRepo{}
	doDaily(t, repo, "min_down_seconds=300")
	if repo.gotMinDown != 300 {
		t.Errorf("min_down = %d, esperado 300", repo.gotMinDown)
	}
}

// Repo nil (deps não montadas) devolve 200 com série vazia em vez de 500 — o
// mesmo contrato do StationFailuresHandler.
func TestFailuresDaily_NilRepoReturnsEmptySeries(t *testing.T) {
	h := &FailuresDailyHandler{}
	req := httptest.NewRequest(http.MethodGet, "/admin/failures-daily", nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperado 200", rec.Code)
	}
	var body catalog.FailuresDailyResult
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("resposta não é JSON válido: %v", err)
	}
	if body.Days == nil {
		t.Error("days = null, esperado [] (o frontend faz .map direto)")
	}
}

func TestFailuresDaily_RepoErrorReturns500(t *testing.T) {
	repo := &fakeDailyRepo{err: fmt.Errorf("boom")}
	if rec := doDaily(t, repo, ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, esperado 500", rec.Code)
	}
}
