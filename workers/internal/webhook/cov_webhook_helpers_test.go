package webhook

// Infra de teste do pacote webhook (cobertura TCC), tudo prefixado covW:
//   - covWNATS: servidor NATS mínimo em TCP local (nats.go real assina/publica);
//   - covWDB / covWSeed*: banco de teste + cliente/campanha/commercial sintéticos;
//   - covWReceiver: endpoint HTTP (httptest) que registra as entregas recebidas.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/db"
)

// ───────────────────────── NATS falso ─────────────────────────

type covWNATS struct {
	ln   net.Listener
	mu   sync.Mutex
	subs map[string][]covWSub // subject -> assinantes
}

type covWSub struct {
	conn net.Conn
	sid  string
}

func covWNewNATS(t *testing.T) *covWNATS {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &covWNATS{ln: ln, subs: map[string][]covWSub{}}
	go s.accept()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *covWNATS) URL() string { return "nats://" + s.ln.Addr().String() }

func (s *covWNATS) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.serve(c)
	}
}

func (s *covWNATS) serve(c net.Conn) {
	defer c.Close()
	_, _ = fmt.Fprintf(c, "INFO {\"server_id\":\"covw\",\"version\":\"2.10.0\",\"proto\":1,\"headers\":true,\"max_payload\":1048576}\r\n")
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 0 {
			continue
		}
		switch strings.ToUpper(f[0]) {
		case "PING":
			s.write(c, "PONG\r\n")
		case "SUB":
			// SUB subject [queue] sid
			subj, sid := f[1], f[len(f)-1]
			s.mu.Lock()
			s.subs[subj] = append(s.subs[subj], covWSub{conn: c, sid: sid})
			s.mu.Unlock()
		case "UNSUB":
			sid := f[1]
			s.mu.Lock()
			for subj, l := range s.subs {
				var keep []covWSub
				for _, x := range l {
					if !(x.conn == c && x.sid == sid) {
						keep = append(keep, x)
					}
				}
				s.subs[subj] = keep
			}
			s.mu.Unlock()
		case "PUB":
			// PUB subject [reply] size
			n, _ := strconv.Atoi(f[len(f)-1])
			buf := make([]byte, n+2)
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			s.deliver(f[1], buf[:n], 0)
		case "HPUB":
			// HPUB subject [reply] hdrLen totalLen
			hl, _ := strconv.Atoi(f[len(f)-2])
			tl, _ := strconv.Atoi(f[len(f)-1])
			buf := make([]byte, tl+2)
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			s.deliver(f[1], buf[:tl], hl)
		}
	}
}

func (s *covWNATS) write(c net.Conn, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = c.Write([]byte(msg))
}

func (s *covWNATS) deliver(subject string, payload []byte, hdrLen int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sub := range s.subs[subject] {
		var out []byte
		if hdrLen > 0 {
			out = append(out, []byte(fmt.Sprintf("HMSG %s %s %d %d\r\n", subject, sub.sid, hdrLen, len(payload)))...)
		} else {
			out = append(out, []byte(fmt.Sprintf("MSG %s %s %d\r\n", subject, sub.sid, len(payload)))...)
		}
		out = append(out, payload...)
		out = append(out, '\r', '\n')
		_, _ = sub.conn.Write(out)
	}
}

// covWConnect abre uma conexão NATS real contra o servidor falso.
func covWConnect(t *testing.T, s *covWNATS) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(s.URL(), nats.NoReconnect(), nats.Timeout(5*time.Second))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	return nc
}

// ───────────────────────── banco de teste ─────────────────────────

func covWDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return ctx, pool
}

// covWSeedClient cria um cliente. url/secret vazios = sem webhook configurado.
func covWSeedClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool, url, secret string, enabled bool, events []string) uuid.UUID {
	t.Helper()
	var u, s any
	if url != "" {
		u = url
	}
	if secret != "" {
		s = secret
	}
	if events == nil {
		events = []string{"detection.confirmed"}
	}
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO clients (name, webhook_url, webhook_secret, webhook_enabled, webhook_events)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		"Cliente Teste "+uuid.NewString()[:8], u, s, enabled, events).Scan(&id))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM webhook_deliveries WHERE client_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id)
	})
	return id
}

// covWSeedDelivery insere uma linha da outbox direto (sem passar pelo Enqueue).
func covWSeedDelivery(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID uuid.UUID,
	eventType string, payload string, status string, attempts int, nextAttemptIn time.Duration) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO webhook_deliveries (client_id, event_type, payload, status, attempt_count, next_attempt_at)
		VALUES ($1, $2, $3::jsonb, $4, $5, NOW() + $6::interval) RETURNING id`,
		clientID, eventType, payload, status, attempts, fmt.Sprintf("%d milliseconds", nextAttemptIn.Milliseconds())).Scan(&id))
	return id
}

type covWDeliveryRow struct {
	Status       string
	Attempts     int
	NextAttempt  time.Time
	ResponseCode *int
	ResponseBody *string
	LastError    *string
	DeliveredAt  *time.Time
	DBNow        time.Time
}

func covWReadDelivery(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) covWDeliveryRow {
	t.Helper()
	var r covWDeliveryRow
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT status, attempt_count, next_attempt_at, response_code, response_body, last_error, delivered_at, NOW()
		FROM webhook_deliveries WHERE id = $1`, id).
		Scan(&r.Status, &r.Attempts, &r.NextAttempt, &r.ResponseCode, &r.ResponseBody, &r.LastError, &r.DeliveredAt, &r.DBNow))
	return r
}

