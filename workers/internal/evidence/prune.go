package evidence

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/metrics"
)

// Local retention prune (§11.4, prod variant). The plan's original design tiered
// aged evidence hot→cold→archive across separate R2 buckets. In production all
// three tiers point at the same MinIO bucket (see main.go / NewTieringJob), so
// the tier "move" is a no-op DB flag flip and evidence never leaves MinIO —
// growing the disk unbounded until PutObject starts returning 507
// (incident 2026-07-02). Until real R2 offload is wired, we bound MinIO with a
// hard local retention: delete evidence objects older than RetentionMaxAge and
// mark the row 'expired'. The detection itself stays (it still counts as an
// airing); only the audio clip is reclaimed. See
// docs/features/evidence-local-retention.md.

// pruneCandidate is one detection row eligible for retention pruning.
type pruneCandidate struct {
	ID          uuid.UUID
	DetectedAt  time.Time
	EvidenceKey string
	SizeBytes   int64
}

// deleteFunc removes the stored object for a key (MinIO/S3).
type deleteFunc func(ctx context.Context, key string) error

// expireFunc marks a detection row's evidence as expired (status='expired',
// key cleared) after its object has been deleted.
type expireFunc func(ctx context.Context, id uuid.UUID, detectedAt time.Time) error

// runPrune is the destructive core of local retention, isolated from DB/S3
// wiring so its ordering and error handling are unit-tested (prune_test.go).
//
// For each candidate it deletes the object FIRST, then — only on success —
// marks the row expired. Order is deliberate: a crash between the two steps
// must leave an orphaned 'available' row (re-pruned on the next pass) rather
// than an 'expired' row still pointing at a live object that would then never
// be reclaimed. A delete failure leaves the row untouched and is counted, so
// the whole move is retried next run (Delete is idempotent). dryRun performs
// no side effects and only accounts what WOULD be pruned — the safety valve for
// the first production rollout.
func runPrune(
	ctx context.Context,
	cands []pruneCandidate,
	dryRun bool,
	del deleteFunc,
	expire expireFunc,
	log *zap.Logger,
) (pruned int, reclaimed int64, errs int) {
	for _, c := range cands {
		if ctx.Err() != nil {
			return pruned, reclaimed, errs
		}
		if dryRun {
			pruned++
			reclaimed += c.SizeBytes
			continue
		}
		if err := del(ctx, c.EvidenceKey); err != nil {
			log.Warn("evidence prune: object delete failed (row left intact, will retry)",
				zap.String("detection_id", c.ID.String()),
				zap.String("key", c.EvidenceKey),
				zap.Error(err),
			)
			errs++
			continue
		}
		if err := expire(ctx, c.ID, c.DetectedAt); err != nil {
			log.Warn("evidence prune: mark expired failed (object already deleted, will retry)",
				zap.String("detection_id", c.ID.String()),
				zap.Error(err),
			)
			errs++
			continue
		}
		pruned++
		reclaimed += c.SizeBytes
	}
	return pruned, reclaimed, errs
}

// pruneExpired enforces local retention: repeatedly select the oldest evidence
// clips past RetentionMaxAge and prune them (delete object + mark row expired)
// until none remain or ctx is done. No-op unless RetentionMaxAge>0 and the Hot
// client + Detections repo are wired. Best-effort — individual failures are
// counted, never panic. All evidence physically lives in the Hot/MinIO bucket
// today, so deletes always target j.Hot.
func (j *TieringJob) pruneExpired(ctx context.Context) (pruned int, reclaimed int64, errs int) {
	if j.RetentionMaxAge <= 0 || j.Hot == nil || j.Detections == nil {
		return 0, 0, 0
	}
	cutoff := j.Now().Add(-j.RetentionMaxAge)
	del := func(ctx context.Context, key string) error { return j.Hot.Delete(ctx, key) }
	expire := func(ctx context.Context, id uuid.UUID, detectedAt time.Time) error {
		return j.Detections.MarkEvidenceExpired(ctx, id, detectedAt)
	}

	// Dry-run: preview the FULL backlog (no LIMIT) without mutating anything.
	// Real deletes wouldn't advance the cursor in dry mode (status never flips
	// out of the predicate), so it must be a single unbounded pass.
	if j.PruneDryRun {
		cands, err := j.listExpiryCandidates(ctx, cutoff, 0)
		if err != nil {
			j.Log.Error("evidence prune: dry-run candidate query failed", zap.Error(err))
			return 0, 0, 1
		}
		p, r, e := runPrune(ctx, cands, true, del, expire, j.Log)
		j.Log.Info("evidence prune: DRY-RUN — clips that WOULD be deleted",
			zap.Int("clips", p), zap.Int64("bytes", r),
			zap.Time("cutoff", cutoff), zap.Duration("retention", j.RetentionMaxAge))
		return p, r, e
	}

	// Real prune: batch until the predicate is drained. Each pruned row flips
	// to 'expired' and leaves the predicate, so the next LIMIT batch returns
	// the following rows — no OFFSET needed.
	for {
		if ctx.Err() != nil {
			return pruned, reclaimed, errs
		}
		cands, err := j.listExpiryCandidates(ctx, cutoff, j.BatchSize)
		if err != nil {
			j.Log.Error("evidence prune: candidate query failed", zap.Error(err))
			return pruned, reclaimed, errs + 1
		}
		if len(cands) == 0 {
			return pruned, reclaimed, errs
		}
		p, r, e := runPrune(ctx, cands, false, del, expire, j.Log)
		pruned += p
		reclaimed += r
		errs += e
		metrics.EvidencePrunedTotal.Add(float64(p))
		metrics.EvidencePrunedBytes.Add(float64(r))
		// Short read = predicate drained. Also guard against a stuck cursor:
		// if a whole batch errored (nothing flipped to expired), the same rows
		// would return forever — bail rather than spin.
		if len(cands) < j.BatchSize || p == 0 {
			return pruned, reclaimed, errs
		}
	}
}

// listExpiryCandidates returns evidence clips older than cutoff that still have
// a live object ('available' + non-null key). limit<=0 means no LIMIT (dry-run
// full-backlog preview). Ordered oldest-first so the batched real prune drains
// the predicate deterministically. detected_at drives the age (it is the
// partition key, so the range scan prunes partitions).
func (j *TieringJob) listExpiryCandidates(ctx context.Context, cutoff time.Time, limit int) ([]pruneCandidate, error) {
	q := `
		SELECT id, detected_at, evidence_key, COALESCE(evidence_size_bytes, 0)
		FROM detections
		WHERE evidence_status = 'available'
		  AND evidence_key IS NOT NULL
		  AND detected_at < $1
		ORDER BY detected_at ASC`
	args := []any{cutoff}
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := j.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []pruneCandidate
	for rows.Next() {
		var c pruneCandidate
		if err := rows.Scan(&c.ID, &c.DetectedAt, &c.EvidenceKey, &c.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
