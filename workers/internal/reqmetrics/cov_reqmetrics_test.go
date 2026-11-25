package reqmetrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestCovZ_IsAdminExempt(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/metrics", true},
		{"/v1/internal/health", true},
		{"/v1/internal/auth/login", true},
		{"/v1/internal/admin/monitoring/ips", true},
		{"/v1/internal/admin/", true},
		{"/v1/internal/admin", false},
		{"/v1/internal/campaigns", false},
		{"/", false},
		{"/metrics/extra", false},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			assert.Equal(t, c.want, isAdminExempt(c.path))
		})
	}
}

func TestCovZ_ShouldSkip(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/metrics", true},
		{"/v1/internal/health", true},
		{"/debug/pprof", true},
		{"/debug/pprof/heap", true},
		{"/v1/internal/campaigns", false},
		{"/v1/internal/health/deep", false},
		{"/", false},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			assert.Equal(t, c.want, shouldSkip(c.path))
		})
	}
}

func TestCovZ_ClientIP(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want string
	}{
		{"ipv4 com porta", "203.0.113.7:51234", "203.0.113.7"},
		{"ipv6 entre colchetes", "[2001:db8::1]:8080", "2001:db8::1"},
		{"ipv6 loopback", "[::1]:443", "::1"},
		{"sem porta", "203.0.113.7", "203.0.113.7"},
		{"vazio", "", ""},
		{"termina em dois pontos", "203.0.113.7:", "203.0.113.7:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			r.RemoteAddr = c.addr
			assert.Equal(t, c.want, clientIP(r))
		})
	}
}

func TestCovZ_SetUserID(t *testing.T) {
	uid := uuid.New()

	t.Run("sem holder no contexto e silencioso", func(t *testing.T) {
		assert.NotPanics(t, func() { SetUserID(context.Background(), uid) })
	})

	t.Run("com holder grava o id", func(t *testing.T) {
		h := &userIDHolder{}
		ctx := context.WithValue(context.Background(), holderCtxKey{}, h)
		SetUserID(ctx, uid)
		require.NotNil(t, h.UserID)
		assert.Equal(t, uid, *h.UserID)
	})
}

func TestCovZ_BlockList_AddRemoveHas(t *testing.T) {
	b := &BlockList{ips: map[string]struct{}{}}
	assert.False(t, b.Has("198.51.100.1"))

	b.Add("198.51.100.1")
	assert.True(t, b.Has("198.51.100.1"))
	assert.False(t, b.Has("198.51.100.2"))

	b.Remove("198.51.100.1")
	assert.False(t, b.Has("198.51.100.1"))

	// remover algo ausente nao panica.
	assert.NotPanics(t, func() { b.Remove("198.51.100.99") })
}

func TestCovZ_BlockMiddleware(t *testing.T) {
	list := &BlockList{ips: map[string]struct{}{"192.0.2.10": {}}}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	cases := []struct {
		name       string
		list       *BlockList
		path       string
		remoteAddr string
		wantStatus int
	}{
		{"ip bloqueado em rota comum", list, "/v1/internal/campaigns", "192.0.2.10:1234", http.StatusForbidden},
		{"ip livre em rota comum", list, "/v1/internal/campaigns", "192.0.2.11:1234", http.StatusNoContent},
		{"ip bloqueado mas rota admin isenta", list, "/v1/internal/admin/monitoring/ips", "192.0.2.10:1234", http.StatusNoContent},
		{"ip bloqueado mas login isento", list, "/v1/internal/auth/login", "192.0.2.10:1234", http.StatusNoContent},
		{"ip bloqueado mas health isento", list, "/v1/internal/health", "192.0.2.10:1234", http.StatusNoContent},
		{"lista nula nao bloqueia", nil, "/v1/internal/campaigns", "192.0.2.10:1234", http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := BlockMiddleware(c.list)(next)
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			req.RemoteAddr = c.remoteAddr
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, c.wantStatus, rec.Code)
			if c.wantStatus == http.StatusForbidden {
				assert.Contains(t, rec.Body.String(), "ip blocked")
			}
		})
	}
}

