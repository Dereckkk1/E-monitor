package handlers

// cov_a_admin_monitoring_test.go — testes do painel /admin/monitoring
// (telemetria HTTP por rota, atores, bloqueio de IP/usuário, Web Vitals).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/reqmetrics"
)

// ── funções puras ────────────────────────────────────────────────────────────

func TestCovA_Monitoring_RangeWindowENormalize(t *testing.T) {
	casos := []struct {
		in   string
		dur  time.Duration
		norm string
	}{
		{"1h", time.Hour, "1h"},
		{"24h", 24 * time.Hour, "24h"},
		{"7d", 7 * 24 * time.Hour, "7d"},
		{"30d", 30 * 24 * time.Hour, "30d"},
		{"", 24 * time.Hour, "24h"},
		{"90d", 24 * time.Hour, "24h"},
		{"DROP TABLE", 24 * time.Hour, "24h"},
	}
	for _, c := range casos {
		t.Run(c.in, func(t *testing.T) {
			assert.Equal(t, c.dur, rangeWindow(c.in))
			assert.Equal(t, c.norm, normalizeRange(c.in))
		})
	}
}

func TestCovA_Monitoring_SinceFromRange(t *testing.T) {
	antes := time.Now()
	got := sinceFromRange("1h")
	depois := time.Now()
	assert.False(t, got.Before(antes.Add(-time.Hour)), "since não pode ser anterior a agora-1h")
	assert.False(t, got.After(depois.Add(-time.Hour)), "since não pode ser posterior a agora-1h")
}

func TestCovA_Monitoring_HideLocalhost(t *testing.T) {
	sql, args := hideLocalhostClause(true)
	assert.Contains(t, sql, "NOT IN")
	assert.Contains(t, sql, "127.0.0.1")
	assert.Nil(t, args)

	sql, args = hideLocalhostClause(false)
	assert.Empty(t, sql)
	assert.Nil(t, args)

	casos := map[string]bool{
		"":              true, // padrão: esconde
		"true":          true,
		"1":             true,
		"false":         false,
		"0":             false,
		"qualquercoisa": false,
	}
	for qs, want := range casos {
		req := httptest.NewRequest(http.MethodGet, "/x?hideLocalhost="+qs, nil)
		if qs == "" {
			req = httptest.NewRequest(http.MethodGet, "/x", nil)
		}
		assert.Equal(t, want, queryHideLocalhost(req), "hideLocalhost=%q", qs)
	}
}

func TestCovA_Monitoring_HealthBucket(t *testing.T) {
	casos := []struct {
		p95  int
		err  float64
		want string
	}{
		{0, 0, "good"},
		{499, 0.99, "good"},
		{500, 0, "warning"},
		{100, 1, "warning"},
		{1999, 4.99, "warning"},
		{2000, 0, "critical"},
		{100, 5, "critical"},
		{3000, 50, "critical"},
	}
	for _, c := range casos {
		assert.Equal(t, c.want, healthBucket(c.p95, c.err), "p95=%d err=%v", c.p95, c.err)
	}
}

func TestCovA_Monitoring_RiskLevel(t *testing.T) {
	casos := []struct {
		nome            string
		reqs, rotas, nf int
		autenticado     bool
		want            string
	}{
		{"autenticado tranquilo", 3, 1, 0, true, "low"},
		{"anônimo sem sinais", 3, 1, 0, false, "medium"},           // só +15
		{"autenticado com 10% de 404", 10, 1, 1, true, "low"},      // +10
		{"autenticado com 20% de 404", 10, 1, 2, true, "medium"},   // +20
		{"autenticado com 50% de 404", 10, 1, 5, true, "high"},     // +40
		{"varredura de rotas autenticada", 10, 8, 0, true, "high"}, // +35
		{"meia varredura autenticada", 10, 5, 0, true, "medium"},   // +15
		{"scanner anônimo", 10, 10, 10, false, "critical"},         // 40+35+15
		{"volume alto anônimo", 500, 1, 0, false, "medium"},        // 15+10
		{"volume médio autenticado", 200, 1, 0, true, "low"},       // +5
		{"sem requests", 0, 0, 0, true, "low"},
		{"poucas requests ignoram razão de rotas", 4, 4, 0, true, "low"}, // reqs<5
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			assert.Equal(t, c.want, riskLevel(c.reqs, c.rotas, c.nf, c.autenticado))
		})
	}
}

