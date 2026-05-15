package handlers

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"radiocheck/internal/supervisor"
)

// SystemHealthHandler powers the admin overview dashboard: a single endpoint
// that returns the health of every component the system depends on, plus an
// "attention" list of currently-broken things with classified reasons.
//
// Designed to be the operator's first stop when something feels off. Pings
// each dependency in parallel and aggregates results, so the response stays
// under ~2s even when one service is timing out.
//
// Auth: admin-only (mounted under the admin group in router.go).
type SystemHealthHandler struct {
	DB   *pgxpool.Pool
	NATS *nats.Conn
	Sup  interface {
		WorkerStatuses() []supervisor.WorkerStatus
	}

	// Endpoints to probe. Empty string ⇒ marked as "disabled" in the response.
	RedisURL      string // e.g. redis://redis:6379/0 or host:6379
	S3Endpoint    string // e.g. http://minio:9000 (required by config)
	ClapURL       string // CLAP_VERIFIER_URL env
	PrometheusURL string // PROMETHEUS_URL env (optional)
	GrafanaURL    string // GRAFANA_URL env (optional)
	JaegerURL     string // JAEGER_URL env (optional)

	Log *zap.Logger

	httpClient *http.Client
}

// probeTimeout caps any individual dependency check. Picked low enough that a
// dead service can't pin the whole endpoint, high enough that a sluggish but
// alive service still answers.
const probeTimeout = 1500 * time.Millisecond

// stallThreshold mirrors WorkerStatus.StallRisk (30s without PCM). Pulled out
// as a constant so the attention classifier and the supervisor agree on the
// definition.
const stallThreshold = 30 * time.Second

// ServiceStatus is the shape returned for every infra dependency. detail is
// human-readable (operator-facing) — we deliberately avoid leaking raw Go
// errors to the frontend so the UI can render them as-is.
type ServiceStatus struct {
	Status    string `json:"status"`               // "ok" | "down" | "disabled"
	Detail    string `json:"detail,omitempty"`     // why it's down (or what version, etc.)
	LatencyMs int64  `json:"latency_ms,omitempty"` // probe round-trip
}

// AttentionItem is one row in the "needs your attention" list. The frontend
// renders these as a prioritized list with action shortcuts. Severity drives
// color; kind drives icon and grouping.
type AttentionItem struct {
	Kind         string     `json:"kind"`           // worker_offline | infra_down | stream_outage | data_pipeline
	Severity     string     `json:"severity"`       // warning | critical
	Title        string     `json:"title"`          // short summary
	Detail       string     `json:"detail"`         // one-line explanation including classified reason
	Reason       string     `json:"reason,omitempty"` // machine-readable enum for tooling
	Since        *time.Time `json:"since,omitempty"`
	StationID    string     `json:"station_id,omitempty"`
	StationName  string     `json:"station_name,omitempty"`
	ActionURL    string     `json:"action_url,omitempty"`
	ActionLabel  string     `json:"action_label,omitempty"`
}

type WorkersHealth struct {
	ExpectedActive int `json:"expected_active"` // stations with monitoring_status='active'
	Running        int `json:"running"`         // workers in supervisor + producing PCM
	Stalled        int `json:"stalled"`         // workers in supervisor, no PCM > stallThreshold
	Missing        int `json:"missing"`         // station active but no worker registered (reconciler drift)
}

type StreamsHealth struct {
	ExpectedActive int `json:"expected_active"`
	LiveNow        int `json:"live_now"`
	DownNow        int `json:"down_now"`
	Incidents24h   int `json:"incidents_24h"`
}

type DataPipelineHealth struct {
	LastDetectionAt   *time.Time `json:"last_detection_at,omitempty"`
	Detections1h      int        `json:"detections_1h"`
	WebhooksPending   int        `json:"webhooks_pending"`
	WebhooksFailed24h int        `json:"webhooks_failed_24h"`
	LastBackupAt      *time.Time `json:"last_backup_at,omitempty"`
}