func TestCovZ_Writer_Submit(t *testing.T) {
	t.Run("writer nulo devolve false", func(t *testing.T) {
		var w *Writer
		assert.False(t, w.Submit(Sample{Route: "/x"}))
	})

	t.Run("canal cheio descarta e conta", func(t *testing.T) {
		w := NewWriter(nil, nil, Config{BufferSize: 2})
		assert.True(t, w.Submit(Sample{Route: "/a"}))
		assert.True(t, w.Submit(Sample{Route: "/b"}))
		assert.False(t, w.Submit(Sample{Route: "/c"}), "terceiro nao cabe no buffer de 2")
		assert.False(t, w.Submit(Sample{Route: "/d"}))
		assert.Equal(t, uint64(2), w.dropped.Load())
		assert.Len(t, w.ch, 2)
	})

	t.Run("buffer zero usa o default de 4096", func(t *testing.T) {
		w := NewWriter(nil, nil, Config{})
		assert.Equal(t, 4096, cap(w.ch))
	})
}

func TestCovZ_Middleware_CapturesSample(t *testing.T) {
	w := NewWriter(nil, nil, Config{BufferSize: 16})
	uid := uuid.New()

	r := chi.NewRouter()
	r.Use(Middleware(w, 1)) // slowMs=1 -> handler que dorme 25ms e "lento"
	r.Get("/v1/things/{id}", func(rw http.ResponseWriter, req *http.Request) {
		SetUserID(req.Context(), uid)
		time.Sleep(25 * time.Millisecond)
		rw.WriteHeader(http.StatusTeapot)
	})
	r.Get("/v1/boom", func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusInternalServerError)
	})
	r.Get("/v1/implicit", func(rw http.ResponseWriter, req *http.Request) {
		_, _ = rw.Write([]byte("ok")) // sem WriteHeader explicito -> 200
	})
	r.Get("/metrics", func(rw http.ResponseWriter, req *http.Request) {})

	do := func(path string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "203.0.113.50:4000"
		r.ServeHTTP(httptest.NewRecorder(), req)
	}

	do("/v1/things/abc-123")
	do("/v1/boom")
	do("/v1/implicit")
	do("/v1/naoexiste")
	do("/metrics") // pulado: nao gera sample

	require.Len(t, w.ch, 4, "/metrics nao deve ser instrumentado")

	byRoute := map[string]Sample{}
	for len(w.ch) > 0 {
		s := <-w.ch
		byRoute[s.Route] = s
	}

	things := byRoute["/v1/things/{id}"]
	assert.Equal(t, http.MethodGet, things.Method)
	assert.Equal(t, http.StatusTeapot, things.StatusCode)
	assert.Equal(t, "203.0.113.50", things.IP)
	assert.True(t, things.IsSlow)
	assert.False(t, things.IsError)
	assert.GreaterOrEqual(t, things.DurationMs, 20)
	require.NotNil(t, things.UserID)
	assert.Equal(t, uid, *things.UserID)
	assert.WithinDuration(t, time.Now().UTC(), things.TS, time.Minute)

	boom := byRoute["/v1/boom"]
	assert.Equal(t, http.StatusInternalServerError, boom.StatusCode)
	assert.True(t, boom.IsError)
	assert.Nil(t, boom.UserID)

	implicit := byRoute["/v1/implicit"]
	assert.Equal(t, http.StatusOK, implicit.StatusCode)
	assert.False(t, implicit.IsError)

	// 404 do chi: sem pattern resolvido, cai no path cru.
	missing, ok := byRoute["/v1/naoexiste"]
	require.True(t, ok, "rota inexistente usa o path cru como route")
	assert.Equal(t, http.StatusNotFound, missing.StatusCode)
}