func TestCovA_Monitoring_RequestIP(t *testing.T) {
	casos := map[string]string{
		"192.0.2.10:54321": "192.0.2.10",
		"[2001:db8::1]:80": "2001:db8::1",
		"semporta":         "semporta",
		"192.0.2.10:":      "192.0.2.10:", // porta vazia: devolve cru
	}
	for in, want := range casos {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = in
		assert.Equal(t, want, requestIP(req), "RemoteAddr=%q", in)
	}
}

func TestCovA_Monitoring_HumanDuration(t *testing.T) {
	casos := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{59 * time.Second, "59s"},
		{time.Minute, "1m"},
		{59*time.Minute + 59*time.Second, "59m"},
		{time.Hour, "1h 0m"},
		{5*time.Hour + 30*time.Minute, "5h 30m"},
		{24 * time.Hour, "1d 0h"},
		{49*time.Hour + 20*time.Minute, "2d 1h"},
	}
	for _, c := range casos {
		assert.Equal(t, c.want, humanDuration(c.d), "d=%v", c.d)
	}
}

func TestCovA_Monitoring_Runtime(t *testing.T) {
	used, total, rss := readMemoryMB()
	assert.GreaterOrEqual(t, used, 0)
	assert.GreaterOrEqual(t, total, used, "heap total (HeapSys) nunca é menor que o em uso")
	assert.Greater(t, rss, 0, "o processo de teste ocupa pelo menos 1MB")
	assert.Equal(t, runtime.Version(), goVersion())
}

// ── handlers (banco) ─────────────────────────────────────────────────────────

type covAMetrica struct {
	Route  string
	Method string
	Status int
	DurMs  int
	IP     string
	UserID *uuid.UUID
	Email  string
	Quando time.Time
	Erro   bool
	Lenta  bool
}

func covAMon(t *testing.T) (context.Context, *pgxpool.Pool, *AdminMonitoringHandler) {
	t.Helper()
	ctx, pool := covADB(t)
	_, err := pool.Exec(ctx, `TRUNCATE system_metrics, blocked_ips, web_vitals RESTART IDENTITY`)
	require.NoError(t, err)
	h := &AdminMonitoringHandler{
		DB:    pool,
		Block: reqmetrics.NewBlockList(ctx, pool, zap.NewNop()),
		Log:   zap.NewNop(),
	}
	return ctx, pool, h
}

func covASeedMetricas(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ms ...covAMetrica) {
	t.Helper()
	// Um único "agora" para o lote: linhas semeadas juntas caem no mesmo balde
	// de hora mesmo se o teste rodar na virada da hora.
	padrao := time.Now().Add(-10 * time.Minute)
	for _, m := range ms {
		if m.Method == "" {
			m.Method = "GET"
		}
		if m.Quando.IsZero() {
			m.Quando = padrao
		}
		var email *string
		if m.Email != "" {
			email = &m.Email
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO system_metrics (ts, route, method, status_code, duration_ms, ip, user_id, user_email, is_error, is_slow)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			m.Quando, m.Route, m.Method, m.Status, m.DurMs, m.IP, m.UserID, email, m.Erro, m.Lenta)
		require.NoError(t, err)
	}
}

func covAMonRouter(h *AdminMonitoringHandler) chi.Router {
	r := chi.NewRouter()
	r.Get("/overview", h.Overview)
	r.Get("/routes", h.Routes)
	r.Get("/errors", h.Errors)
	r.Get("/slow", h.Slow)
	r.Get("/timeline", h.Timeline)
	r.Get("/top-actors", h.TopActors)
	r.Get("/actor-detail", h.ActorDetail)
	r.Get("/blocked-ips", h.BlockedIPs)
	r.Post("/block-ip", h.BlockIP)
	r.Delete("/block-ip/{ip}", h.UnblockIP)
	r.Post("/block-user/{userId}", h.BlockUser)
	r.Get("/vitals", h.Vitals)
	r.Post("/web-vitals", h.PostVitals)
	return r
}

