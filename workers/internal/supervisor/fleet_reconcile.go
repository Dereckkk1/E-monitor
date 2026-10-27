package supervisor

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/metrics"
)

// fleetReconcileInterval is how often the fleet reconciler compares "stations
// covered by an 'ativa' campaign" against the supervisor's worker map.
//
// Background — the per-worker reconciler (reconcile.go) only watches workers
// that EXIST. Nothing re-created a worker that was missing: a startStationWorker
// that failed (Start, stall-watchdog respawn, reconcile rebuild — all just log
// the error) or a campaign moved to 'ativa' by direct SQL left the station
// unmonitored until the next API restart, with no self-healing. The same blind
// spot let stations.monitoring_status drift (incident 2026-10-02: 80 stations
// 'active' without any campaign, read as "drift do reconciler" in /admin/overview).
//
// 2 min × 2 passes of grace (see planFleetStarts) bounds a missing worker at
// ~4 min, against one cheap query per pass.
const fleetReconcileInterval = 2 * time.Minute

// planFleetStarts is the pure decision of the fleet reconciler. A station that
// must have a worker (desired) but has no entry in the supervisor map (present)
// is "missing"; it is only started when it was ALSO missing on the previous
// pass. One pass of grace keeps the reconciler from racing a Start() or a
// stall-watchdog respawn that is mid-flight — two concurrent
// startStationWorker calls for the same station would leave an orphan ffmpeg
// outside the map.
//
// Stations parked by the connect circuit breaker have a placeholder entry, so
// they count as present: their respawn belongs to the breaker. Present but not
// desired (orphans) are not handled here — the per-worker reconciler stops
// those (stopOrphanWorker).
func planFleetStarts(desired []uuid.UUID, present, prevMissing map[uuid.UUID]struct{}) (toStart []uuid.UUID, missing map[uuid.UUID]struct{}) {
	missing = make(map[uuid.UUID]struct{})
	for _, id := range desired {
		if _, ok := present[id]; ok {
			continue
		}
		missing[id] = struct{}{}
		if _, wasMissing := prevMissing[id]; wasMissing {
			toStart = append(toStart, id)
		}
	}
	return toStart, missing
}

// StartFleetReconciler runs the fleet reconciler in a goroutine until ctx is
// cancelled. Call once, after RestoreActive.
func (s *Supervisor) StartFleetReconciler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(fleetReconcileInterval)
		defer ticker.Stop()
		missing := map[uuid.UUID]struct{}{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			missing = s.reconcileFleetOnce(ctx, missing, s.startStationWorker)
		}
	}()
}

// reconcileFleetOnce runs one pass: starts workers missing for two passes in a
// row (via start — startStationWorker in production) and syncs
// stations.monitoring_status with the 'ativa' campaigns. Returns the stations
// missing on this pass, to be fed back as prevMissing on the next one. On a
// query error the previous state is kept and nothing is started.
func (s *Supervisor) reconcileFleetOnce(
	ctx context.Context,
	prevMissing map[uuid.UUID]struct{},
	start func(context.Context, uuid.UUID) error,
) map[uuid.UUID]struct{} {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	desired, err := s.campaigns.StationsCoveredByActive(queryCtx)
	if err != nil {
		s.log.Warn("supervisor.fleet: covered stations query failed; skipping pass", zap.Error(err))
		return prevMissing
	}

	s.mu.Lock()
	present := make(map[uuid.UUID]struct{}, len(s.workers))
	for id := range s.workers {
		present[id] = struct{}{}
	}
	s.mu.Unlock()

	toStart, missing := planFleetStarts(desired, present, prevMissing)
	for _, stationID := range toStart {
		s.log.Warn("supervisor.fleet: station covered by an active campaign has no worker — starting it",
			zap.String("station_id", stationID.String()))
		if err := start(context.Background(), stationID); err != nil {
			metrics.FleetReconcileActions.WithLabelValues("start_failed").Inc()
			s.log.Error("supervisor.fleet: start missing worker failed; retrying next pass",
				zap.String("station_id", stationID.String()),
				zap.Error(err))
			continue
		}
		metrics.FleetReconcileActions.WithLabelValues("started").Inc()
	}

	paused, activated, err := s.stations.SyncMonitoringStatus(queryCtx)
	if err != nil {
		s.log.Warn("supervisor.fleet: monitoring_status sync failed", zap.Error(err))
	}
	if paused > 0 || activated > 0 {
		metrics.FleetReconcileActions.WithLabelValues("status_paused").Add(float64(paused))
		metrics.FleetReconcileActions.WithLabelValues("status_activated").Add(float64(activated))
		s.log.Warn("supervisor.fleet: monitoring_status drift fixed",
			zap.Int64("set_paused", paused),
			zap.Int64("set_active", activated))
	}
	return missing
}
