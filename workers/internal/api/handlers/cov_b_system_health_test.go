package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/supervisor"
)

func TestCovB_SystemHealth_RollupOverall(t *testing.T) {
	ok := ServiceStatus{Status: "ok"}
	down := ServiceStatus{Status: "down"}
	infra := func(pg, nt, mn, rd, cl ServiceStatus) map[string]ServiceStatus {
		return map[string]ServiceStatus{"postgres": pg, "nats": nt, "minio": mn, "redis": rd, "clap_verifier": cl}
	}
	tests := []struct {
		name string
		in   SystemHealth
		want string
	}{
		{"tudo ok", SystemHealth{Infrastructure: infra(ok, ok, ok, ok, ok)}, "healthy"},
		{"postgres fora é crítico", SystemHealth{Infrastructure: infra(down, ok, ok, ok, ok)}, "critical"},
		{"nats fora é crítico", SystemHealth{Infrastructure: infra(ok, down, ok, ok, ok)}, "critical"},
		{"minio fora é crítico", SystemHealth{Infrastructure: infra(ok, ok, down, ok, ok)}, "critical"},
		{"crítico vence o degradado", SystemHealth{
			Infrastructure: infra(down, ok, ok, ok, ok), Workers: WorkersHealth{Missing: 3}}, "critical"},
		{"worker ausente degrada", SystemHealth{Infrastructure: infra(ok, ok, ok, ok, ok), Workers: WorkersHealth{Missing: 1}}, "degraded"},
		{"worker travado degrada", SystemHealth{Infrastructure: infra(ok, ok, ok, ok, ok), Workers: WorkersHealth{Stalled: 1}}, "degraded"},
		{"stream caído degrada", SystemHealth{Infrastructure: infra(ok, ok, ok, ok, ok), Streams: StreamsHealth{DownNow: 1}}, "degraded"},
		{"redis fora degrada", SystemHealth{Infrastructure: infra(ok, ok, ok, down, ok)}, "degraded"},
		{"clap fora degrada", SystemHealth{Infrastructure: infra(ok, ok, ok, ok, down)}, "degraded"},
		{"observabilidade fora NÃO escala", SystemHealth{
			Infrastructure: infra(ok, ok, ok, ok, ok),
			Observability:  map[string]ServiceStatus{"prometheus": down, "grafana": down, "jaeger": down}}, "healthy"},
		{"desativado (disabled) não conta como fora", SystemHealth{
			Infrastructure: infra(ok, ok, ok, ServiceStatus{Status: "disabled"}, ServiceStatus{Status: "disabled"})}, "healthy"},
		{"mapas vazios", SystemHealth{}, "healthy"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, rollupOverall(&tc.in))
		})
	}
}

func TestCovB_SystemHealth_Helpers(t *testing.T) {
	t.Run("itoa", func(t *testing.T) {
		for in, want := range map[int]string{0: "0", 7: "7", 10: "10", 6379: "6379", 1234567: "1234567"} {
			assert.Equal(t, want, itoa(in))
		}
	})
	t.Run("ensurePort", func(t *testing.T) {
		assert.Equal(t, "host:1234", ensurePort("host:1234", 9))
		assert.Equal(t, "host:9", ensurePort("host", 9))
		assert.Equal(t, "[::1]:80", ensurePort("[::1]:80", 9))
	})
	t.Run("stripURLToHostPort", func(t *testing.T) {
		tests := []struct {
			in   string
			def  int
			want string
		}{
			{"redis://redis:6379/0", 1, "redis:6379"},
			{"redis://redis/0", 6379, "redis:6379"},
			{"http://minio:9000", 1, "minio:9000"},
			{"https://bucket.example.com", 443, "bucket.example.com:443"},
			{"cache:6380", 1, "cache:6380"},
			{"  cache  ", 6379, "cache:6379"},
			{"localhost:6379", 1, "localhost:6379"},
		}
		for _, tc := range tests {
			assert.Equal(t, tc.want, stripURLToHostPort(tc.in, tc.def), tc.in)
		}
	})
	t.Run("humanizeSince", func(t *testing.T) {
		now := time.Now()
		tests := []struct {
			ago  time.Duration
			want string
		}{
			{10 * time.Second, "menos de 1min"},
			{5*time.Minute + 5*time.Second, "5min"},
			{3*time.Hour + time.Minute, "3h"},
			{49 * time.Hour, "2d"},
		}
		for _, tc := range tests {
			assert.Equal(t, tc.want, humanizeSince(now.Add(-tc.ago)))
		}
	})
}

