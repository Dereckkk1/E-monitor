package webhook

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/metrics"
)

// Backoff schedule (§13.1.4): 1m, 5m, 15m, 1h, 4h. After 5 attempts the
// delivery is moved to the DLQ (status='dead'). Index zero is the delay
// applied after the FIRST failure (i.e. retry #1 -> wait 1m).
var backoffSchedule = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
	4 * time.Hour,
}

const (
	maxAttempts            = 5
	defaultPollInterval    = 5 * time.Second
	defaultMaxConcurrent   = 10
	defaultHTTPTimeout     = 10 * time.Second
	maxResponseBodyBytes   = 4 * 1024 // 4 KB cap for response_body persistence
	signatureHeaderName    = "X-Radiocheck-Signature"
	eventTypeHeaderName    = "X-Radiocheck-Event"
	userAgentHeader        = "Radiocheck-Webhook/1.0"
)

// Worker drains pending webhook_deliveries rows and POSTs them to the
// configured client URL. It is safe to run multiple workers concurrently in
// the same process — `FOR UPDATE SKIP LOCKED` is used to claim work.
type Worker struct {
	db            *pgxpool.Pool
	httpClient    *http.Client
	log           *zap.Logger
	pollInterval  time.Duration
	maxConcurrent int
}

// NewWorker builds a Worker with sensible defaults.
func NewWorker(db *pgxpool.Pool, log *zap.Logger) *Worker {
	return &Worker{
		db:            db,
		httpClient:    &http.Client{Timeout: defaultHTTPTimeout},
		log:           log,
		pollInterval:  defaultPollInterval,
		maxConcurrent: defaultMaxConcurrent,
	}
}

// WithHTTPClient overrides the HTTP client (used in tests).
func (w *Worker) WithHTTPClient(c *http.Client) *Worker {
	w.httpClient = c
	return w
}

// WithPollInterval overrides the loop interval (used in tests).
func (w *Worker) WithPollInterval(d time.Duration) *Worker {
	w.pollInterval = d
	return w
}

// Run drives the polling loop until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.log.Info("webhook worker: starting",
		zap.Duration("poll_interval", w.pollInterval),
		zap.Int("max_concurrent", w.maxConcurrent),
	)
	t := time.NewTicker(w.pollInterval)
	defer t.Stop()

	// Run once immediately so unit tests don't need to wait the first tick.
	w.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			w.log.Info("webhook worker: stopping")
			return nil
		case <-t.C:
			w.tick(ctx)
		}
	}
}

// tick processes one batch of pending deliveries.
func (w *Worker) tick(ctx context.Context) {
	w.refreshGauges(ctx)

	rows, err := w.claimBatch(ctx, w.maxConcurrent)
	if err != nil {
		w.log.Warn("webhook worker: claim batch failed", zap.Error(err))
		return
	}
	for _, r := range rows {
		w.deliver(ctx, r)
	}
}

// pendingDelivery is the row shape produced by claimBatch.
type pendingDelivery struct {
	ID            uuid.UUID
	ClientID      uuid.UUID
	EventType     string
	Payload       []byte
	AttemptCount  int
	URL           string
	Secret        string
}