func TestCovA_Monitoring_Overview(t *testing.T) {
	ctx, pool, h := covAMon(t)
	covASeedMetricas(t, ctx, pool,
		covAMetrica{Route: "/v1/a", Status: 200, DurMs: 100, IP: "198.51.100.1"},
		covAMetrica{Route: "/v1/a", Status: 500, DurMs: 300, IP: "198.51.100.1", Erro: true},
		covAMetrica{Route: "/v1/c", Status: 200, DurMs: 3000, IP: "198.51.100.2", Lenta: true},
		covAMetrica{Route: "/v1/b", Status: 200, DurMs: 50, IP: "127.0.0.1"},
		covAMetrica{Route: "/v1/old", Status: 200, DurMs: 10, IP: "198.51.100.3", Quando: time.Now().Add(-3 * time.Hour)},
	)
	r := covAMonRouter(h)

	t.Run("esconde localhost e ignora fora da janela", func(t *testing.T) {
		rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/overview?range=1h", nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		period := body["period"].(map[string]any)
		assert.Equal(t, "1h", period["range"])
		assert.EqualValues(t, 3, period["totalRequests"])
		assert.EqualValues(t, 1, period["totalErrors"])
		assert.EqualValues(t, 1, period["totalSlow"])
		assert.Equal(t, "33.33%", period["errorRate"])
		assert.EqualValues(t, 1133, period["avgDuration"]) // (100+300+3000)/3 = 1133,3
		srv := body["server"].(map[string]any)
		assert.True(t, strings.HasPrefix(srv["node"].(string), "go"), "node = %v", srv["node"])
		assert.NotEmpty(t, srv["uptime"].(map[string]any)["human"])
	})

	t.Run("hideLocalhost=false inclui o tráfego interno", func(t *testing.T) {
		rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/overview?range=1h&hideLocalhost=false", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		period := covADecode(t, rec)["period"].(map[string]any)
		assert.EqualValues(t, 4, period["totalRequests"])
	})

	t.Run("janela de 24h pega a linha antiga", func(t *testing.T) {
		rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/overview?range=bogus", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		period := covADecode(t, rec)["period"].(map[string]any)
		assert.Equal(t, "24h", period["range"], "range inválido vira 24h")
		assert.EqualValues(t, 4, period["totalRequests"])
	})
}