func covWCountDeliveries(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID uuid.UUID, eventType string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM webhook_deliveries WHERE client_id = $1 AND event_type = $2`, clientID, eventType).Scan(&n))
	return n
}

// covWSeedStation cria uma emissora de teste.
func covWSeedStation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO stations (name, band, stream_url) VALUES ($1, 'FM', 'http://stream.example.com/a')
		RETURNING id`, name).Scan(&id))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, id) })
	return id
}

// covWSeedCampaign cria uma campanha do cliente.
func covWSeedCampaign(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID uuid.UUID,
	status, start, end string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'Campanha Teste', $2::date, $3::date, $4) RETURNING id`,
		clientID, start, end, status).Scan(&id))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, id) })
	return id
}

// covWSeedCommercial cria um commercial legado pronto na campanha. Devolve (id, short_id).
func covWSeedCommercial(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campaignID uuid.UUID, title string) (uuid.UUID, int32) {
	t.Helper()
	var id uuid.UUID
	var short int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO commercials (campaign_id, title, duration_seconds, master_storage_path,
		                         master_sha256, fingerprint_status)
		VALUES ($1, $2, 30, '/tmp/a.mp3', $3, 'ready') RETURNING id, short_id`,
		campaignID, title, "covw-"+uuid.NewString()).Scan(&id, &short))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, id) })
	return id, short
}

// covWSeedMaterialLink cria um material pronto do cliente e o vincula à campanha
// para a emissora informada. Devolve (material_id, short_id).
func covWSeedMaterialLink(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID, campaignID, stationID uuid.UUID,
	title string) (uuid.UUID, int32) {
	t.Helper()
	var id uuid.UUID
	var short int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO materials (client_id, title, duration_seconds, master_storage_path,
		                       master_sha256, fingerprint_status)
		VALUES ($1, $2, 30, '/tmp/m.mp3', $3, 'ready') RETURNING id, short_id`,
		clientID, title, "covw-"+uuid.NewString()).Scan(&id, &short))
	_, err := pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, ARRAY[$3]::uuid[])`, campaignID, id, stationID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM campaign_materials WHERE material_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, id)
	})
	return id, short
}

// ───────────────────────── receptor HTTP ─────────────────────────

// covWHit é uma requisição recebida pelo covWReceiver.
type covWHit struct {
	Method  string
	Header  http.Header
	Body    []byte
	Arrived time.Time
}

// covWReceiver sobe um httptest.Server que registra cada requisição e responde
// com o status/corpo configurados (alteráveis durante o teste).
type covWReceiver struct {
	Srv *httptest.Server

	mu     sync.Mutex
	hits   []covWHit
	status int
	body   string
}

func covWNewReceiver(t *testing.T, status int, body string) *covWReceiver {
	t.Helper()
	r := &covWReceiver{status: status, body: body}
	r.Srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.hits = append(r.hits, covWHit{Method: req.Method, Header: req.Header.Clone(), Body: b, Arrived: time.Now()})
		st, bd := r.status, r.body
		r.mu.Unlock()
		w.WriteHeader(st)
		_, _ = w.Write([]byte(bd))
	}))
	t.Cleanup(r.Srv.Close)
	return r
}

func (r *covWReceiver) URL() string { return r.Srv.URL }

func (r *covWReceiver) Hits() []covWHit {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]covWHit(nil), r.hits...)
}

func (r *covWReceiver) Set(status int, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status, r.body = status, body
}

// covWWorker monta um Worker que enxerga o httptest (loopback) — o cliente SSRF
// padrão bloquearia 127.0.0.1.
func covWWorker(pool *pgxpool.Pool) *Worker {
	return NewWorker(pool, zap.NewNop()).WithHTTPClient(&http.Client{Timeout: 5 * time.Second})
}

// covWClosedURL devolve uma URL local em que ninguém escuta (conexão recusada).
func covWClosedURL(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.NotFoundHandler())
	u := s.URL
	s.Close()
	return u
}

// covWOutbox devolve um Outbox real sobre o pool.
func covWOutbox(pool *pgxpool.Pool) *Outbox {
	return NewOutbox(pool, catalog.NewClients(pool), zap.NewNop())
}

// covWMetricValue lê o valor atual de um Counter/Gauge do Prometheus.
func covWMetricValue(t *testing.T, m interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	var d dto.Metric
	require.NoError(t, m.Write(&d))
	if d.Counter != nil {
		return d.Counter.GetValue()
	}
	if d.Gauge != nil {
		return d.Gauge.GetValue()
	}
	t.Fatalf("métrica sem counter/gauge")
	return 0
}

// covWEventually espera cond virar verdadeira com margem larga.
func covWEventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	require.Eventually(t, cond, 20*time.Second, 25*time.Millisecond, msg)
}