// claimBatch grabs up to `limit` pending rows whose backoff has elapsed,
// using FOR UPDATE SKIP LOCKED so multiple workers don't fight over the same
// row. The transaction is committed before HTTP work begins so we don't keep
// row locks open for seconds at a time.
//
// Safety: the rows are only "claimed" in the sense that we read them; we do
// NOT flip status to 'in_progress', because at-least-once delivery is the
// contract and a crash between claim and POST would just leave them pending
// for the next tick.
func (w *Worker) claimBatch(ctx context.Context, limit int) ([]pendingDelivery, error) {
	tx, err := w.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	rs, err := tx.Query(ctx, `
		SELECT wd.id, wd.client_id, wd.event_type, wd.payload, wd.attempt_count,
		       COALESCE(c.webhook_url, ''), COALESCE(c.webhook_secret, '')
		FROM webhook_deliveries wd
		JOIN clients c ON c.id = wd.client_id
		WHERE wd.status = 'pending' AND wd.next_attempt_at <= NOW()
		ORDER BY wd.next_attempt_at ASC
		LIMIT $1
		FOR UPDATE OF wd SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	defer rs.Close()

	var out []pendingDelivery
	for rs.Next() {
		var p pendingDelivery
		if err := rs.Scan(&p.ID, &p.ClientID, &p.EventType, &p.Payload, &p.AttemptCount, &p.URL, &p.Secret); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// deliver attempts to POST a single delivery and records the outcome.
func (w *Worker) deliver(ctx context.Context, d pendingDelivery) {
	if d.URL == "" {
		w.markDead(ctx, d, "client has no webhook_url configured", 0, "")
		return
	}

	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(d.Payload))
	if err != nil {
		w.scheduleRetryOrDead(ctx, d, err.Error(), 0, "")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgentHeader)
	req.Header.Set(signatureHeaderName, "sha256="+signBody(d.Secret, d.Payload))
	req.Header.Set(eventTypeHeaderName, d.EventType)

	resp, err := w.httpClient.Do(req)
	dur := time.Since(start)
	metrics.WebhookDeliveryDuration.Observe(dur.Seconds())

	if err != nil {
		w.scheduleRetryOrDead(ctx, d, err.Error(), 0, "")
		return
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	bodyStr := string(bodyBytes)

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		w.markDelivered(ctx, d, resp.StatusCode, bodyStr)
	case resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500:
		// Retryable.
		w.scheduleRetryOrDead(ctx, d, "non-2xx", resp.StatusCode, bodyStr)
	default:
		// 4xx (except 408/429) — unrecoverable client error. Mark failed.
		w.markFailed(ctx, d, "client error (non-retryable)", resp.StatusCode, bodyStr)
	}
}

func (w *Worker) markDelivered(ctx context.Context, d pendingDelivery, code int, body string) {
	_, err := w.db.Exec(ctx, `
		UPDATE webhook_deliveries
		SET status = 'delivered',
		    attempt_count = attempt_count + 1,
		    response_code = $2,
		    response_body = $3,
		    last_error = NULL,
		    delivered_at = NOW(),
		    updated_at   = NOW()
		WHERE id = $1`, d.ID, code, body)
	if err != nil {
		w.log.Error("webhook worker: mark delivered failed",
			zap.String("delivery_id", d.ID.String()), zap.Error(err))
	}
	metrics.WebhookDeliveriesTotal.WithLabelValues("delivered").Inc()
	w.log.Info("webhook worker: delivered",
		zap.String("delivery_id", d.ID.String()),
		zap.Int("status", code),
	)
}

func (w *Worker) markFailed(ctx context.Context, d pendingDelivery, reason string, code int, body string) {
	_, err := w.db.Exec(ctx, `
		UPDATE webhook_deliveries
		SET status = 'failed',
		    attempt_count = attempt_count + 1,
		    response_code = $2,
		    response_body = $3,
		    last_error = $4,
		    updated_at = NOW()
		WHERE id = $1`, d.ID, code, body, reason)
	if err != nil {
		w.log.Error("webhook worker: mark failed failed",
			zap.String("delivery_id", d.ID.String()), zap.Error(err))
	}
	metrics.WebhookDeliveriesTotal.WithLabelValues("failed").Inc()
	w.log.Warn("webhook worker: failed (non-retryable)",
		zap.String("delivery_id", d.ID.String()),
		zap.Int("status", code),
		zap.String("reason", reason),
	)
}

func (w *Worker) markDead(ctx context.Context, d pendingDelivery, reason string, code int, body string) {
	_, err := w.db.Exec(ctx, `
		UPDATE webhook_deliveries
		SET status = 'dead',
		    attempt_count = attempt_count + 1,
		    response_code = NULLIF($2, 0),
		    response_body = NULLIF($3, ''),
		    last_error = $4,
		    updated_at = NOW()
		WHERE id = $1`, d.ID, code, body, reason)
	if err != nil {
		w.log.Error("webhook worker: mark dead failed",
			zap.String("delivery_id", d.ID.String()), zap.Error(err))
	}
	metrics.WebhookDeliveriesTotal.WithLabelValues("dead").Inc()
	w.log.Error("webhook worker: dead-letter",
		zap.String("delivery_id", d.ID.String()),
		zap.String("reason", reason),
	)
}

func (w *Worker) scheduleRetryOrDead(ctx context.Context, d pendingDelivery, reason string, code int, body string) {
	nextAttempt := d.AttemptCount + 1
	if nextAttempt >= maxAttempts {
		w.markDead(ctx, d, reason, code, body)
		return
	}
	delay := backoffSchedule[nextAttempt-1] // attempt 1 (just failed) -> wait 1m, etc.
	_, err := w.db.Exec(ctx, `
		UPDATE webhook_deliveries
		SET status = 'pending',
		    attempt_count = $2,
		    next_attempt_at = NOW() + $3::interval,
		    response_code = NULLIF($4, 0),
		    response_body = NULLIF($5, ''),
		    last_error = $6,
		    updated_at = NOW()
		WHERE id = $1`,
		d.ID, nextAttempt, delay.String(), code, body, reason,
	)
	if err != nil {
		w.log.Error("webhook worker: schedule retry failed",
			zap.String("delivery_id", d.ID.String()), zap.Error(err))
		return
	}
	metrics.WebhookDeliveriesTotal.WithLabelValues("retry").Inc()
	w.log.Info("webhook worker: retry scheduled",
		zap.String("delivery_id", d.ID.String()),
		zap.Int("attempt", nextAttempt),
		zap.Duration("delay", delay),
		zap.String("reason", reason),
	)
}

// refreshGauges keeps the queue/DLQ size gauges in sync. Cheap COUNT queries
// against an indexed status column.
func (w *Worker) refreshGauges(ctx context.Context) {
	var pending, dead int
	if err := w.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM webhook_deliveries WHERE status = 'pending'`,
	).Scan(&pending); err != nil && !errors.Is(err, context.Canceled) {
		w.log.Debug("webhook worker: queue gauge query failed", zap.Error(err))
	} else {
		metrics.WebhookQueueSize.Set(float64(pending))
	}
	if err := w.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM webhook_deliveries WHERE status = 'dead'`,
	).Scan(&dead); err != nil && !errors.Is(err, context.Canceled) {
		w.log.Debug("webhook worker: dlq gauge query failed", zap.Error(err))
	} else {
		metrics.WebhookDLQSize.Set(float64(dead))
	}
}
