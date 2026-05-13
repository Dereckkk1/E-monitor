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
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/events"
	"radiocheck/internal/observability"
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

// Start subscribes to detections.confirmed (and detections.retracted, for
// §18.2.2) and translates each message into an Outbox.Enqueue call. Both
// subscriptions are drained when ctx is cancelled.
func (d *Deliverer) Start(ctx context.Context) error {
	subConfirmed, err := d.nc.Subscribe(events.SubjectDetectionConfirmed, func(msg *nats.Msg) {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		spanCtx, span := observability.StartConsumerSpan(bgCtx, msg, "webhook.outbox_enqueue")
		defer span.End()
		if err := d.handleDetection(spanCtx, msg.Data); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			d.log.Warn("webhook deliverer: handle detection failed", zap.Error(err))
		}
	})
	if err != nil {
		return fmt.Errorf("webhook: subscribe confirmed: %w", err)
	}
	subRetracted, err := d.nc.Subscribe(events.SubjectDetectionRetracted, func(msg *nats.Msg) {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		spanCtx, span := observability.StartConsumerSpan(bgCtx, msg, "webhook.outbox_enqueue_retracted")
		defer span.End()
		if err := d.handleRetracted(spanCtx, msg.Data); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			d.log.Warn("webhook deliverer: handle retracted failed", zap.Error(err))
		}
	})
	if err != nil {
		_ = subConfirmed.Drain()
		return fmt.Errorf("webhook: subscribe retracted: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = subConfirmed.Drain()
		_ = subRetracted.Drain()
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

	// We need station_id + detected_at parsed up-front so the materials
	// fallback can pick the active campaign that targets this station on
	// the day the match fired.
	stationUUID, err := uuid.Parse(ev.StationID)
	if err != nil {
		return fmt.Errorf("parse station id: %w", err)
	}
	detectedAt, err := time.Parse(time.RFC3339, ev.DetectedAt)
	if err != nil {
		return fmt.Errorf("parse detected_at: %w", err)
	}

	// Look up the commercial (by short_id) to discover its owning client.
	// Try commercials first; if missing, fall back to materials via the
	// campaign_materials link active on detectedAt for this station.
	// Multi-attribution is a future feature (F-119); for now the most
	// recently added link wins.
	var (
		commercialID    uuid.UUID
		commercialTitle string
		clientID        uuid.UUID
	)
	lookupErr := d.db.QueryRow(ctx, `
		SELECT c.id, COALESCE(c.title, ''), COALESCE(ca.client_id, '00000000-0000-0000-0000-000000000000'::uuid)
		FROM commercials c
		LEFT JOIN campaigns ca ON ca.id = c.campaign_id
		WHERE c.short_id = $1 AND c.fingerprint_status = 'ready'
		LIMIT 1`, ev.CommercialShortID).Scan(&commercialID, &commercialTitle, &clientID)

	if errors.Is(lookupErr, pgx.ErrNoRows) {
		lookupErr = d.db.QueryRow(ctx, `
			SELECT m.id, COALESCE(m.title, ''), ca.client_id
			FROM materials m
			JOIN campaign_materials cm ON cm.material_id = m.id
			JOIN campaigns ca           ON ca.id = cm.campaign_id
			WHERE m.short_id = $1
			  AND m.fingerprint_status = 'ready'
			  AND $2 = ANY(cm.target_stations)
			  AND ca.status IN ('programada','ativa')
			  AND $3::date BETWEEN ca.start_date AND ca.end_date
			ORDER BY cm.added_at DESC
			LIMIT 1`, ev.CommercialShortID, stationUUID, detectedAt).Scan(&commercialID, &commercialTitle, &clientID)
	}

	if lookupErr != nil {
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			// Neither commercials nor materials resolve this short_id.
			// Drop quietly — no client to notify.
			d.log.Warn("webhook confirmed: short_id resolved to neither commercial nor material",
				zap.Int32("short_id", ev.CommercialShortID),
				zap.String("station_id", ev.StationID),
			)
			return nil
		}
		return fmt.Errorf("lookup commercial %d: %w", ev.CommercialShortID, lookupErr)
	}
	if clientID == uuid.Nil {
		// No client = no webhook recipient. Quietly drop.
		return nil
	}

	// Annotate the active span (deliverer.handle) so a search by client_id
	// or station_id surfaces this enqueue.
	observability.AddSpanAttributes(ctx,
		attribute.String("client_id", clientID.String()),
		attribute.String("station_id", ev.StationID),
		attribute.Int("commercial_short_id", int(ev.CommercialShortID)),
	)
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

// retractedEvent matches the JSON shape published by the supervisor on
// SubjectDetectionRetracted (see workers/internal/supervisor/disambiguation.go).
type retractedEvent struct {
	StationID         string  `json:"station_id"`
	CommercialShortID int32   `json:"commercial_short_id"`
	DetectedAt        string  `json:"detected_at"`
	RetractedAt       string  `json:"retracted_at"`
	Reason            string  `json:"reason"`
	Confidence        float64 `json:"confidence"`
}

// handleRetracted enqueues a detection.retracted webhook for the client that
// owns the affected commercial. Payload mirrors the confirmed envelope but
// carries detected_at + retracted_at + reason so receivers can mark their
// local copy as overruled.
func (d *Deliverer) handleRetracted(ctx context.Context, raw []byte) error {
	var ev retractedEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}

	// We need station_id + detected_at parsed up-front for the materials
	// fallback (same shape as handleDetection).
	stationUUID, err := uuid.Parse(ev.StationID)
	if err != nil {
		return fmt.Errorf("parse station id: %w", err)
	}
	detectedAt, err := time.Parse(time.RFC3339, ev.DetectedAt)
	if err != nil {
		return fmt.Errorf("parse detected_at: %w", err)
	}

	// Look up the commercial + owning client by short_id (commercial may
	// already have its row touched by the supervisor's UPDATE — that's fine,
	// we just need the client). Mirrors handleDetection's dual-lookup but
	// without the fingerprint_status filter: a retraction can fire on a
	// commercial/material whose row is mid-update.
	var (
		commercialID    uuid.UUID
		commercialTitle string
		clientID        uuid.UUID
	)
	lookupErr := d.db.QueryRow(ctx, `
		SELECT c.id, COALESCE(c.title, ''), COALESCE(ca.client_id, '00000000-0000-0000-0000-000000000000'::uuid)
		FROM commercials c
		LEFT JOIN campaigns ca ON ca.id = c.campaign_id
		WHERE c.short_id = $1
		LIMIT 1`, ev.CommercialShortID).Scan(&commercialID, &commercialTitle, &clientID)

	if errors.Is(lookupErr, pgx.ErrNoRows) {
		lookupErr = d.db.QueryRow(ctx, `
			SELECT m.id, COALESCE(m.title, ''), ca.client_id
			FROM materials m
			JOIN campaign_materials cm ON cm.material_id = m.id
			JOIN campaigns ca           ON ca.id = cm.campaign_id
			WHERE m.short_id = $1
			  AND $2 = ANY(cm.target_stations)
			  AND ca.status IN ('programada','ativa')
			  AND $3::date BETWEEN ca.start_date AND ca.end_date
			ORDER BY cm.added_at DESC
			LIMIT 1`, ev.CommercialShortID, stationUUID, detectedAt).Scan(&commercialID, &commercialTitle, &clientID)
	}

	if lookupErr != nil {
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			d.log.Warn("webhook retracted: short_id resolved to neither commercial nor material",
				zap.Int32("short_id", ev.CommercialShortID),
				zap.String("station_id", ev.StationID),
			)
			return nil
		}
		return fmt.Errorf("lookup commercial %d: %w", ev.CommercialShortID, lookupErr)
	}
	if clientID == uuid.Nil {
		return nil
	}
	var stationName string
	_ = d.db.QueryRow(ctx,
		`SELECT COALESCE(name, '') FROM stations WHERE id = $1`, stationUUID,
	).Scan(&stationName)

	body := map[string]any{
		"detection": map[string]any{
			"detected_at":  ev.DetectedAt,
			"retracted_at": ev.RetractedAt,
			"reason":       ev.Reason,
			"confidence":   ev.Confidence,
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
		Type:     EventDetectionRetracted,
		ClientID: clientID,
		Body:     body,
	})
}