func TestCovA_Monitoring_Overview_SemDadosTaxaZero(t *testing.T) {
	_, _, h := covAMon(t)
	rec := covAServe(covAMonRouter(h), httptest.NewRequest(http.MethodGet, "/overview", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	period := covADecode(t, rec)["period"].(map[string]any)
	assert.Equal(t, "0.00%", period["errorRate"])
	assert.EqualValues(t, 0, period["totalRequests"])
}

func TestCovA_Monitoring_Routes(t *testing.T) {
	ctx, pool, h := covAMon(t)
	var ms []covAMetrica
	for _, d := range []int{100, 200, 300, 400, 500} { // p50=300, p95=p99=500 -> warning
		ms = append(ms, covAMetrica{Route: "/v1/quente", Status: 200, DurMs: d, IP: "198.51.100.1"})
	}
	ms = append(ms,
		covAMetrica{Route: "/v1/lenta", Status: 200, DurMs: 3000, IP: "198.51.100.1", Lenta: true}, // critical
		covAMetrica{Route: "/v1/ok", Status: 200, DurMs: 10, IP: "198.51.100.1"},
		covAMetrica{Route: "/v1/ok", Status: 500, DurMs: 20, IP: "198.51.100.1", Erro: true}, // 50% erro -> critical
	)
	covASeedMetricas(t, ctx, pool, ms...)

	rec := covAServe(covAMonRouter(h), httptest.NewRequest(http.MethodGet, "/routes?range=24h", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := covADecode(t, rec)
	assert.EqualValues(t, 3, body["totalRoutes"])
	routes := body["routes"].([]any)
	require.Len(t, routes, 3)

	porRota := map[string]map[string]any{}
	for _, it := range routes {
		m := it.(map[string]any)
		porRota[m["route"].(string)] = m
	}
	assert.Equal(t, "/v1/quente", routes[0].(map[string]any)["route"], "ordenado por contagem desc")

	q := porRota["/v1/quente"]
	assert.EqualValues(t, 5, q["count"])
	assert.EqualValues(t, 300, q["p50"])
	assert.EqualValues(t, 500, q["p95"])
	assert.EqualValues(t, 500, q["p99"])
	assert.EqualValues(t, 300, q["avg"])
	assert.EqualValues(t, 500, q["max"])
	assert.Equal(t, "0.00%", q["errorRate"])
	assert.Equal(t, "warning", q["health"])

	l := porRota["/v1/lenta"]
	assert.EqualValues(t, 1, l["slowCount"])
	assert.Equal(t, "critical", l["health"])

	o := porRota["/v1/ok"]
	assert.EqualValues(t, 1, o["errorCount"])
	assert.Equal(t, "50.00%", o["errorRate"])
	assert.Equal(t, "critical", o["health"])
}

func TestCovA_Monitoring_ErrorsESlow(t *testing.T) {
	ctx, pool, h := covAMon(t)
	covASeedMetricas(t, ctx, pool,
		covAMetrica{Route: "/v1/x", Status: 500, DurMs: 100, IP: "198.51.100.1", Erro: true},
		covAMetrica{Route: "/v1/x", Status: 500, DurMs: 300, IP: "198.51.100.1", Erro: true},
		covAMetrica{Route: "/v1/y", Status: 502, DurMs: 40, IP: "198.51.100.1", Erro: true},
		covAMetrica{Route: "/v1/z", Status: 200, DurMs: 2500, IP: "198.51.100.1", Lenta: true, Method: "POST"},
		covAMetrica{Route: "/v1/w", Status: 200, DurMs: 9000, IP: "198.51.100.1", Lenta: true},
		covAMetrica{Route: "/v1/fine", Status: 200, DurMs: 5, IP: "198.51.100.1"},
	)
	r := covAMonRouter(h)

	rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/errors", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := covADecode(t, rec)
	assert.EqualValues(t, 3, body["totalErrors"])
	errs := body["errors"].([]any)
	require.Len(t, errs, 2)
	primeiro := errs[0].(map[string]any)
	assert.Equal(t, "/v1/x", primeiro["route"], "mais frequente primeiro")
	assert.EqualValues(t, 500, primeiro["statusCode"])
	assert.EqualValues(t, 2, primeiro["count"])
	assert.EqualValues(t, 200, primeiro["avgDuration"])

	rec = covAServe(r, httptest.NewRequest(http.MethodGet, "/slow", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body = covADecode(t, rec)
	assert.EqualValues(t, 2, body["totalSlow"])
	reqs := body["requests"].([]any)
	require.Len(t, reqs, 2)
	assert.Equal(t, "/v1/w", reqs[0].(map[string]any)["route"], "mais lenta primeiro")
	assert.EqualValues(t, 9000, reqs[0].(map[string]any)["duration"])
	assert.Equal(t, "POST", reqs[1].(map[string]any)["method"])
}

func TestCovA_Monitoring_Timeline(t *testing.T) {
	ctx, pool, h := covAMon(t)
	quando := time.Now().Add(-90 * time.Minute)
	covASeedMetricas(t, ctx, pool,
		covAMetrica{Route: "/v1/a", Status: 200, DurMs: 100, IP: "198.51.100.1", Quando: quando},
		covAMetrica{Route: "/v1/a", Status: 500, DurMs: 300, IP: "198.51.100.1", Quando: quando, Erro: true},
		covAMetrica{Route: "/v1/a", Status: 200, DurMs: 2500, IP: "198.51.100.1", Quando: quando, Lenta: true},
	)
	r := covAMonRouter(h)
	reHora := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:00$`)
	reDia := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

	casos := []struct {
		rng, agrupado string
		re            *regexp.Regexp
	}{
		{"24h", "hour", reHora},
		{"1h", "hour", reHora}, // 90min atrás fica fora de 1h: lista vazia, ainda assim "hour"
		{"7d", "day", reDia},
		{"30d", "day", reDia},
	}
	for _, c := range casos {
		t.Run(c.rng, func(t *testing.T) {
			rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/timeline?range="+c.rng, nil))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			body := covADecode(t, rec)
			assert.Equal(t, c.agrupado, body["groupedBy"])
			assert.Equal(t, c.rng, body["range"])
			tl := body["timeline"].([]any)
			if c.rng == "1h" {
				assert.Empty(t, tl)
				return
			}
			require.Len(t, tl, 1, "as 3 linhas caem no mesmo balde")
			row := tl[0].(map[string]any)
			assert.Regexp(t, c.re, row["period"])
			assert.EqualValues(t, 3, row["totalRequests"])
			assert.EqualValues(t, 1, row["totalErrors"])
			assert.EqualValues(t, 1, row["totalSlow"])
			assert.EqualValues(t, 967, row["avgDuration"]) // (100+300+2500)/3 = 966,67
		})
	}
}

func TestCovA_Monitoring_TopActors(t *testing.T) {
	ctx, pool, h := covAMon(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	var email string
	require.NoError(t, pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, admin).Scan(&email))

	var ms []covAMetrica
	// ator autenticado, IP bloqueado: 6 requests em 2 rotas.
	for i := 0; i < 6; i++ {
		ms = append(ms, covAMetrica{Route: []string{"/v1/a", "/v1/b"}[i%2], Status: 200, DurMs: 10,
			IP: "203.0.113.5", UserID: &admin, Email: email})
	}
	// scanner anônimo: 10 requests, 10 rotas, 10x 404.
	for i := 0; i < 10; i++ {
		ms = append(ms, covAMetrica{Route: "/wp/" + string(rune('a'+i)), Status: 404, DurMs: 5, IP: "203.0.113.9"})
	}
	// anônimo discreto: 2 requests.
	ms = append(ms, covAMetrica{Route: "/v1/a", Status: 200, DurMs: 5, IP: "203.0.113.7"},
		covAMetrica{Route: "/v1/a", Status: 500, DurMs: 5, IP: "203.0.113.7", Erro: true, Lenta: true})
	covASeedMetricas(t, ctx, pool, ms...)
	_, err := pool.Exec(ctx, `INSERT INTO blocked_ips (ip, reason) VALUES ('203.0.113.5', 'teste de bloqueio')`)
	require.NoError(t, err)

	rec := covAServe(covAMonRouter(h), httptest.NewRequest(http.MethodGet, "/top-actors?range=24h", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := covADecode(t, rec)
	assert.EqualValues(t, 3, body["totalActors"])
	actors := body["actors"].([]any)
	require.Len(t, actors, 3)

	scanner := actors[0].(map[string]any)
	assert.Equal(t, "203.0.113.9", scanner["ip"], "10 requests: o maior vem primeiro")
	assert.EqualValues(t, 10, scanner["notFoundCount"])
	assert.EqualValues(t, 10, scanner["uniqueRouteCount"])
	assert.Equal(t, "critical", scanner["riskLevel"])
	assert.Nil(t, scanner["userId"])
	assert.Nil(t, scanner["userEmail"])
	assert.Equal(t, false, scanner["isIPBlocked"])

	logado := actors[1].(map[string]any)
	assert.Equal(t, "203.0.113.5", logado["ip"])
	assert.Equal(t, admin.String(), logado["userId"])
	assert.Equal(t, email, logado["userEmail"])
	assert.Equal(t, true, logado["isIPBlocked"])
	assert.Equal(t, "teste de bloqueio", logado["blockedReason"])
	assert.NotNil(t, logado["blockedAt"])
	assert.Equal(t, "low", logado["riskLevel"])

	discreto := actors[2].(map[string]any)
	assert.Equal(t, "203.0.113.7", discreto["ip"])
	assert.EqualValues(t, 1, discreto["errorCount"])
	assert.EqualValues(t, 1, discreto["slowCount"])
	assert.Equal(t, "medium", discreto["riskLevel"], "anônimo sem outros sinais = +15")
}

func TestCovA_Monitoring_ActorDetail(t *testing.T) {
	ctx, pool, h := covAMon(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	outro := mcptest.Usuario(t, ctx, pool, "operator", true)
	covASeedMetricas(t, ctx, pool,
		covAMetrica{Route: "/v1/a", Status: 200, DurMs: 10, IP: "203.0.113.5", UserID: &admin, Email: "admin@example.com"},
		covAMetrica{Route: "/v1/a", Status: 500, DurMs: 20, IP: "203.0.113.5", UserID: &admin, Email: "admin@example.com", Erro: true},
		covAMetrica{Route: "/v1/b", Status: 200, DurMs: 30, IP: "203.0.113.5"},
		covAMetrica{Route: "/v1/c", Status: 200, DurMs: 30, IP: "203.0.113.99", UserID: &outro},
	)
	r := covAMonRouter(h)

	t.Run("sem ip nem userId é 400", func(t *testing.T) {
		rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/actor-detail", nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "ip or userId required")
	})

	t.Run("userId inválido sem ip também é 400", func(t *testing.T) {
		rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/actor-detail?userId=nao-e-uuid", nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("só ip", func(t *testing.T) {
		rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/actor-detail?ip=203.0.113.5&range=7d", nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.Equal(t, "7d", body["range"])
		assert.Len(t, body["requests"].([]any), 3)
		tops := body["topRoutes"].([]any)
		require.NotEmpty(t, tops)
		assert.Equal(t, "/v1/a", tops[0].(map[string]any)["route"])
		assert.EqualValues(t, 2, tops[0].(map[string]any)["count"])
		tl := body["timeline"].([]any)
		require.Len(t, tl, 1)
		assert.EqualValues(t, 3, tl[0].(map[string]any)["count"])
		assert.EqualValues(t, 1, tl[0].(map[string]any)["errors"])
	})

	t.Run("ip e userId juntos filtram pelos dois", func(t *testing.T) {
		rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/actor-detail?ip=203.0.113.5&userId="+admin.String(), nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		reqs := body["requests"].([]any)
		assert.Len(t, reqs, 2, "a linha anônima do mesmo IP não entra")
		assert.Equal(t, "admin@example.com", reqs[0].(map[string]any)["userEmail"])
		assert.Equal(t, "24h", body["range"])
	})

	t.Run("só userId", func(t *testing.T) {
		rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/actor-detail?userId="+outro.String(), nil))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Len(t, covADecode(t, rec)["requests"].([]any), 1)
	})
}

func TestCovA_Monitoring_BlockedIPsEBloqueio(t *testing.T) {
	ctx, pool, h := covAMon(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	r := covAMonRouter(h)
	comClaims := func(req *http.Request) *http.Request { return covAWith(req, covAClaims("admin", admin)) }

	t.Run("lista vazia", func(t *testing.T) {
		rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/blocked-ips", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, covADecode(t, rec)["blockedIPs"].([]any))
	})

	t.Run("BlockIP recusas", func(t *testing.T) {
		rec := covAServe(r, covAJSON(t, http.MethodPost, "/block-ip", "{nao-json"))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid body")

		rec = covAServe(r, covAJSON(t, http.MethodPost, "/block-ip", map[string]string{"ip": "   "}))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "ip is required")
		assert.False(t, h.Block.Has("   "))
	})

	t.Run("BlockIP grava, registra quem bloqueou e atualiza a lista em memória", func(t *testing.T) {
		req := comClaims(covAJSON(t, http.MethodPost, "/block-ip", map[string]string{"ip": " 198.51.100.77 "}))
		rec := covAServe(r, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "198.51.100.77", covADecode(t, rec)["ip"], "o IP é aparado")
		assert.True(t, h.Block.Has("198.51.100.77"))

		var reason, byID, byEmail string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT reason, blocked_by_id::text, blocked_by_email FROM blocked_ips WHERE ip = '198.51.100.77'`).
			Scan(&reason, &byID, &byEmail))
		assert.Equal(t, "Bloqueado manualmente pelo painel admin", reason, "motivo padrão")
		assert.Equal(t, admin.String(), byID)
		assert.Contains(t, byEmail, "@mcp.teste")

		// Bloquear de novo atualiza o motivo (ON CONFLICT), não duplica.
		req = comClaims(covAJSON(t, http.MethodPost, "/block-ip", map[string]string{"ip": "198.51.100.77", "reason": "novo motivo"}))
		require.Equal(t, http.StatusOK, covAServe(r, req).Code)
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM blocked_ips`).Scan(&n))
		assert.Equal(t, 1, n)
		require.NoError(t, pool.QueryRow(ctx, `SELECT reason FROM blocked_ips WHERE ip='198.51.100.77'`).Scan(&reason))
		assert.Equal(t, "novo motivo", reason)

		rec = covAServe(r, httptest.NewRequest(http.MethodGet, "/blocked-ips", nil))
		list := covADecode(t, rec)["blockedIPs"].([]any)
		require.Len(t, list, 1)
		assert.Equal(t, "198.51.100.77", list[0].(map[string]any)["ip"])
		assert.Equal(t, "novo motivo", list[0].(map[string]any)["reason"])
		assert.Equal(t, admin.String(), list[0].(map[string]any)["blockedById"])
	})

	t.Run("BlockIP sem claims grava sem autor e sem BlockList/Log", func(t *testing.T) {
		h2 := &AdminMonitoringHandler{DB: pool}
		rec := covAServe(covAMonRouter(h2), covAJSON(t, http.MethodPost, "/block-ip", map[string]string{"ip": "198.51.100.88"}))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var byID *string
		require.NoError(t, pool.QueryRow(ctx, `SELECT blocked_by_id::text FROM blocked_ips WHERE ip='198.51.100.88'`).Scan(&byID))
		assert.Nil(t, byID)
	})

	t.Run("UnblockIP remove do banco e da memória", func(t *testing.T) {
		require.True(t, h.Block.Has("198.51.100.77"))
		rec := covAServe(r, httptest.NewRequest(http.MethodDelete, "/block-ip/198.51.100.77", nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "198.51.100.77", covADecode(t, rec)["ip"])
		assert.False(t, h.Block.Has("198.51.100.77"))
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM blocked_ips WHERE ip='198.51.100.77'`).Scan(&n))
		assert.Zero(t, n)

		// Desbloquear de novo é inofensivo.
		rec = covAServe(r, httptest.NewRequest(http.MethodDelete, "/block-ip/198.51.100.77", nil))
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("UnblockIP sem o parâmetro é 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/block-ip/", nil)
		rec := httptest.NewRecorder()
		h.UnblockIP(rec, req) // chamada direta: chi não casaria a rota sem {ip}
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "ip is required")
	})
}

