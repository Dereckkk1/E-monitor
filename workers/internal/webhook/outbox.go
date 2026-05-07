// Package webhook implements outbound webhook delivery (§13.1.4).
//
// The system follows the outbox pattern:
//
//  1. When a domain event occurs (today: detection confirmed) the publisher
//     calls Outbox.Enqueue, which inserts a row in webhook_deliveries with
//     status='pending'. This decouples event production from HTTP delivery.
//
//  2. A background Worker (worker.go) polls webhook_deliveries, sends pending
//     rows over HTTP with HMAC-SHA256 signature, and either marks them
//     delivered, schedules a retry, or moves them to the DLQ (status='dead').
//
// HMAC: every request carries `X-Radiocheck-Signature: sha256=<hex>` computed
// over the raw JSON body using the client's webhook_secret.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/metrics"
)

// EventType is the discriminator placed in the JSON payload's `type` field
// and persisted in webhook_deliveries.event_type.
type EventType string

const (
	EventDetectionConfirmed EventType = "detection.confirmed"
	EventDetectionRetracted EventType = "detection.retracted"
	EventWebhookTest        EventType = "webhook.test"
)

// Event is the in-memory representation handed to the Outbox by upstream
// publishers (supervisor / API test endpoint). The enqueuer is responsible
// for serializing Body into the persisted JSONB payload.
type Event struct {
	Type     EventType
	ClientID uuid.UUID
	// Body is the user-visible JSON object (without the wrapping envelope).
	// The outbox wraps it as { event_id, type, occurred_at, ... }.
	Body any
}

// OutboxEnqueuer is the contract used by callers that want to fire a webhook.
type OutboxEnqueuer interface {
	Enqueue(ctx context.Context, ev *Event) error
}

// Outbox is the production implementation of OutboxEnqueuer backed by
// Postgres + the catalog.Clients repository.
type Outbox struct {
	db      *pgxpool.Pool
	clients *catalog.Clients
	log     *zap.Logger
}

// NewOutbox builds a ready-to-use Outbox.
func NewOutbox(db *pgxpool.Pool, clients *catalog.Clients, log *zap.Logger) *Outbox {
	return &Outbox{db: db, clients: clients, log: log}
}

// Enqueue inserts a webhook_deliveries row for the given event.
//
// It is a no-op (returns nil) when the destination client has no URL,
// has webhooks disabled, or has not subscribed to the event type — that's
// the cheap path for clients that haven't opted in.
func (o *Outbox) Enqueue(ctx context.Context, ev *Event) error {
	if ev == nil {
		return fmt.Errorf("webhook: nil event")
	}
	cfg, err := o.clients.GetWebhookConfig(ctx, ev.ClientID)
	if err != nil {
		return fmt.Errorf("webhook outbox: load config: %w", err)
	}
	if cfg == nil || !cfg.Enabled || cfg.URL == "" {
		return nil
	}
	if !subscribedTo(cfg.Events, string(ev.Type)) {
		return nil
	}

	envelope := map[string]any{
		"event_id":    uuid.New().String(),
		"type":        string(ev.Type),
		"occurred_at": time.Now().UTC().Format(time.RFC3339Nano),
		"data":        ev.Body,
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("webhook outbox: marshal payload: %w", err)
	}

	_, err = o.db.Exec(ctx, `
		INSERT INTO webhook_deliveries
			(client_id, event_type, payload, status, attempt_count, next_attempt_at)
		VALUES ($1, $2, $3::jsonb, 'pending', 0, NOW())`,
		ev.ClientID, string(ev.Type), payload,
	)
	if err != nil {
		return fmt.Errorf("webhook outbox: insert: %w", err)
	}

	metrics.WebhookDeliveriesTotal.WithLabelValues("enqueued").Inc()
	o.log.Info("webhook outbox: enqueued",
		zap.String("client_id", ev.ClientID.String()),
		zap.String("event_type", string(ev.Type)),
	)
	return nil
}

func subscribedTo(events []string, t string) bool {
	if len(events) == 0 {
		return true // empty filter = subscribe to all
	}
	for _, e := range events {
		if e == t || e == "*" {
			return true
		}
	}
	return false
}

// signBody returns the lowercase hex encoding of HMAC-SHA256(secret, body).
//
// DEPRECATED: kept only for backwards compatibility in tests. New deliveries
// use signTimestampedBody to prevent replay (the unsigned-timestamp scheme
// allowed an on-path attacker to replay an intercepted webhook indefinitely
// because the body alone never expires).
func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// signTimestampedBody returns lowercase hex of
// HMAC-SHA256(secret, fmt.Sprintf("%d.%s", unixSeconds, body)).
//
// Rationale: signing only `body` lets an MITM (or anyone with stolen-in-flight
// payloads) replay valid POSTs forever. Including a timestamp in the signed
// material — Stripe-style — lets the receiver enforce a freshness window
// (e.g. 5 minutes) and reject anything older.
//
// The timestamp is also sent in the X-Radiocheck-Timestamp header so the
// receiver can re-derive the signature; if an attacker mutates the header,
// the signature won't match.
func signTimestampedBody(secret string, unixSeconds int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	// Stripe uses the literal "%d.%s" format. We do the same so docs/examples
	// match the most common reference implementation receivers will already
	// know.
	mac.Write([]byte(fmt.Sprintf("%d.", unixSeconds)))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
