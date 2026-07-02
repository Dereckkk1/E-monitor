package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DedupSuppression is one recorded §18.2.2 suppression (audit 2026-07-02 A3):
// the "losing" detection the dedup dropped, plus the "kept" winner. Persisting
// both confidences lets us spot the pathological case where the SUPPRESSED cut
// was MORE confident than the kept one — a real airing likely killed by a
// weaker (false-confirming) longer cut. Internal/forensic only; never surfaced
// to clients.
type DedupSuppression struct {
	StationID            uuid.UUID
	SuppressedShortID    int32
	KeptShortID          int32
	SuppressedDuration   int
	KeptDuration         int
	SuppressedConfidence float64
	KeptConfidence       float64
	BroadcastStart       time.Time
	DetectedAt           time.Time
	Reason               string
}

// DedupSuppressions is the repository for the dedup_suppressions audit table.
type DedupSuppressions struct {
	pool *pgxpool.Pool
}

// NewDedupSuppressions returns a repo backed by pool.
func NewDedupSuppressions(pool *pgxpool.Pool) *DedupSuppressions {
	return &DedupSuppressions{pool: pool}
}

// Record persists one suppression. Best-effort forensic write, never on the hot
// matching path — only fires when the dedup drops a detection.
func (d *DedupSuppressions) Record(ctx context.Context, s DedupSuppression) error {
	_, err := d.pool.Exec(ctx, `
		INSERT INTO dedup_suppressions
		  (station_id, suppressed_short_id, kept_short_id, suppressed_duration,
		   kept_duration, suppressed_confidence, kept_confidence,
		   broadcast_start, detected_at, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		s.StationID, s.SuppressedShortID, s.KeptShortID, s.SuppressedDuration,
		s.KeptDuration, s.SuppressedConfidence, s.KeptConfidence,
		s.BroadcastStart, s.DetectedAt, s.Reason)
	return err
}
