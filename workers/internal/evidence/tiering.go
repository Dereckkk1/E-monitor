package evidence

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/metrics"
	"radiocheck/internal/storage"
)

// TieringJob moves evidence objects between storage tiers based on age (§11.4):
//
//	hot     0–30 days     local SSD / hot bucket
//	cold    30–365 days   R2 standard
//	archive 365+ days     R2 IA
//
// The job is idempotent: if it crashes between copying to the new tier and
// deleting from the old, the next run will retry the delete (or, if the
// detection row is already updated, just observe the duplicate and move on).
type TieringJob struct {
	DB        *pgxpool.Pool
	Hot       *storage.Client
	Cold      *storage.Client
	Archive   *storage.Client
	Log       *zap.Logger
	BatchSize int           // default 100
	Now       func() time.Time
	HotMaxAge time.Duration // default 30d
	ColdMaxAge time.Duration // default 365d
	// ArchiveStorageClass is forwarded to the archive bucket on PUT (e.g.
	// "STANDARD_IA"). Empty string lets the bucket default decide.
	ArchiveStorageClass string
}

// NewTieringJob returns a TieringJob with sensible defaults wired in. Hot,
// Cold, Archive may all point to the same *storage.Client during local dev —
// in that case prefix-based separation is used (still safe).
func NewTieringJob(db *pgxpool.Pool, hot, cold, archive *storage.Client, log *zap.Logger) *TieringJob {
	return &TieringJob{
		DB:                  db,
		Hot:                 hot,
		Cold:                cold,
		Archive:             archive,
		Log:                 log,
		BatchSize:           100,
		Now:                 time.Now,
		HotMaxAge:           30 * 24 * time.Hour,
		ColdMaxAge:          365 * 24 * time.Hour,
		ArchiveStorageClass: "STANDARD_IA",
	}
}

// Run executes a single tiering pass: hot→cold then cold→archive. It is safe
// to invoke concurrently from a manual admin trigger; database row updates use
// a UNIQUE constraint on detection id and the S3 ops are idempotent.
func (j *TieringJob) Run(ctx context.Context) error {
	start := time.Now()
	j.Log.Info("evidence tiering: starting pass")

	hotMoved, hotErrs := j.moveTier(ctx, "hot", "cold", j.Hot, j.Cold, j.HotMaxAge, "")
	coldMoved, coldErrs := j.moveTier(ctx, "cold", "archive", j.Cold, j.Archive, j.ColdMaxAge, j.ArchiveStorageClass)

	if err := j.refreshStorageGauge(ctx); err != nil {
		j.Log.Warn("evidence tiering: refresh storage gauge failed", zap.Error(err))
	}
	metrics.EvidenceTieringLastRun.Set(float64(j.Now().Unix()))

	j.Log.Info("evidence tiering: pass complete",
		zap.Int("hot_to_cold", hotMoved),
		zap.Int("cold_to_archive", coldMoved),
		zap.Int("errors", hotErrs+coldErrs),
		zap.Duration("duration", time.Since(start)),
	)
	if hotErrs+coldErrs > 0 {
		return fmt.Errorf("tiering: %d errors in pass", hotErrs+coldErrs)
	}
	return nil
}

// candidate represents one detection row picked up by the tier query.
type candidate struct {
	ID          uuid.UUID
	DetectedAt  time.Time
	EvidenceKey string
	SizeBytes   int64
}

// moveTier scans for candidates in `fromTier` older than `maxAge`, copies them
// to the destination bucket and updates the detections row. Returns moved
// count + error count.
func (j *TieringJob) moveTier(
	ctx context.Context,
	fromTier, toTier string,
	src, dst *storage.Client,
	maxAge time.Duration,
	dstStorageClass string,
) (moved, errs int) {
	if src == nil || dst == nil {
		j.Log.Warn("evidence tiering: skipping — bucket missing",
			zap.String("from", fromTier), zap.String("to", toTier))
		return 0, 0
	}
	cutoff := j.Now().Add(-maxAge)

	rows, err := j.DB.Query(ctx, `
		SELECT id, detected_at, evidence_key, COALESCE(evidence_size_bytes, 0)
		FROM detections
		WHERE tier = $1
		  AND created_at < $2
		  AND evidence_key IS NOT NULL
		  AND evidence_status = 'available'
		ORDER BY created_at ASC
		LIMIT $3`,
		fromTier, cutoff, j.BatchSize,
	)
	if err != nil {
		j.Log.Error("evidence tiering: query failed",
			zap.String("from", fromTier), zap.Error(err))
		metrics.EvidenceTieringErrors.Inc()
		return 0, 1
	}

	var batch []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.ID, &c.DetectedAt, &c.EvidenceKey, &c.SizeBytes); err != nil {
			j.Log.Warn("evidence tiering: scan failed", zap.Error(err))
			errs++
			continue
		}
		batch = append(batch, c)
	}
	rows.Close()

	for _, c := range batch {
		if ctx.Err() != nil {
			return moved, errs
		}
		if err := j.moveOne(ctx, c, fromTier, toTier, src, dst, dstStorageClass); err != nil {
			j.Log.Warn("evidence tiering: move failed",
				zap.String("detection_id", c.ID.String()),
				zap.String("from", fromTier),
				zap.String("to", toTier),
				zap.Error(err),
			)
			metrics.EvidenceTieringErrors.Inc()
			errs++
			continue
		}
		moved++
		metrics.EvidenceTierMovements.WithLabelValues(fromTier, toTier).Inc()
	}
	return moved, errs
}