func TestCovB_SystemHealth_Probes(t *testing.T) {
	h := &SystemHealthHandler{httpClient: &http.Client{Timeout: 2 * time.Second}}
	listenAddr := covBListener(t)
	deadAddr := covBDeadAddr(t)

	t.Run("tcpProbe: porta aberta ok, fechada down", func(t *testing.T) {
		assert.Equal(t, "ok", tcpProbe(listenAddr).Status)
		st := tcpProbe(deadAddr)
		assert.Equal(t, "down", st.Status)
		assert.Equal(t, "unreachable", st.Detail)
	})
	t.Run("redis: sem URL desativado; com URL sonda TCP", func(t *testing.T) {
		assert.Equal(t, ServiceStatus{Status: "disabled", Detail: "REDIS_URL not set"}, (&SystemHealthHandler{}).probeRedis())
		assert.Equal(t, "ok", (&SystemHealthHandler{RedisURL: "redis://" + listenAddr + "/0"}).probeRedis().Status)
		assert.Equal(t, "down", (&SystemHealthHandler{RedisURL: deadAddr}).probeRedis().Status)
	})
	t.Run("s3: sem endpoint desativado; com endpoint sonda TCP", func(t *testing.T) {
		assert.Equal(t, "disabled", (&SystemHealthHandler{}).probeS3().Status)
		assert.Equal(t, "ok", (&SystemHealthHandler{S3Endpoint: "http://" + listenAddr}).probeS3().Status)
		assert.Equal(t, "down", (&SystemHealthHandler{S3Endpoint: "http://" + deadAddr}).probeS3().Status)
	})
	t.Run("nats: nil, desconectado e conectado", func(t *testing.T) {
		st := (&SystemHealthHandler{}).probeNATS()
		assert.Equal(t, "down", st.Status)
		assert.Equal(t, "not configured", st.Detail)
		st = (&SystemHealthHandler{NATS: &nats.Conn{}}).probeNATS()
		assert.Equal(t, "down", st.Status)
		assert.Equal(t, "disconnected", st.Detail)
		assert.Equal(t, "ok", (&SystemHealthHandler{NATS: covBNATS(t)}).probeNATS().Status)
	})
	t.Run("http: cada classe de resposta", func(t *testing.T) {
		codes := map[string]int{"/ok": 200, "/redir": 302, "/missing": 404, "/boom": 503}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			code, found := codes[r.URL.Path]
			if !found {
				code = 200
			}
			w.WriteHeader(code)
		}))
		defer srv.Close()
		assert.Equal(t, "disabled", h.probeHTTP("", "/ok").Status)
		assert.Equal(t, "ok", h.probeHTTP(srv.URL+"/", "/ok").Status, "barra final da base é aparada")
		assert.Equal(t, "ok", h.probeHTTP(srv.URL, "/missing").Status, "4xx = daemon vivo")
		boom := h.probeHTTP(srv.URL, "/boom")
		assert.Equal(t, "down", boom.Status)
		assert.Equal(t, "5xx response", boom.Detail)
		unreachable := h.probeHTTP("http://"+deadAddr, "/")
		assert.Equal(t, "down", unreachable.Status)
		assert.Equal(t, "unreachable", unreachable.Detail)
		assert.Equal(t, "invalid url", h.probeHTTP("http://exemplo.invalid\x7f", "/").Detail)
	})
}

// covBSeedHealthStations cria 6 emissoras ativas, uma por classe de falha, e
// devolve os ids por papel.
type covBHealthFixture struct {
	backoff, missing, streamDown, stalledOld, neverPCM, ok uuid.UUID
}

func covBSeedHealth(t *testing.T, s *covBHealthEnv) covBHealthFixture {
	t.Helper()
	mk := func(name string) uuid.UUID {
		id := mcptest.Emissora(t, s.ctx, s.pool, name, 0)
		_, err := s.pool.Exec(s.ctx, `UPDATE stations SET monitoring_status = 'active' WHERE id = $1`, id)
		require.NoError(t, err)
		return id
	}
	f := covBHealthFixture{
		backoff:    mk("Rádio A Backoff FM"),
		missing:    mk("Rádio B Sem Worker FM"),
		streamDown: mk("Rádio C Queda FM"),
		stalledOld: mk("Rádio D Travada FM"),
		neverPCM:   mk("Rádio E Muda FM"),
		ok:         mk("Rádio F Saudável FM"),
	}
	now := time.Now()
	s.sup.ws = []supervisor.WorkerStatus{
		{StationID: f.streamDown.String(), Active: true, LastPCMAt: now},
		{StationID: f.stalledOld.String(), Active: true, LastPCMAt: now.Add(-2 * time.Hour)},
		{StationID: f.neverPCM.String(), Active: true}, // LastPCMAt zero
		{StationID: f.ok.String(), Active: true, LastPCMAt: now},
		{StationID: "id-que-nao-e-uuid", Active: true, LastPCMAt: now}, // ignorado
	}
	s.sup.backoff = map[uuid.UUID]uint32{f.backoff: 7}
	// queda recente (<15min, há 5min) na C; queda velha (1h) na F não conta como "agora".
	mcptest.Queda(t, s.ctx, s.pool, f.streamDown, now.Add(-5*time.Minute), 60)
	mcptest.Queda(t, s.ctx, s.pool, f.ok, now.Add(-time.Hour), 60)
	return f
}