type SystemHealth struct {
	GeneratedAt    time.Time                `json:"generated_at"`
	Overall        string                   `json:"overall"` // healthy | degraded | critical
	Infrastructure map[string]ServiceStatus `json:"infrastructure"`
	Observability  map[string]ServiceStatus `json:"observability"`
	Workers        WorkersHealth            `json:"workers"`
	Streams        StreamsHealth            `json:"streams"`
	DataPipeline   DataPipelineHealth       `json:"data_pipeline"`
	Attention      []AttentionItem          `json:"attention"`
}

// Get returns the full system health snapshot. Probes run concurrently; total
// response time ≈ slowest single probe (capped by probeTimeout).
func (h *SystemHealthHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.httpClient == nil {
		h.httpClient = &http.Client{Timeout: probeTimeout}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	out := SystemHealth{
		GeneratedAt:    time.Now().UTC(),
		Infrastructure: map[string]ServiceStatus{},
		Observability:  map[string]ServiceStatus{},
	}

	// Parallel infra probes. Each writes to its own slot in a sync.Map then we
	// drain it at the end — avoids a mutex per probe.
	var (
		wg     sync.WaitGroup
		muInfr sync.Mutex
		muObs  sync.Mutex
	)

	setInfra := func(name string, s ServiceStatus) {
		muInfr.Lock()
		out.Infrastructure[name] = s
		muInfr.Unlock()
	}
	setObs := func(name string, s ServiceStatus) {
		muObs.Lock()
		out.Observability[name] = s
		muObs.Unlock()
	}

	// Infra: postgres, nats, redis, minio, clap.
	wg.Add(5)
	go func() { defer wg.Done(); setInfra("postgres", h.probePostgres(ctx)) }()
	go func() { defer wg.Done(); setInfra("nats", h.probeNATS()) }()
	go func() { defer wg.Done(); setInfra("redis", h.probeRedis()) }()
	go func() { defer wg.Done(); setInfra("minio", h.probeS3()) }()
	go func() { defer wg.Done(); setInfra("clap_verifier", h.probeHTTP(h.ClapURL, "/health")) }()

	// Observability: prometheus, grafana, jaeger (all optional).
	wg.Add(3)
	go func() { defer wg.Done(); setObs("prometheus", h.probeHTTP(h.PrometheusURL, "/-/ready")) }()
	go func() { defer wg.Done(); setObs("grafana", h.probeHTTP(h.GrafanaURL, "/api/health")) }()
	go func() { defer wg.Done(); setObs("jaeger", h.probeHTTP(h.JaegerURL, "/")) }()

	wg.Wait()

	// Workers + streams summary + attention list need DB queries, so they run
	// sequentially after the infra probes (they're fast: ~5ms each).
	out.Workers, out.Streams, out.Attention = h.summarizeRuntime(ctx)
	out.DataPipeline = h.summarizeDataPipeline(ctx)

	// Roll up overall status. The rules are conservative: any core infra
	// (postgres/nats/minio) down ⇒ critical; any worker stalled/missing or
	// stream currently down for an active station ⇒ degraded; otherwise
	// healthy. Observability (prometheus/grafana/jaeger) being down is
	// noted but doesn't escalate severity — losing monitoring isn't the
	// same as losing the system.
	out.Overall = rollupOverall(&out)

	writeJSON(w, http.StatusOK, out)
}

// rollupOverall implements the severity escalation rules. Extracted so it can
// be unit-tested without spinning up a Postgres pool.
func rollupOverall(h *SystemHealth) string {
	for _, name := range []string{"postgres", "nats", "minio"} {
		if s, ok := h.Infrastructure[name]; ok && s.Status == "down" {
			return "critical"
		}
	}
	if h.Workers.Missing > 0 || h.Workers.Stalled > 0 || h.Streams.DownNow > 0 {
		return "degraded"
	}
	// Redis/CLAP down is degraded — they're optional but used.
	for _, name := range []string{"redis", "clap_verifier"} {
		if s, ok := h.Infrastructure[name]; ok && s.Status == "down" {
			return "degraded"
		}
	}
	return "healthy"
}

// ─── Probes ──────────────────────────────────────────────────────────────

func (h *SystemHealthHandler) probePostgres(ctx context.Context) ServiceStatus {
	start := time.Now()
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if err := h.DB.Ping(probeCtx); err != nil {
		return ServiceStatus{Status: "down", Detail: "ping failed", LatencyMs: time.Since(start).Milliseconds()}
	}
	return ServiceStatus{Status: "ok", LatencyMs: time.Since(start).Milliseconds()}
}

func (h *SystemHealthHandler) probeNATS() ServiceStatus {
	if h.NATS == nil {
		return ServiceStatus{Status: "down", Detail: "not configured"}
	}
	if !h.NATS.IsConnected() {
		return ServiceStatus{Status: "down", Detail: "disconnected"}
	}
	return ServiceStatus{Status: "ok"}
}

// probeRedis does a plain TCP dial. We don't need to issue a PING — a closed
// port means the daemon is dead, and an open port means something is
// listening; the API itself uses redis-client under the hood and will surface
// auth errors via its own code paths.
func (h *SystemHealthHandler) probeRedis() ServiceStatus {
	if h.RedisURL == "" {
		return ServiceStatus{Status: "disabled", Detail: "REDIS_URL not set"}
	}
	addr := stripURLToHostPort(h.RedisURL, 6379)
	return tcpProbe(addr)
}

// probeS3 also TCP-dials. MinIO has a /minio/health/live endpoint but the
// system also supports R2/AWS where that path doesn't exist; a TCP reach is
// the lowest-common-denominator signal that the bucket is at least
// addressable.
func (h *SystemHealthHandler) probeS3() ServiceStatus {
	if h.S3Endpoint == "" {
		return ServiceStatus{Status: "disabled", Detail: "S3_ENDPOINT not set"}
	}
	addr := stripURLToHostPort(h.S3Endpoint, 9000)
	return tcpProbe(addr)
}

// probeHTTP issues a GET against base+path and treats any 2xx/3xx/4xx as
// "reachable" (= ok). Only timeouts, connection refused, and 5xx count as
// down. The 4xx allowance covers cases like Jaeger's UI returning 404 on /
// when there's no traceID — the daemon is up, just the response wasn't a
// homepage.
func (h *SystemHealthHandler) probeHTTP(base, path string) ServiceStatus {
	if base == "" {
		return ServiceStatus{Status: "disabled", Detail: "endpoint not set"}
	}
	start := time.Now()
	full := strings.TrimRight(base, "/") + path
	req, err := http.NewRequest(http.MethodGet, full, nil)
	if err != nil {
		return ServiceStatus{Status: "down", Detail: "invalid url"}
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return ServiceStatus{Status: "down", Detail: "unreachable", LatencyMs: time.Since(start).Milliseconds()}
	}
	defer resp.Body.Close()
	latency := time.Since(start).Milliseconds()
	if resp.StatusCode >= 500 {
		return ServiceStatus{Status: "down", Detail: "5xx response", LatencyMs: latency}
	}
	return ServiceStatus{Status: "ok", LatencyMs: latency}
}

// stripURLToHostPort accepts either a full URL ("redis://host:6379/0") or a
// bare host[:port] and returns "host:port" with the supplied default when no
// port is present. Used by TCP probes.
func stripURLToHostPort(raw string, defaultPort int) string {
	s := strings.TrimSpace(raw)
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return ensurePort(u.Host, defaultPort)
	}
	return ensurePort(s, defaultPort)
}

