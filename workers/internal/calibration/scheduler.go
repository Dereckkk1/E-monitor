// Package calibration's Scheduler periodically re-calibrates the per-station
// matching threshold (§9.4 — Threshold Adaptativo).
//
// The bootstrap calibration job (RunCalibrationJob) only handles the *initial*
// 7-day calibration window: it promotes a station out of calibration_mode by
// computing noise_p99 from the noise_samples buffer.
//
// In production, broadcast conditions drift over time (new equipment, jingles,
// audio processing changes). §9.4 requires that the threshold be recomputed
// every 7 days. This Scheduler closes that gap: every 24 h it looks for
// stations whose station_thresholds row has not been updated for at least 7
// days, and triggers a fresh calibration cycle by resetting calibration_mode
// to true and clearing noise_samples. The next ingestion windows refill the
// buffer; after another 7 days the existing RunCalibrationJob promotes the
// station back out of calibration mode with a fresh noise_p99.
//
// Multi-replica safety
// ────────────────────
// If two API replicas run simultaneously, both would race on the same set of
// stations and double-recalibrate. The scheduler wraps the work in a Postgres
// session-level advisory lock (pg_try_advisory_lock). The lock key is a
// FNV-1a hash of the literal string "radiocheck:calibration-scheduler". On
// every tick, the loser of the race logs and exits cleanly.
package calibration

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"radiocheck/internal/metrics"
)

// Defaults for the scheduler. Overridable via env vars in cmd/api/main.go
// (CALIBRATION_INTERVAL, CALIBRATION_MIN_AGE) so dev/test can shorten them.
const (
	DefaultSchedulerInterval = 24 * time.Hour
	DefaultMinAge            = 7 * 24 * time.Hour
	DefaultStationTimeout    = 5 * time.Minute
	DefaultMaxParallel       = 5
)

// advisoryLockKey is the deterministic int64 used by pg_try_advisory_lock.
// Computed once at package init; value is part of the public contract because
// any other Postgres client that wants to coordinate with the scheduler must
// use the same key.
var advisoryLockKey = computeLockKey("radiocheck:calibration-scheduler")

// AdvisoryLockKey exposes the lock key for diagnostics / SQL queries.
func AdvisoryLockKey() int64 { return advisoryLockKey }

func computeLockKey(s string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	// Cast to int64 — Postgres advisory lock keys are bigint.
	return int64(h.Sum64())
}

// Scheduler is the driver loop. It does NOT own the actual recalibration
// SQL — that lives in recalibrateStation below — so RunCalibrationJob's
// existing logic stays untouched.
type Scheduler struct {
	pool *pgxpool.Pool
	log  *zap.Logger

	Interval       time.Duration // how often to scan
	MinAge         time.Duration // ignore stations whose thresholds were updated more recently than this
	StationTimeout time.Duration // per-station ctx timeout
	MaxParallel    int64         // max stations recalibrated concurrently per tick

	// Hooks — overridable in tests.
	NowFn func() time.Time
}

// NewScheduler builds a Scheduler with default knobs. Callers may override
// Interval/MinAge after construction.
func NewScheduler(pool *pgxpool.Pool, log *zap.Logger) *Scheduler {
	return &Scheduler{
		pool:           pool,
		log:            log,
		Interval:       DefaultSchedulerInterval,
		MinAge:         DefaultMinAge,
		StationTimeout: DefaultStationTimeout,
		MaxParallel:    DefaultMaxParallel,
		NowFn:          time.Now,
	}
}

