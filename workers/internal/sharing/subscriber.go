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

// payload is the JSON shape produced by the Python fingerprint service after
// it finishes writing hashes for a commercial. Mirrors the index.reload
// payload so an operator can re-trigger flagging by hand if needed:
//
//	nats pub fingerprint.shared-scan '{"commercial_id":"<uuid>"}'
type payload struct {
	CommercialID string `json:"commercial_id"`
}

// Subscribe starts the listener. Each message resolves the commercial's
// master path from the DB and runs the shared-hash scan. After a successful
// scan it publishes SubjectIndexReload so the matching index reloads with
// the freshly-set is_shared flags.
//
// Failures are logged and swallowed: an un-flagged commercial just falls
// back to all-unique scoring (the pre-fix behaviour); the next upload that
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
		commercialID, err := uuid.Parse(p.CommercialID)
		if err != nil {
			s.log.Warn("shared-scan: invalid commercial_id",
				zap.String("commercial_id", p.CommercialID),
				zap.Error(err),
			)
			return
		}

		// We use a fresh background context per message — the parent ctx is
		// only used to drive Drain on shutdown.
		bgCtx := context.Background()

		// Look up the master path. The commercial may have been deleted
		// between the publish and our handler firing; treat that as a
		// no-op rather than an error.
		var masterPath string
		var status string
		if err := s.pool.QueryRow(bgCtx,
			`SELECT master_storage_path, fingerprint_status FROM commercials WHERE id = $1`,
			commercialID,
		).Scan(&masterPath, &status); err != nil {
			s.log.Warn("shared-scan: lookup commercial failed",
				zap.String("commercial_id", commercialID.String()),
				zap.Error(err),
			)
			return
		}
		if status != "ready" {
			s.log.Info("shared-scan: skipping — fingerprint_status not ready",
				zap.String("commercial_id", commercialID.String()),
				zap.String("status", status),
			)
			return
		}

		if err := MarkSharedHashes(bgCtx, s.pool, commercialID, masterPath); err != nil {
			s.log.Error("shared-scan: MarkSharedHashes failed",
				zap.String("commercial_id", commercialID.String()),
				zap.Error(err),
			)
			return
		}

		// Republish index.reload so the matching index picks up the
		// freshly-set is_shared flags. The earlier reload published by the
		// Python service made the new commercial's hashes visible without
		// flags; this second reload supersedes them.
		reload, _ := json.Marshal(payload{CommercialID: commercialID.String()})
		if err := s.nc.Publish(events.SubjectIndexReload, reload); err != nil {
			s.log.Warn("shared-scan: republish index.reload failed",
				zap.String("commercial_id", commercialID.String()),
				zap.Error(err),
			)
			return
		}
		s.log.Info("shared-scan: ok",
			zap.String("commercial_id", commercialID.String()),
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