func TestCovZ_Middleware_SlowMsDefaultAndFastRequest(t *testing.T) {
	w := NewWriter(nil, nil, Config{BufferSize: 4})
	h := Middleware(w, 0)(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodPost, "/v1/fast", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	require.Len(t, w.ch, 1)
	s := <-w.ch
	assert.Equal(t, http.MethodPost, s.Method)
	assert.False(t, s.IsSlow, "slowMs<=0 vira 2000ms; request instantaneo nao e lento")
	assert.Equal(t, http.StatusOK, s.StatusCode)
}

// --- caminhos com banco -----------------------------------------------------

func TestCovZ_BlockList_ReloadFromDB(t *testing.T) {
	ctx, pool := covZNewPool(t)
	const ipA, ipB = "198.51.100.201", "198.51.100.202"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM blocked_ips WHERE ip IN ($1,$2)`, ipA, ipB)
	})
	_, err := pool.Exec(ctx, `INSERT INTO blocked_ips (ip, reason) VALUES ($1,'teste'),($2,'teste')`, ipA, ipB)
	require.NoError(t, err)

	b := NewBlockList(ctx, pool, zap.NewNop())
	assert.True(t, b.Has(ipA))
	assert.True(t, b.Has(ipB))

	// Reload substitui o set inteiro (nao faz merge).
	b.Add("198.51.100.203")
	_, err = pool.Exec(ctx, `DELETE FROM blocked_ips WHERE ip=$1`, ipB)
	require.NoError(t, err)
	require.NoError(t, b.Reload(ctx))
	assert.True(t, b.Has(ipA))
	assert.False(t, b.Has(ipB), "ip removido da tabela some do set")
	assert.False(t, b.Has("198.51.100.203"), "Add em memoria nao sobrevive ao Reload")
}

func TestCovZ_NewBlockList_LoadFailureIsLoggedAndSafe(t *testing.T) {
	_, pool := covZNewPool(t)
	pool.Close() // pool fechado -> Query falha

	core, logs := observer.New(zap.WarnLevel)
	b := NewBlockList(context.Background(), pool, zap.New(core))
	require.NotNil(t, b)
	assert.False(t, b.Has("198.51.100.201"), "falha de carga deixa o set vazio")
	assert.Equal(t, 1, logs.FilterMessage("reqmetrics: initial block-list load failed").Len())
}

func TestCovZ_BlockList_RunStopsOnCancel(t *testing.T) {
	b := &BlockList{ips: map[string]struct{}{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("BlockList.Run nao terminou apos cancelar o contexto")
	}
}

func TestCovZ_Writer_RunFlushesBatchToDB(t *testing.T) {
	ctx, pool := covZNewPool(t)
	const route = "/covz/flush-batch"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM system_metrics WHERE route=$1`, route)
	})

	w := NewWriter(pool, zap.NewNop(), Config{BufferSize: 16})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		w.Run(runCtx, Config{BatchSize: 2, FlushEvery: time.Hour})
		close(done)
	}()

	uid := uuid.New()
	now := time.Now().UTC()
	// 2 samples = BatchSize -> flush imediato; o 3o fica pendente ate o cancel.
	require.True(t, w.Submit(Sample{TS: now, Route: route, Method: "GET", StatusCode: 200, DurationMs: 12, IP: "203.0.113.9", UserID: &uid}))
	require.True(t, w.Submit(Sample{TS: now, Route: route, Method: "POST", StatusCode: 500, DurationMs: 3000, IP: "203.0.113.9", UserEmail: "ops@example.com", IsError: true, IsSlow: true}))

	require.Eventually(t, func() bool {
		return covZCountMetrics(t, ctx, pool, route) == 2
	}, 10*time.Second, 50*time.Millisecond, "batch cheio deveria ser gravado")

	require.True(t, w.Submit(Sample{TS: now, Route: route, Method: "GET", StatusCode: 200, DurationMs: 1, IP: "203.0.113.9"}))
	// Garante que o 3o ja foi consumido do canal antes de cancelar, para que o
	// flush do shutdown o grave.
	require.Eventually(t, func() bool { return len(w.ch) == 0 }, 10*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Writer.Run nao terminou apos cancelar")
	}
	assert.Equal(t, 3, covZCountMetrics(t, ctx, pool, route), "cancelamento drena o batch pendente")

	// Confere o mapeamento de colunas.
	var (
		gotUID   *uuid.UUID
		gotEmail *string
		isErr    bool
		isSlow   bool
		status   int
	)
	err := pool.QueryRow(ctx, `SELECT user_id, user_email, is_error, is_slow, status_code FROM system_metrics WHERE route=$1 AND method='POST'`, route).
		Scan(&gotUID, &gotEmail, &isErr, &isSlow, &status)
	require.NoError(t, err)
	assert.Nil(t, gotUID)
	require.NotNil(t, gotEmail)
	assert.Equal(t, "ops@example.com", *gotEmail)
	assert.True(t, isErr)
	assert.True(t, isSlow)
	assert.Equal(t, 500, status)

	err = pool.QueryRow(ctx, `SELECT user_id, user_email FROM system_metrics WHERE route=$1 AND method='GET' AND duration_ms=12`, route).Scan(&gotUID, &gotEmail)
	require.NoError(t, err)
	require.NotNil(t, gotUID)
	assert.Equal(t, uid, *gotUID)
	assert.Nil(t, gotEmail, "email vazio vira NULL")
}