type covBHealthEnv struct {
	ctx  context.Context
	pool *pgxpool.Pool
	sup  *covBSup
	h    *SystemHealthHandler
}

func covBHealthEnvSetup(t *testing.T) *covBHealthEnv {
	t.Helper()
	ctx, pool := covBDB(t)
	sup := &covBSup{}
	return &covBHealthEnv{ctx: ctx, pool: pool, sup: sup, h: &SystemHealthHandler{DB: pool, Sup: sup}}
}

func TestCovB_SystemHealth_SummarizeRuntime(t *testing.T) {
	env := covBHealthEnvSetup(t)

	t.Run("sem emissoras ativas zera tudo", func(t *testing.T) {
		w, s, att := env.h.summarizeRuntime(env.ctx)
		assert.Equal(t, WorkersHealth{}, w)
		assert.Equal(t, StreamsHealth{}, s)
		assert.Empty(t, att)
	})

	f := covBSeedHealth(t, env)
	w, s, att := env.h.summarizeRuntime(env.ctx)

	t.Run("contadores", func(t *testing.T) {
		assert.Equal(t, 6, w.ExpectedActive)
		assert.Equal(t, 1, w.Running, "só a saudável")
		assert.Equal(t, 3, w.Stalled, "queda + travada + nunca recebeu áudio")
		assert.Equal(t, 1, w.Missing)
		assert.Equal(t, 6, s.ExpectedActive)
		assert.Equal(t, 1, s.LiveNow)
		assert.Equal(t, 3, s.DownNow, "backoff + sem worker + queda")
		assert.Equal(t, 2, s.Incidents24h, "as duas quedas das últimas 24h, inclusive a velha")
	})

	byStation := map[string]AttentionItem{}
	for _, it := range att {
		byStation[it.StationID] = it
	}
	t.Run("a emissora saudável não gera atenção", func(t *testing.T) {
		require.Len(t, att, 5)
		assert.NotContains(t, byStation, f.ok.String())
	})
	t.Run("backoff é aviso de stream inalcançável (não drift)", func(t *testing.T) {
		it := byStation[f.backoff.String()]
		assert.Equal(t, "stream_unreachable", it.Kind)
		assert.Equal(t, "warning", it.Severity)
		assert.Equal(t, "connect_backoff", it.Reason)
		assert.Contains(t, it.Detail, "7 tentativas")
		assert.Equal(t, "/operations", it.ActionURL)
	})
	t.Run("sem worker é crítico e aponta o reconciler", func(t *testing.T) {
		it := byStation[f.missing.String()]
		assert.Equal(t, "worker_offline", it.Kind)
		assert.Equal(t, "critical", it.Severity)
		assert.Equal(t, "not_registered", it.Reason)
		assert.Contains(t, it.Detail, "reconciler")
	})
	t.Run("queda recente vira stream_outage com horário", func(t *testing.T) {
		it := byStation[f.streamDown.String()]
		assert.Equal(t, "stream_outage", it.Kind)
		assert.Equal(t, "warning", it.Severity)
		assert.Equal(t, "stream_down", it.Reason)
		require.NotNil(t, it.Since)
		assert.WithinDuration(t, time.Now().Add(-5*time.Minute), *it.Since, 30*time.Second)
		assert.Equal(t, "/monitoring", it.ActionURL)
		assert.Contains(t, it.Detail, "há 5min")
	})
	t.Run("worker travado há 2h", func(t *testing.T) {
		it := byStation[f.stalledOld.String()]
		assert.Equal(t, "stalled", it.Reason)
		assert.Contains(t, it.Detail, "2h")
		assert.NotNil(t, it.Since)
	})
	t.Run("worker que nunca recebeu áudio", func(t *testing.T) {
		it := byStation[f.neverPCM.String()]
		assert.Equal(t, "stalled", it.Reason)
		assert.Contains(t, it.Detail, "nunca recebeu áudio")
		assert.Nil(t, it.Since)
	})
}

