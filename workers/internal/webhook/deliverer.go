package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

type Deliverer struct {
	db   *pgxpool.Pool
	nc   *nats.Conn
	log  *zap.Logger
	http *http.Client
	sub  *nats.Subscription
}

func New(db *pgxpool.Pool, nc *nats.Conn, log *zap.Logger) *Deliverer {
	return &Deliverer{db: db, nc: nc, log: log, http: &http.Client{Timeout: 5 * time.Second}}
}

func (d *Deliverer) Start(ctx context.Context) error {
	sub, err := d.nc.Subscribe("detection.confirmed", func(msg *nats.Msg) {
		var payload map[string]interface{}
		if err := json.Unmarshal(msg.Data, &payload); err != nil {
			d.log.Error("webhook: malformed NATS message", zap.Error(err))
			return
		}
		d.dispatch(ctx, payload)
	})
	if err != nil {
		return fmt.Errorf("webhook: subscribe: %w", err)
	}
	d.sub = sub
	go func() {
		<-ctx.Done()
		_ = sub.Drain()
	}()
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.retryFailed(ctx)
			}
		}
	}()
	return nil
}

func (d *Deliverer) dispatch(ctx context.Context, payload map[string]interface{}) {
	rows, err := d.db.Query(ctx, `
		SELECT id::text, webhook_url, webhook_secret FROM clients
		WHERE webhook_url IS NOT NULL AND webhook_url != ''
	`)
	if err != nil {
		d.log.Warn("webhook: query clients failed", zap.Error(err))
		return
	}
	defer rows.Close()
	for rows.Next() {
		var clientID, url, secret string
		if err := rows.Scan(&clientID, &url, &secret); err != nil {
			d.log.Warn("webhook: dispatch scan failed", zap.Error(err))
			continue
		}
		body, _ := json.Marshal(map[string]interface{}{
			"event_type": "detection.confirmed",
			"detection":  payload,
		})
		if err := d.post(url, secret, body); err != nil {
			d.log.Warn("webhook: delivery failed, queuing retry",
				zap.String("client_id", clientID), zap.Error(err))
			_, _ = d.db.Exec(ctx, `
				INSERT INTO webhook_failures (client_id, detection_id, payload, next_retry_at, last_error)
				VALUES ($1, $2, $3, NOW() + INTERVAL '1 minute', $4)
			`, clientID, payload["detection_id"], body, err.Error())
		}
	}
}

func (d *Deliverer) post(url, secret string, body []byte) error {
	sig := hmacSHA256(secret, body)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", "sha256="+sig)
	req.Header.Set("X-Event-Type", "detection.confirmed")
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: status %d", resp.StatusCode)
	}
	return nil
}

func (d *Deliverer) retryFailed(ctx context.Context) {
	rows, err := d.db.Query(ctx, `
		SELECT wf.id::text, wf.client_id::text, wf.payload,
		       c.webhook_url, c.webhook_secret, wf.attempt
		FROM webhook_failures wf
		JOIN clients c ON c.id = wf.client_id
		WHERE wf.next_retry_at < NOW() AND wf.attempt <= 5
		  AND c.webhook_url IS NOT NULL AND c.webhook_url != ''
	`)
	if err != nil {
		d.log.Warn("webhook: retry query failed", zap.Error(err))
		return
	}
	defer rows.Close()
	retryDelays := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 4 * time.Hour}
	for rows.Next() {
		var id, clientID string
		var payload []byte
		var url, secret string
		var attempt int
		if err := rows.Scan(&id, &clientID, &payload, &url, &secret, &attempt); err != nil {
			d.log.Warn("webhook: retry scan failed", zap.Error(err))
			continue
		}
		if err := d.post(url, secret, payload); err == nil {
			_, _ = d.db.Exec(ctx, `DELETE FROM webhook_failures WHERE id = $1`, id)
		} else {
			if attempt >= 5 {
				d.log.Error("webhook dead letter", zap.String("failure_id", id))
				_, _ = d.db.Exec(ctx, `UPDATE webhook_failures SET attempt = 99 WHERE id = $1`, id)
				continue
			}
			_, _ = d.db.Exec(ctx,
				`UPDATE webhook_failures SET attempt = $2, next_retry_at = NOW() + $3, last_error = $4 WHERE id = $1`,
				id, attempt+1, retryDelays[attempt], err.Error())
		}
	}
}

func hmacSHA256(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body) //nolint:errcheck
	return hex.EncodeToString(mac.Sum(nil))
}