func ensurePort(hp string, def int) string {
	if _, _, err := net.SplitHostPort(hp); err == nil {
		return hp
	}
	return hp + ":" + itoa(def)
}

// itoa keeps the file dependency-free — strconv would be fine too but we
// avoid the import.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func tcpProbe(addr string) ServiceStatus {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return ServiceStatus{Status: "down", Detail: "unreachable", LatencyMs: time.Since(start).Milliseconds()}
	}
	conn.Close()
	return ServiceStatus{Status: "ok", LatencyMs: time.Since(start).Milliseconds()}
}

// ─── Runtime summary (workers + streams + attention list) ────────────────

// summarizeRuntime correlates three sources of truth — the supervisor's
// in-memory map, the stations table, and stream_health_events — to produce
// the counters at the top of the dashboard and the "attention" list at the
// bottom. The classification rules favor specific reasons over generic ones:
// a worker that's both stalled AND has a recent stream_down event is reported
// as stream_down because that's the actionable root cause.
func (h *SystemHealthHandler) summarizeRuntime(ctx context.Context) (WorkersHealth, StreamsHealth, []AttentionItem) {
	var (
		workers   WorkersHealth
		streams   StreamsHealth
		attention []AttentionItem
	)

	// Active stations from DB.
	type activeStation struct {
		id   uuid.UUID
		name string
		band string
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, name, COALESCE(band, '')
		FROM stations
		WHERE monitoring_status = 'active'
		ORDER BY name
	`)
	if err != nil {
		if h.Log != nil {
			h.Log.Warn("system_health: stations query failed", zap.Error(err))
		}
		return workers, streams, attention
	}
	active := []activeStation{}
	for rows.Next() {
		var s activeStation
		if err := rows.Scan(&s.id, &s.name, &s.band); err != nil {
			continue
		}
		active = append(active, s)
	}
	rows.Close()

	workers.ExpectedActive = len(active)
	streams.ExpectedActive = len(active)

	// Worker map: station_id ⇒ status.
	type wState struct {
		registered bool
		stalled    bool
		lastPCM    time.Time
	}
	wmap := map[uuid.UUID]wState{}
	if h.Sup != nil {
		for _, ws := range h.Sup.WorkerStatuses() {
			id, err := uuid.Parse(ws.StationID)
			if err != nil {
				continue
			}
			st := wState{registered: true, lastPCM: ws.LastPCMAt}
			// Match the supervisor's own definition (>30s without PCM = stall).
			if !ws.LastPCMAt.IsZero() && time.Since(ws.LastPCMAt) > stallThreshold {
				st.stalled = true
			}
			// A worker that's been registered but never produced any PCM is
			// almost certainly stuck on a dead URL — same operator action.
			if ws.LastPCMAt.IsZero() {
				st.stalled = true
			}
			wmap[id] = st
		}
	}

	// Current stream-down set: stations with an open down event (event_at in
	// the last 24h with no matching up event after). The query is small
	// because we only check active stations.
	type downInfo struct {
		since time.Time
	}
	downSet := map[uuid.UUID]downInfo{}
	rows2, err := h.DB.Query(ctx, `
		SELECT station_id, MAX(event_at)
		FROM stream_health_events
		WHERE event_type = 'down'
		  AND event_at > NOW() - INTERVAL '6 hours'
		  AND station_id IN (
			SELECT id FROM stations WHERE monitoring_status = 'active'
		  )
		GROUP BY station_id
	`)
	if err == nil {
		for rows2.Next() {
			var id uuid.UUID
			var since time.Time
			if err := rows2.Scan(&id, &since); err != nil {
				continue
			}
			// We don't have a guaranteed "up" event, so we approximate
			// "currently down" as "last down event in the past 15 minutes".
			// stream_health_events is append-only and the watchdog writes
			// new down events as the outage persists, so a stale event
			// means recovery.
			if time.Since(since) < 15*time.Minute {
				downSet[id] = downInfo{since: since}
			}
		}
		rows2.Close()
	}

	// Incidents 24h: count distinct stations with any down event in 24h.
	if err := h.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM stream_health_events
		WHERE event_type = 'down' AND event_at > NOW() - INTERVAL '24 hours'
	`).Scan(&streams.Incidents24h); err != nil && h.Log != nil {
		h.Log.Warn("system_health: incidents query failed", zap.Error(err))
	}

	// Classify each active station.
	for _, st := range active {
		w, hasWorker := wmap[st.id]
		_, isStreamDown := downSet[st.id]

		switch {
		case !hasWorker:
			workers.Missing++
			streams.DownNow++
			attention = append(attention, AttentionItem{
				Kind:        "worker_offline",
				Severity:    "critical",
				Reason:      "not_registered",
				Title:       st.name,
				Detail:      "Worker não registrado — supervisor não conhece esta emissora ativa (drift do reconciler).",
				StationID:   st.id.String(),
				StationName: st.name,
				ActionURL:   "/operations",
				ActionLabel: "Ver workers",
			})
		case isStreamDown:
			workers.Stalled++ // a stream-down worker can't produce PCM, so counts as stalled too
			streams.DownNow++
			since := downSet[st.id].since
			attention = append(attention, AttentionItem{
				Kind:        "stream_outage",
				Severity:    "warning",
				Reason:      "stream_down",
				Title:       st.name,
				Detail:      "Stream fora do ar — última queda registrada há " + humanizeSince(since) + ".",
				Since:       &since,
				StationID:   st.id.String(),
				StationName: st.name,
				ActionURL:   "/monitoring",
				ActionLabel: "Ver streams",
			})
		case w.stalled:
			workers.Stalled++
			detail := "Worker travado — sem áudio há mais de " + humanizeSince(w.lastPCM) + "."
			if w.lastPCM.IsZero() {
				detail = "Worker nunca recebeu áudio desde o boot — URL morta ou DNS quebrado."
			}
			var since *time.Time
			if !w.lastPCM.IsZero() {
				lp := w.lastPCM
				since = &lp
			}
			attention = append(attention, AttentionItem{
				Kind:        "worker_offline",
				Severity:    "warning",
				Reason:      "stalled",
				Title:       st.name,
				Detail:      detail,
				Since:       since,
				StationID:   st.id.String(),
				StationName: st.name,
				ActionURL:   "/operations",
				ActionLabel: "Ver workers",
			})
		default:
			workers.Running++
			streams.LiveNow++
		}
	}

	return workers, streams, attention
}