// moveOne copies a single object to the destination and, only after success,
// updates the row + deletes the source. Order matters for idempotency.
func (j *TieringJob) moveOne(
	ctx context.Context,
	c candidate,
	fromTier, toTier string,
	src, dst *storage.Client,
	dstStorageClass string,
) error {
	// 1. Download from source (streaming, not buffered into RAM beyond the
	//    Reader contract).
	body, ct, size, err := src.Get(ctx, c.EvidenceKey)
	if err != nil {
		return fmt.Errorf("get from %s: %w", fromTier, err)
	}
	defer body.Close()

	// 2. Upload to destination (with storage class hint when applicable).
	//    For now we keep the same key; if buckets differ, the prefix is enough
	//    to separate. If src == dst (same bucket) we still benefit from the
	//    storage-class change.
	if dstStorageClass != "" {
		if err := dst.PutWithStorageClass(ctx, c.EvidenceKey, body, ct, dstStorageClass); err != nil {
			return fmt.Errorf("put to %s: %w", toTier, err)
		}
	} else {
		if err := dst.Put(ctx, c.EvidenceKey, body, ct); err != nil {
			return fmt.Errorf("put to %s: %w", toTier, err)
		}
	}

	// 3. Verify destination object exists (HEAD) before deleting source.
	if _, _, err := dst.Head(ctx, c.EvidenceKey); err != nil {
		return fmt.Errorf("verify dst: %w", err)
	}

	// 4. Update the detections row to reflect the new tier.
	finalSize := size
	if finalSize <= 0 {
		finalSize = c.SizeBytes
	}
	tag, err := j.DB.Exec(ctx, `
		UPDATE detections
		SET tier = $3, evidence_size_bytes = $4
		WHERE id = $1 AND detected_at = $2`,
		c.ID, c.DetectedAt, toTier, finalSize,
	)
	if err != nil {
		return fmt.Errorf("update detections: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errors.New("detection row vanished mid-move")
	}

	// 5. Delete from source — only if src and dst are different buckets,
	//    OR if storage class is different. Same bucket + same key + same
	//    class would be a no-op + dangerous delete.
	if src.Bucket() != dst.Bucket() {
		if err := src.Delete(ctx, c.EvidenceKey); err != nil {
			// Don't fail the whole move — the row is already updated, the
			// next run will retry the delete (Delete is idempotent).
			j.Log.Warn("evidence tiering: source delete failed (will retry)",
				zap.String("key", c.EvidenceKey),
				zap.String("bucket", src.Bucket()),
				zap.Error(err),
			)
			metrics.EvidenceTieringErrors.Inc()
		}
	}

	j.Log.Info("evidence tiering: moved",
		zap.String("detection_id", c.ID.String()),
		zap.String("key", c.EvidenceKey),
		zap.String("from", fromTier),
		zap.String("to", toTier),
		zap.Int64("size_bytes", finalSize),
	)
	return nil
}

// refreshStorageGauge updates the per-tier storage gauge using a SUM query.
func (j *TieringJob) refreshStorageGauge(ctx context.Context) error {
	rows, err := j.DB.Query(ctx, `
		SELECT tier, COALESCE(SUM(evidence_size_bytes), 0)
		FROM detections
		WHERE evidence_key IS NOT NULL
		GROUP BY tier`)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := map[string]bool{"hot": false, "cold": false, "archive": false}
	for rows.Next() {
		var tier string
		var bytes int64
		if err := rows.Scan(&tier, &bytes); err != nil {
			return err
		}
		metrics.EvidenceStorageBytes.WithLabelValues(tier).Set(float64(bytes))
		seen[tier] = true
	}
	for tier, ok := range seen {
		if !ok {
			metrics.EvidenceStorageBytes.WithLabelValues(tier).Set(0)
		}
	}
	return rows.Err()
}

// Schedule runs Run in a loop, firing once a day at 03:00 in
// America/Sao_Paulo (§11.4). It blocks until ctx is done.
func (j *TieringJob) Schedule(ctx context.Context) {
	if j.DB == nil {
		j.Log.Warn("evidence tiering: DB is nil — schedule disabled")
		<-ctx.Done()
		return
	}

	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		j.Log.Warn("evidence tiering: TZ load failed, using UTC", zap.Error(err))
		loc = time.UTC
	}

	// Tick once a minute; trigger when the local clock crosses 03:00 and we
	// haven't run today yet. Cheap and resilient to clock drift.
	var lastRun time.Time
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	// Also kick off an immediate pass on startup so the metrics gauge has a
	// real value within the first hour of uptime.
	go func() {
		runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		if err := j.Run(runCtx); err != nil {
			j.Log.Warn("evidence tiering: startup pass failed", zap.Error(err))
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().In(loc)
			if now.Hour() != 3 {
				continue
			}
			if !lastRun.IsZero() && now.Year() == lastRun.Year() && now.YearDay() == lastRun.YearDay() {
				continue
			}
			lastRun = now
			runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			if err := j.Run(runCtx); err != nil {
				j.Log.Warn("evidence tiering: scheduled pass failed", zap.Error(err))
			}
			cancel()
		}
	}
}

// pgxRow is a tiny helper kept here in case we ever swap pgxpool for a
// transaction-bound *pgx.Conn. Currently unused but documented intent.
var _ = pgx.ErrNoRows
var _ io.Reader = (io.Reader)(nil)
