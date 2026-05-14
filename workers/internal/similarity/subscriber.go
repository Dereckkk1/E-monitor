package similarity

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

// Subscriber listens on SubjectMaterialSimilarityCheck and runs
// CheckMaterialSimilarity for each material named in the payload. Failures
// are logged and persisted to materials.similarity_check_status='failed' by
// CheckMaterialSimilarity itself.
type Subscriber struct {
	pool *pgxpool.Pool
	nc   *nats.Conn
	log  *zap.Logger
}

// NewSubscriber wires a Subscriber to the given DB pool, NATS connection,
// and logger. Subscribe() registers the listener; the returned subscription
// is drained when ctx ends.
func NewSubscriber(pool *pgxpool.Pool, nc *nats.Conn, log *zap.Logger) *Subscriber {
	return &Subscriber{pool: pool, nc: nc, log: log}
}

type payload struct {
	MaterialID string `json:"material_id"`
}

// Subscribe registers the listener. Each message decodes the payload, parses
// the material UUID, and runs the scan in a background context (the parent
// ctx is used only to drive Drain on shutdown).
//
// Failures inside the handler are logged and swallowed: an un-scanned
// material falls back to similarity_check_status='pending' or 'failed', and
// can be retried via `nats pub material.similarity-check '{...}'`.
func (s *Subscriber) Subscribe(ctx context.Context) (*nats.Subscription, error) {
	sub, err := s.nc.Subscribe(events.SubjectMaterialSimilarityCheck, func(msg *nats.Msg) {
		var p payload
		if err := json.Unmarshal(msg.Data, &p); err != nil {
			s.log.Warn("similarity: invalid payload",
				zap.Error(err),
				zap.ByteString("data", msg.Data),
			)
			return
		}
		materialID, err := uuid.Parse(p.MaterialID)
		if err != nil {
			s.log.Warn("similarity: invalid material_id",
				zap.String("material_id", p.MaterialID),
				zap.Error(err),
			)
			return
		}

		bgCtx := context.Background()
		if err := CheckMaterialSimilarity(bgCtx, s.pool, materialID); err != nil {
			s.log.Error("similarity: CheckMaterialSimilarity failed",
				zap.String("material_id", materialID.String()),
				zap.Error(err),
			)
			return
		}
		s.log.Info("similarity: ok",
			zap.String("material_id", materialID.String()),
		)
	})
	if err != nil {
		return nil, fmt.Errorf("similarity: subscribe: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = sub.Drain()
	}()
	return sub, nil
}