// Run blocks until ctx is canceled. It performs an initial pass immediately
// (matching the LifecycleScheduler pattern) so a freshly started API doesn't
// have to wait 24 h for the first calibration tick after a deploy.
func (s *Scheduler) Run(ctx context.Context) error {
	if s.pool == nil {
		return errors.New("calibration scheduler: pool is nil")
	}
	if s.log == nil {
		s.log = zap.NewNop()
	}
	s.log.Info("calibration scheduler started",
		zap.Duration("interval", s.Interval),
		zap.Duration("min_age", s.MinAge),
		zap.Int64("max_parallel", s.MaxParallel),
	)

	s.tick(ctx)

	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("calibration scheduler stopped")
			return nil
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// RunOnce executes a single scan immediately, ignoring the ticker. Used by
// the admin endpoint and tests. Returns the number of stations attempted.
func (s *Scheduler) RunOnce(ctx context.Context) (int, error) {
	return s.tickWithResult(ctx)
}

// RunOnceForStation forces a single station to be recalibrated immediately,
// bypassing the MinAge filter and the advisory lock. Used by the admin
// endpoint when an operator wants to re-run a specific station.
//
// Observabilidade: emite as **mesmas** métricas que o tick natural
// (radiocheck_calibration_runs_total{result}, last_success_timestamp,
// duration_seconds), via recalibrateAndRecord. Antes deste commit a
// rota admin não incrementava nada — qualquer dashboard/alerta baseado
// nessas métricas perdia execuções manuais.
func (s *Scheduler) RunOnceForStation(ctx context.Context, stationID uuid.UUID) error {
	stCtx, cancel := context.WithTimeout(ctx, s.StationTimeout)
	defer cancel()
	return s.recalibrateAndRecord(stCtx, stationID)
}

// recalibrateAndRecord wraps recalibrateOne with the standard metric
// instrumentation shared by both the tick path and the admin endpoint.
// Returns the underlying error from recalibrateOne unchanged.
func (s *Scheduler) recalibrateAndRecord(ctx context.Context, stationID uuid.UUID) error {
	start := time.Now()
	err := s.recalibrateOne(ctx, stationID)
	elapsed := time.Since(start)
	if err != nil {
		metrics.CalibrationRunsTotal.WithLabelValues("error").Inc()
		return err
	}
	metrics.CalibrationRunsTotal.WithLabelValues("success").Inc()
	metrics.CalibrationDurationSeconds.Observe(elapsed.Seconds())
	metrics.CalibrationLastSuccessTimestamp.WithLabelValues(stationID.String()).Set(float64(time.Now().Unix()))
	return nil
}

func (s *Scheduler) tick(ctx context.Context) {
	if _, err := s.tickWithResult(ctx); err != nil {
		s.log.Warn("calibration scheduler: tick failed", zap.Error(err))
	}
}

func (s *Scheduler) tickWithResult(ctx context.Context) (int, error) {
	// Take a connection so the session-level advisory lock survives across
	// the whole scan. Releasing the connection back to the pool releases
	// the lock automatically.
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	var locked bool
	if err := conn.QueryRow(ctx,
		"SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&locked); err != nil {
		return 0, fmt.Errorf("advisory lock: %w", err)
	}
	if !locked {
		s.log.Info("calibration scheduler: another instance holds the advisory lock; skipping tick")
		return 0, nil
	}
	defer func() {
		// Best-effort release; pool.Release already drops the lock when the
		// session ends, but unlocking explicitly is cheap and lets a co-located
		// replica grab the lock sooner.
		_, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()

	stations, err := s.listEligible(ctx, conn.Conn())
	if err != nil {
		return 0, fmt.Errorf("list eligible: %w", err)
	}
	if len(stations) == 0 {
		s.log.Debug("calibration scheduler: no stations eligible")
		return 0, nil
	}
	s.log.Info("calibration scheduler: starting pass",
		zap.Int("eligible_stations", len(stations)),
	)

	sem := semaphore.NewWeighted(s.MaxParallel)
	g, gctx := errgroup.WithContext(ctx)
	for _, id := range stations {
		id := id
		if err := sem.Acquire(gctx, 1); err != nil {
			break
		}
		g.Go(func() error {
			defer sem.Release(1)
			stCtx, cancel := context.WithTimeout(gctx, s.StationTimeout)
			defer cancel()
			start := time.Now()
			if err := s.recalibrateAndRecord(stCtx, id); err != nil {
				s.log.Warn("calibration scheduler: station failed",
					zap.String("station_id", id.String()),
					zap.Error(err))
				// Swallow — one station's failure must not interrupt others.
				return nil
			}
			s.log.Info("calibration scheduler: station recalibrated",
				zap.String("station_id", id.String()),
				zap.Duration("duration", time.Since(start)),
			)
			return nil
		})
	}
	_ = g.Wait()
	return len(stations), nil
}

// listEligible returns stations whose threshold row is older than MinAge AND
// not currently in calibration mode. Stations currently calibrating are
// already being handled by the existing RunCalibrationJob path.
//
// Stations that have *never* been calibrated (no station_thresholds row) are
// not returned: the worker creates the row on first ingestion and the
// existing job promotes them after 7 days.
func (s *Scheduler) listEligible(ctx context.Context, conn *pgx.Conn) ([]uuid.UUID, error) {
	cutoff := s.NowFn().Add(-s.MinAge)
	rows, err := conn.Query(ctx, `
		SELECT s.id
		FROM stations s
		JOIN station_thresholds t ON t.station_id = s.id
		WHERE t.calibration_mode = false
		  AND t.updated_at < $1
		  AND s.monitoring_status IN ('active', 'calibrating')
		ORDER BY t.updated_at ASC
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// recalibrateOne resets a single station to calibration_mode=true so the
// worker starts collecting a fresh noise_samples buffer. The existing
// RunCalibrationJob (already scheduled daily) will then promote it back out
// after MinAge elapses.
//
// This intentionally does NOT touch RunCalibrationJob's logic — it only
// arms a future invocation by flipping calibration_mode.
func (s *Scheduler) recalibrateOne(ctx context.Context, stationID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE station_thresholds
		SET calibration_mode = true,
		    calibration_started_at = NOW(),
		    noise_samples = '{}'::float[],
		    updated_at = NOW()
		WHERE station_id = $1
	`, stationID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("no threshold row for station %s", stationID)
	}
	return nil
}
