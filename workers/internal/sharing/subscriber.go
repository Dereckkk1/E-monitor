package sharing

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"radiocheck/internal/events"
)

// Subscriber listens on SubjectFingerprintSharedScan and runs MarkSharedHashes
// for the commercial named in each message. After flagging, it republishes
// SubjectIndexReload so the in-memory matching index picks up the new flags.
type Subscriber struct {
	pool *pgxpool.Pool
	nc   *nats.Conn
	log  *zap.Logger
}

// NewSubscriber wires a Subscriber to the given DB pool, NATS connection,
// and logger. Subscribe() registers the listener; the returned subscription
// should be drained on shutdown.
func NewSubscriber(pool *pgxpool.Pool, nc *nats.Conn, log *zap.Logger) *Subscriber {
	return &Subscriber{pool: pool, nc: nc, log: log}
}

// payload accepts both commercial_id and material_id keys. The Python
// daemon picks the one matching what the API published upstream. Exactly
// one should be set per message; commercial_id takes precedence if both
// are present (matches index/loader.go reloadPayload behavior).
type payload struct {
	CommercialID string `json:"commercial_id,omitempty"`
	MaterialID   string `json:"material_id,omitempty"`
}

// Subscribe starts the listener. Each message resolves the entity's master
// path from the DB and runs the shared-hash scan. After a successful scan
// it publishes SubjectIndexReload so the matching index reloads with the
// freshly-set is_shared flags.
//
// Failures are logged and swallowed: an un-flagged entity just falls back
// to all-unique scoring (the pre-fix behaviour); the next upload that
// touches the same audio region will retry the flagging.
func (s *Subscriber) Subscribe(ctx context.Context) (*nats.Subscription, error) {
	sub, err := s.nc.Subscribe(events.SubjectFingerprintSharedScan, func(msg *nats.Msg) {
		var p payload
		if err := json.Unmarshal(msg.Data, &p); err != nil {
			s.log.Warn("shared-scan: invalid payload",
				zap.Error(err),
				zap.ByteString("data", msg.Data),
			)
			return
		}

		// We use a fresh background context per message — the parent ctx is
		// only used to drive Drain on shutdown.
		bgCtx := context.Background()

		var entityID uuid.UUID
		var entityKind string
		var masterPath string
		var status string
		var lookupErr error

		if p.CommercialID != "" {
			entityKind = "commercial"
			entityID, lookupErr = uuid.Parse(p.CommercialID)
			if lookupErr == nil {
				lookupErr = s.pool.QueryRow(bgCtx,
					`SELECT master_storage_path, fingerprint_status FROM commercials WHERE id = $1`,
					entityID,
				).Scan(&masterPath, &status)
			}
		} else if p.MaterialID != "" {
			entityKind = "material"
			entityID, lookupErr = uuid.Parse(p.MaterialID)
			if lookupErr == nil {
				lookupErr = s.pool.QueryRow(bgCtx,
					`SELECT master_storage_path, fingerprint_status FROM materials WHERE id = $1`,
					entityID,
				).Scan(&masterPath, &status)
			}
		} else {
			s.log.Warn("shared-scan: payload missing both commercial_id and material_id")
			return
		}

		if lookupErr != nil {
			s.log.Warn("shared-scan: lookup failed",
				zap.String("entity_id", entityID.String()),
				zap.String("kind", entityKind),
				zap.Error(lookupErr),
			)
			return
		}
		if status != "ready" {
			s.log.Info("shared-scan: skipping — fingerprint_status not ready",
				zap.String("entity_id", entityID.String()),
				zap.String("kind", entityKind),
				zap.String("status", status),
			)
			return
		}

		if err := MarkSharedHashes(bgCtx, s.pool, entityID, masterPath); err != nil {
			s.log.Error("shared-scan: MarkSharedHashes failed",
				zap.String("entity_id", entityID.String()),
				zap.String("kind", entityKind),
				zap.Error(err),
			)
			return
		}

		// Republish index.reload so the matching index picks up the
		// freshly-set is_shared flags. The earlier reload published by the
		// Python service made the new entity's hashes visible without
		// flags; this second reload supersedes them.
		var reload []byte
		if entityKind == "commercial" {
			reload, _ = json.Marshal(payload{CommercialID: entityID.String()})
		} else {
			reload, _ = json.Marshal(payload{MaterialID: entityID.String()})
		}
		if err := s.nc.Publish(events.SubjectIndexReload, reload); err != nil {
			s.log.Warn("shared-scan: republish index.reload failed",
				zap.String("entity_id", entityID.String()),
				zap.String("kind", entityKind),
				zap.Error(err),
			)
			return
		}
		s.log.Info("shared-scan: ok",
			zap.String("entity_id", entityID.String()),
			zap.String("kind", entityKind),
		)
	})
	if err != nil {
		return nil, fmt.Errorf("sharing: subscribe: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = sub.Drain()
	}()
	return sub, nil
}