func TestCovA_Monitoring_BlockUser(t *testing.T) {
	ctx, pool, h := covAMon(t)
	alvo := mcptest.Usuario(t, ctx, pool, "operator", true)
	r := covAMonRouter(h)

	rec := covAServe(r, httptest.NewRequest(http.MethodPost, "/block-user/nao-e-uuid", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid user id")

	rec = covAServe(r, httptest.NewRequest(http.MethodPost, "/block-user/"+uuid.NewString(), nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "user not found")

	rec = covAServe(r, httptest.NewRequest(http.MethodPost, "/block-user/"+alvo.String(), nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := covADecode(t, rec)
	assert.Equal(t, alvo.String(), body["userId"])
	assert.Contains(t, body["email"], "@mcp.teste")
	assert.Equal(t, "Usuário bloqueado com sucesso", body["message"])
	var ativo bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT is_active FROM users WHERE id=$1`, alvo).Scan(&ativo))
	assert.False(t, ativo)
}

func TestCovA_Monitoring_WebVitals(t *testing.T) {
	ctx, pool, h := covAMon(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	r := covAMonRouter(h)

	post := func(corpo any, claims bool) *httptest.ResponseRecorder {
		req := covAJSON(t, http.MethodPost, "/web-vitals", corpo)
		req.RemoteAddr = "198.51.100.44:5000"
		if claims {
			req = covAWith(req, covAClaims("admin", admin))
		}
		return covAServe(r, req)
	}

	t.Run("recusas", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, post("{x", false).Code)

		rec := post(map[string]any{"name": "XYZ", "value": 1, "rating": "good", "page": "/p"}, false)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "unknown metric name")

		rec = post(map[string]any{"name": "LCP", "value": 1, "rating": "meh", "page": "/p"}, false)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid rating")
	})

	t.Run("anônimo grava com IP e sem usuário", func(t *testing.T) {
		rec := post(map[string]any{"name": "LCP", "value": 1200.5, "rating": "good", "page": "/dashboard"}, false)
		require.Equal(t, http.StatusNoContent, rec.Code)
		var ip string
		var uid *uuid.UUID
		require.NoError(t, pool.QueryRow(ctx, `SELECT ip, user_id FROM web_vitals WHERE page='/dashboard'`).Scan(&ip, &uid))
		assert.Equal(t, "198.51.100.44", ip)
		assert.Nil(t, uid)
	})

	t.Run("autenticado grava usuário e e-mail", func(t *testing.T) {
		rec := post(map[string]any{"name": "CLS", "value": 0.2, "rating": "poor", "page": "/campanhas"}, true)
		require.Equal(t, http.StatusNoContent, rec.Code)
		var uid uuid.UUID
		var em string
		require.NoError(t, pool.QueryRow(ctx, `SELECT user_id, user_email FROM web_vitals WHERE page='/campanhas'`).Scan(&uid, &em))
		assert.Equal(t, admin, uid)
		assert.Contains(t, em, "@mcp.teste")
	})

	t.Run("falha do banco vira 202 sem quebrar o frontend", func(t *testing.T) {
		h2 := &AdminMonitoringHandler{DB: covAClosedPool(t)}
		req := covAJSON(t, http.MethodPost, "/web-vitals", map[string]any{"name": "TTFB", "value": 10, "rating": "good", "page": "/x"})
		rec := covAServe(covAMonRouter(h2), req)
		assert.Equal(t, http.StatusAccepted, rec.Code)
	})

	t.Run("Vitals agrega por métrica e página", func(t *testing.T) {
		_, err := pool.Exec(ctx, `TRUNCATE web_vitals`)
		require.NoError(t, err)
		seed := []struct {
			name, rating, page string
			v                  float64
		}{
			{"LCP", "good", "/home", 1000},
			{"LCP", "good", "/home", 2000},
			{"LCP", "poor", "/home", 5000},
			{"LCP", "needs-improvement", "/home", 3000},
			{"INP", "good", "/home", 100},
			{"LCP", "good", "http://localhost:5173/x", 900}, // escondido por padrão
		}
		for _, s := range seed {
			_, err := pool.Exec(ctx, `INSERT INTO web_vitals (name, value, rating, page) VALUES ($1,$2,$3,$4)`, s.name, s.v, s.rating, s.page)
			require.NoError(t, err)
		}

		rec := covAServe(r, httptest.NewRequest(http.MethodGet, "/vitals?range=7d", nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := covADecode(t, rec)
		assert.Equal(t, "7d", body["range"])
		vitals := body["vitals"].([]any)
		require.Len(t, vitals, 2, "INP/home e LCP/home; o localhost fica de fora")
		inp := vitals[0].(map[string]any) // ORDER BY name ASC
		assert.Equal(t, "INP", inp["name"])
		assert.Equal(t, "100.0%", inp["goodPercent"])
		assert.Equal(t, "0.0%", inp["poorPercent"])
		lcp := vitals[1].(map[string]any)
		assert.Equal(t, "LCP", lcp["name"])
		assert.EqualValues(t, 4, lcp["count"])
		assert.EqualValues(t, 2750, lcp["avg"])
		assert.EqualValues(t, 3000, lcp["p75"])
		assert.EqualValues(t, 2, lcp["goodCount"])
		assert.EqualValues(t, 1, lcp["poorCount"])
		assert.EqualValues(t, 1, lcp["needsImprovementCount"])
		assert.Equal(t, "50.0%", lcp["goodPercent"])
		assert.Equal(t, "25.0%", lcp["poorPercent"])

		rec = covAServe(r, httptest.NewRequest(http.MethodGet, "/vitals?hideLocalhost=false", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Len(t, covADecode(t, rec)["vitals"].([]any), 3, "com hideLocalhost=false a página localhost aparece")
	})
}

// Todo handler do painel responde 500 (e não panica) quando o banco falha.
func TestCovA_Monitoring_FalhaDoBancoDa500(t *testing.T) {
	h := &AdminMonitoringHandler{DB: covAClosedPool(t), Log: zap.NewNop()}
	r := covAMonRouter(h)
	casos := []struct {
		metodo, alvo, trecho string
		corpo                any
	}{
		{http.MethodGet, "/overview", "overview query failed", nil},
		{http.MethodGet, "/routes", "routes query failed", nil},
		{http.MethodGet, "/errors", "errors query failed", nil},
		{http.MethodGet, "/slow", "slow query failed", nil},
		{http.MethodGet, "/timeline", "timeline query failed", nil},
		{http.MethodGet, "/top-actors", "actors query failed", nil},
		{http.MethodGet, "/blocked-ips", "blocked-ips query failed", nil},
		{http.MethodGet, "/vitals", "vitals query failed", nil},
		{http.MethodPost, "/block-ip", "block-ip insert failed", map[string]string{"ip": "198.51.100.1"}},
		{http.MethodDelete, "/block-ip/198.51.100.1", "unblock-ip failed", nil},
	}
	for _, c := range casos {
		t.Run(c.metodo+" "+c.alvo, func(t *testing.T) {
			rec := covAServe(r, covAJSON(t, c.metodo, c.alvo, c.corpo))
			assert.Equal(t, http.StatusInternalServerError, rec.Code)
			assert.Contains(t, rec.Body.String(), c.trecho)
		})
	}

	// block-user com banco fora: a rota responde 404 (não distingue erro de ausência).
	rec := covAServe(r, httptest.NewRequest(http.MethodPost, "/block-user/"+uuid.NewString(), nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