func TestCovB_SystemHealth_SummarizeDataPipeline(t *testing.T) {
	env := covBHealthEnvSetup(t)

	t.Run("banco vazio: zeros e sem última detecção", func(t *testing.T) {
		dp := env.h.summarizeDataPipeline(env.ctx)
		assert.Nil(t, dp.LastDetectionAt)
		assert.Equal(t, 0, dp.Detections1h)
		assert.Equal(t, 0, dp.WebhooksPending)
		assert.Equal(t, 0, dp.WebhooksFailed24h)
		assert.Equal(t, 0, dp.AuditRejected7d)
	})

	cli := mcptest.Cliente(t, env.ctx, env.pool, "Cliente Teste")
	est := mcptest.Emissora(t, env.ctx, env.pool, "Rádio Teste FM", 0)
	tipo := mcptest.TipoMaterial(t, env.ctx, env.pool, "Spot")
	mat := mcptest.Material(t, env.ctx, env.pool, cli, tipo, "Spot")
	camp := mcptest.Campanha(t, env.ctx, env.pool, cli, "Campanha", "2026-01-01", "2026-12-31", est)
	recent := mcptest.Veiculacao(t, env.ctx, env.pool, camp, mat, est, "in_slot", time.Now().Add(-10*time.Minute))
	mcptest.Veiculacao(t, env.ctx, env.pool, camp, mat, est, "in_slot", time.Now().Add(-3*time.Hour))
	_, err := env.pool.Exec(env.ctx, `UPDATE detections SET evidence_status = 'audit_rejected' WHERE id = $1`, recent)
	require.NoError(t, err)
	for _, st := range []string{"pending", "failed", "delivered"} {
		_, err = env.pool.Exec(env.ctx,
			`INSERT INTO webhook_deliveries (client_id, event_type, payload, status) VALUES ($1, 'detection.created', '{}', $2)`, cli, st)
		require.NoError(t, err)
	}
	// falha antiga (>24h) não conta
	_, err = env.pool.Exec(env.ctx,
		`INSERT INTO webhook_deliveries (client_id, event_type, payload, status, created_at)
		 VALUES ($1, 'detection.created', '{}', 'dead', now() - interval '3 days')`, cli)
	require.NoError(t, err)

	dp := env.h.summarizeDataPipeline(env.ctx)
	require.NotNil(t, dp.LastDetectionAt)
	assert.WithinDuration(t, time.Now().Add(-10*time.Minute), *dp.LastDetectionAt, time.Minute)
	assert.Equal(t, 1, dp.Detections1h, "só a de 10 minutos atrás")
	assert.Equal(t, 1, dp.WebhooksPending)
	assert.Equal(t, 1, dp.WebhooksFailed24h)
	assert.Equal(t, 1, dp.AuditRejected7d)
}

func TestCovB_SystemHealth_Get(t *testing.T) {
	env := covBHealthEnvSetup(t)

	clap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusTeapot)
	}))
	defer clap.Close()
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer prom.Close()
	jaeger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer jaeger.Close()

	env.h.NATS = covBNATS(t)
	env.h.RedisURL = "redis://" + covBListener(t) + "/0"
	env.h.S3Endpoint = "http://" + covBListener(t)
	env.h.ClapURL = clap.URL
	env.h.PrometheusURL = prom.URL
	env.h.JaegerURL = jaeger.URL // GrafanaURL fica vazio de propósito

	get := func(t *testing.T) SystemHealth {
		t.Helper()
		rec := covBServe(env.h.Get, covBReq("GET", "/admin/system-health", "", nil, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		var out SystemHealth
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out
	}

	t.Run("tudo vivo e sem emissoras: healthy", func(t *testing.T) {
		out := get(t)
		assert.Equal(t, "healthy", out.Overall)
		for _, name := range []string{"postgres", "nats", "redis", "minio", "clap_verifier"} {
			assert.Equal(t, "ok", out.Infrastructure[name].Status, name)
		}
		assert.Equal(t, "down", out.Observability["prometheus"].Status, "503 do Prometheus")
		assert.Equal(t, "disabled", out.Observability["grafana"].Status)
		assert.Equal(t, "ok", out.Observability["jaeger"].Status, "404 = vivo")
		assert.WithinDuration(t, time.Now(), out.GeneratedAt, time.Minute)
		assert.Empty(t, out.Attention)
	})
	t.Run("com emissoras em falha: degraded e lista de atenção", func(t *testing.T) {
		covBSeedHealth(t, env)
		out := get(t)
		assert.Equal(t, "degraded", out.Overall)
		assert.Equal(t, 6, out.Workers.ExpectedActive)
		assert.Len(t, out.Attention, 5)
	})
	t.Run("sem NATS o geral é crítico", func(t *testing.T) {
		env.h.NATS = nil
		out := get(t)
		assert.Equal(t, "critical", out.Overall)
		assert.Equal(t, "down", out.Infrastructure["nats"].Status)
	})
	t.Run("postgres fora (pool fechado): probe down", func(t *testing.T) {
		h := &SystemHealthHandler{DB: covBClosedPool(t)}
		st := h.probePostgres(env.ctx)
		assert.Equal(t, "down", st.Status)
		assert.Equal(t, "ping failed", st.Detail)
		assert.Equal(t, "ok", env.h.probePostgres(env.ctx).Status)
	})
}
