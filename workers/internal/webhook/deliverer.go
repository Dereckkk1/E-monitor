package webhook

// Deliverer bridges NATS detection.confirmed events to the outbox.
//
// Architecturally this file replaces the legacy in-process deliverer (which
// did synchronous HTTP from a NATS callback). The new pipeline is:
//
//	ingestor -> NATS subject "detections.confirmed"
//	         -> Deliverer (here) -> Outbox (DB insert)
//	         -> Worker (worker.go) polls + POSTs
//
// Keeping the type name `Deliverer` and constructor `New` lets cmd/api/main.go
// continue to call webhook.New(...).Start(ctx) without churn.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/events"
)

// Deliverer subscribes to detection events on NATS and forwards them to the
// outbox so the persistent worker can deliver them with retries.
type Deliverer struct {
	db          *pgxpool.Pool
	nc          *nats.Conn
	log         *zap.Logger
	outbox      *Outbox
	clients     *catalog.Clients
	commercials *catalog.Commercials
	stations    *catalog.Stations
}

// New builds a Deliverer wired to the given infrastructure.
func New(
	db *pgxpool.Pool,
	nc *nats.Conn,
	log *zap.Logger,
	clients *catalog.Clients,
	commercials *catalog.Commercials,
	stations *catalog.Stations,
) *Deliverer {
	return &Deliverer{
		db:          db,
		nc:          nc,
		log:         log,
		outbox:      NewOutbox(db, clients, log),
		clients:     clients,
		commercials: commercials,
		stations:    stations,
	}
}

// Outbox exposes the underlying enqueuer for callers that want to fire test
// events (e.g. the API "send test webhook" handler).
func (d *Deliverer) Outbox() *Outbox { return d.outbox }

// Start subscribes to detections.confirmed and translates each message into
// an Outbox.Enqueue call. The subscription is drained when ctx is cancelled.
func (d *Deliverer) Start(ctx context.Context) error {
	sub, err := d.nc.Subscribe(events.SubjectDetectionConfirmed, func(msg *nats.Msg) {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := d.handleDetection(bgCtx, msg.Data); err != nil {
			d.log.Warn("webhook deliverer: handle detection failed", zap.Error(err))
		}
	})
	if err != nil {
		return fmt.Errorf("webhook: subscribe: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = sub.Drain()
	}()
	return nil
}

// detectionEvent matches the JSON shape published by ingestor.publishDetection.
type detectionEvent struct {
	StationID           string  `json:"station_id"`
	CommercialShortID   int32   `json:"commercial_short_id"`
	DetectedAt          string  `json:"detected_at"`
	OffsetFrames        int     `json:"offset_frames"`
	Confidence          float64 `json:"confidence"`
	EvidenceWindowStart string  `json:"evidence_window_start"`
	EvidenceWindowEnd   string  `json:"evidence_window_end"`
}

// handleDetection enriches the raw event with station + commercial + client
// metadata and enqueues one webhook per affected client (today: only the
// commercial owner).
func (d *Deliverer) handleDetection(ctx context.Context, raw []byte) error {
	var ev detectionEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}

	// Look up the commercial (by short_id) to discover its owning client.
	var (
		commercialID  uuid.UUID
		commercialTitle string
		clientID      uuid.UUID
	)
	row := d.db.QueryRow(ctx, `
		SELECT c.id, COALESCE(c.title, ''), COALESCE(ca.client_id, '00000000-0000-0000-0000-000000000000'::uuid)
		FROM commercials c
		LEFT JOIN campaigns ca ON ca.id = c.campaign_id
		WHERE c.short_id = $1 AND c.fingerprint_status = 'ready'
		LIMIT 1`, ev.CommercialShortID)
	if err := row.Scan(&commercialID, &commercialTitle, &clientID); err != nil {
		return fmt.Errorf("lookup commercial %d: %w", ev.CommercialShortID, err)
	}
	if clientID == uuid.Nil {
		// No client = no webhook recipient. Quietly drop.
		return nil
	}

	stationUUID, err := uuid.Parse(ev.StationID)
	if err != nil {
		return fmt.Errorf("parse station id: %w", err)
	}
	var stationName string
	_ = d.db.QueryRow(ctx,
		`SELECT COALESCE(name, '') FROM stations WHERE id = $1`, stationUUID,
	).Scan(&stationName)

	body := map[string]any{
		"detection": map[string]any{
			"detected_at":   ev.DetectedAt,
			"confidence":    ev.Confidence,
			"offset_frames": ev.OffsetFrames,
		},
		"station": map[string]any{
			"id":   ev.StationID,
			"name": stationName,
		},
		"commercial": map[string]any{
			"id":       commercialID.String(),
			"short_id": ev.CommercialShortID,
			"title":    commercialTitle,
		},
	}

	return d.outbox.Enqueue(ctx, &Event{
		Type:     EventDetectionConfirmed,
		ClientID: clientID,
		Body:     body,
	})
}