func TestCovZ_Writer_RunFlushTickerWritesSmallBatch(t *testing.T) {
	ctx, pool := covZNewPool(t)
	const route = "/covz/flush-ticker"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM system_metrics WHERE route=$1`, route)
	})

	w := NewWriter(pool, zap.NewNop(), Config{BufferSize: 8})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go w.Run(runCtx, Config{BatchSize: 100, FlushEvery: 30 * time.Millisecond})

	require.True(t, w.Submit(Sample{TS: time.Now().UTC(), Route: route, Method: "GET", StatusCode: 200, DurationMs: 5, IP: "203.0.113.10"}))
	require.Eventually(t, func() bool {
		return covZCountMetrics(t, ctx, pool, route) == 1
	}, 10*time.Second, 50*time.Millisecond, "o ticker deve gravar mesmo com batch < BatchSize")
}

func TestCovZ_Writer_RunLogsFlushFailure(t *testing.T) {
	_, pool := covZNewPool(t)
	pool.Close() // todo flush vai falhar

	core, logs := observer.New(zap.WarnLevel)
	w := NewWriter(pool, zap.New(core), Config{BufferSize: 4})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx, Config{BatchSize: 1, FlushEvery: time.Hour})
		close(done)
	}()
	require.True(t, w.Submit(Sample{TS: time.Now().UTC(), Route: "/covz/fail", Method: "GET", StatusCode: 200}))
	require.Eventually(t, func() bool {
		return logs.FilterMessage("reqmetrics: flush failed").Len() >= 1
	}, 10*time.Second, 20*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Writer.Run nao terminou")
	}
}

func TestCovZ_Writer_PruneRemovesOldRows(t *testing.T) {
	ctx, pool := covZNewPool(t)
	const route = "/covz/prune"
	const page = "/covz-prune-page"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM system_metrics WHERE route=$1`, route)
		_, _ = pool.Exec(context.Background(), `DELETE FROM web_vitals WHERE page=$1`, page)
	})

	old := time.Now().Add(-48 * time.Hour)
	fresh := time.Now()
	for _, ts := range []time.Time{old, old, fresh} {
		_, err := pool.Exec(ctx, `INSERT INTO system_metrics (ts, route, method, status_code, duration_ms) VALUES ($1,$2,'GET',200,1)`, ts, route)
		require.NoError(t, err)
	}
	for _, ts := range []time.Time{old, fresh} {
		_, err := pool.Exec(ctx, `INSERT INTO web_vitals (ts, name, value, rating, page) VALUES ($1,'LCP',1200,'good',$2)`, ts, page)
		require.NoError(t, err)
	}

	w := NewWriter(pool, zap.NewNop(), Config{})
	w.prune(ctx, 24*time.Hour)

	assert.Equal(t, 1, covZCountMetrics(t, ctx, pool, route), "so a linha recente sobrevive")
	var vitals int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM web_vitals WHERE page=$1`, page).Scan(&vitals))
	assert.Equal(t, 1, vitals, "web_vitals tambem respeita a retencao")
}

func TestCovZ_Writer_PruneLogsFailure(t *testing.T) {
	_, pool := covZNewPool(t)
	pool.Close()

	core, logs := observer.New(zap.WarnLevel)
	w := NewWriter(pool, zap.New(core), Config{})
	w.prune(context.Background(), time.Hour)
	assert.Equal(t, 1, logs.FilterMessage("reqmetrics: prune failed").Len())
}

func TestCovZ_Writer_RunPruneTicker(t *testing.T) {
	ctx, pool := covZNewPool(t)
	const route = "/covz/prune-ticker"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM system_metrics WHERE route=$1`, route)
	})
	_, err := pool.Exec(ctx, `INSERT INTO system_metrics (ts, route, method, status_code, duration_ms) VALUES ($1,$2,'GET',200,1)`,
		time.Now().Add(-72*time.Hour), route)
	require.NoError(t, err)

	w := NewWriter(pool, zap.NewNop(), Config{})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go w.Run(runCtx, Config{PruneEvery: 40 * time.Millisecond, Retention: 24 * time.Hour, FlushEvery: time.Hour})

	require.Eventually(t, func() bool {
		return covZCountMetrics(t, ctx, pool, route) == 0
	}, 10*time.Second, 50*time.Millisecond, "o prune periodico remove a linha vencida")
}