func (h *SystemHealthHandler) summarizeDataPipeline(ctx context.Context) DataPipelineHealth {
	out := DataPipelineHealth{}

	// Last detection + count in last hour.
	var lastDet *time.Time
	row := h.DB.QueryRow(ctx, `SELECT MAX(detected_at) FROM detections`)
	if err := row.Scan(&lastDet); err == nil {
		out.LastDetectionAt = lastDet
	}

	if err := h.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM detections WHERE detected_at > NOW() - INTERVAL '1 hour'
	`).Scan(&out.Detections1h); err != nil && h.Log != nil {
		h.Log.Warn("system_health: detections_1h query failed", zap.Error(err))
	}

	// Webhook deliveries: pending now, failed in last 24h. Tables come from
	// migration 0012; we treat absence as zeros so the dashboard still
	// renders on a fresh install where no webhooks have ever fired.
	_ = h.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM webhook_deliveries
		WHERE status IN ('pending','retry')
	`).Scan(&out.WebhooksPending)
	_ = h.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM webhook_deliveries
		WHERE status IN ('failed','dead') AND created_at > NOW() - INTERVAL '24 hours'
	`).Scan(&out.WebhooksFailed24h)

	return out
}

// humanizeSince returns a compact Portuguese duration string suitable for
// dropping into a sentence ("há 4min", "há 2h"). Kept simple — we don't need
// sub-second precision on a health dashboard.
func humanizeSince(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "menos de 1min"
	}
	if d < time.Hour {
		return itoa(int(d.Minutes())) + "min"
	}
	if d < 24*time.Hour {
		return itoa(int(d.Hours())) + "h"
	}
	return itoa(int(d.Hours()/24)) + "d"
}
