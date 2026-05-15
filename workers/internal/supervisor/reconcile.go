package supervisor

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/metrics"
)

// reconcileInterval is how often the supervisor re-reads, per running worker,
// which commercials should be loaded for that station AND the station's
// stream_url, rebuilding the worker when either drifts from what is currently
// loaded.
//
// Background — 2026-05-08 incident (commercials): a commercial whose
// target_stations were edited while a worker was already running could end
// up missing from that worker's CommercialShortIDs list (the list is
// snapshotted on startStationWorker and never re-read). The handler-level
// Reload call that is supposed to cover this path silently swallows errors,
// so any transient failure (DB hiccup, supervisor mid-restart) leaves the
// worker detecting against a stale list — and detections for the new
// commercial are dropped on the floor with no alert.
//
// Background — 2026-05-15 incident (stream_url): the same snapshot pattern
// applied to stations.stream_url. An operator updating an emissora's URL
// via PUT /stations/{id} would write to the DB, but the running worker
// kept feeding the old URL into ffmpeg's reconnect loop forever, never
// producing PCM. Symptom: red dots in /monitoring while the play button in
// /stations (which reads URL live from the DB) kept working.
//
// 30s is the trade-off: short enough that a forgotten/failed Reload or
// edited URL is invisible to the operator for at most one half-cycle, long
// enough that 200 stations × 1 query/30s stays well below 10 QPS on Postgres.
const reconcileInterval = 30 * time.Second

// reconcileReason is the pure decision function the reconciler uses to
// decide whether a worker needs to be restarted, and why. Returns an empty
// string when no restart is needed; otherwise returns a short operator-
// readable reason that is also written to the warn log when the restart
// fires. Keeping this as a pure function lets us cover every combination
// (URL drift, commercials drift, both, neither) without DB or supervisor
// scaffolding in tests.
//
// Defensive: an empty wantedURL is treated as "no restart" rather than
// "restart against empty URL" — stations.stream_url is NOT NULL in the
// schema, so an empty value here would be a query bug or partial result.
// Restarting would produce a worker that fails to start ffmpeg every time.
func reconcileReason(currentURL, wantedURL string, currentIDs, wantedIDs []int32) string {
	urlChanged := wantedURL != "" && currentURL != wantedURL
	idsChanged := !commercialSetEqual(currentIDs, wantedIDs)
	switch {
	case urlChanged && idsChanged:
		return "stream_url and commercial list changed"
	case urlChanged:
		return "stream_url changed"
	case idsChanged:
		return "commercial list changed"
	}
	return ""
}

// commercialSetEqual returns true iff a and b contain the same set of short
// ids (order-independent, duplicate-tolerant). Used by the reconciler to
// decide whether to rebuild the worker.
func commercialSetEqual(a, b []int32) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	// Copy + sort + dedup. The lists are tiny (single-digit length per worker
	// in production), so the allocation cost is negligible compared to the DB
	// round-trip we just paid to obtain them.
	dedup := func(in []int32) []int32 {
		out := make([]int32, len(in))
		copy(out, in)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		w := 0
		for i, v := range out {
			if i == 0 || v != out[w-1] {
				out[w] = v
				w++
			}
		}
		return out[:w]
	}
	da := dedup(a)
	db := dedup(b)
	if len(da) != len(db) {
		return false
	}
	for i := range da {
		if da[i] != db[i] {
			return false
		}
	}
	return true
}

// runWorkerReconciler periodically reconciles the running worker's
// commercial list AND stream URL against the DB and rebuilds the worker
// when they diverge. Mirrors the runStallWatchdog / runThresholdRefresher
// pattern: cancels the current worker context and respawns via
// startStationWorker, then exits (the new worker brings up its own
// reconciler goroutine).
//
// The function exits when ctx is cancelled (worker stopped externally) or
// after it triggers a restart. It deliberately does NOT loop after a
// restart — startStationWorker spawns a fresh reconciler for the new worker.
func (s *Supervisor) runWorkerReconciler(workerCtx context.Context, stationID uuid.UUID) {
	stationLabel := stationID.String()
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-workerCtx.Done():
			return
		case <-ticker.C:
		}

		if s.reconcileOnce(workerCtx, stationID, stationLabel) {
			// Worker was rebuilt — the new instance owns the next reconciler.
			return
		}
	}
}

// reconcileOnce performs a single reconciliation pass. Returns true if the
// worker was scheduled for restart (caller should exit); false if no action
// was taken (transient error, no change, or worker already gone).
func (s *Supervisor) reconcileOnce(ctx context.Context, stationID uuid.UUID, stationLabel string) bool {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Load station to pick up stream_url edits — see reconcileReason and the
	// 2026-05-15 incident comment on reconcileInterval. Errors keep the
	// worker as-is for this cycle (transient DB hiccup must not tear down a
	// healthy worker).
	station, err := s.stations.Get(queryCtx, stationID)
	if err != nil {
		metrics.WorkerReconcileRuns.WithLabelValues(stationLabel, "error").Inc()
		s.log.Warn("supervisor.reconcile: get station failed; keeping worker",
			zap.String("station_id", stationLabel),
			zap.Error(err))
		return false
	}

	activeIDs, err := s.campaigns.ActiveCampaignsForStation(queryCtx, stationID)
	if err != nil {
		metrics.WorkerReconcileRuns.WithLabelValues(stationLabel, "error").Inc()
		s.log.Warn("supervisor.reconcile: active campaigns query failed; keeping worker",
			zap.String("station_id", stationLabel),
			zap.Error(err))
		return false
	}

	coms, err := s.commercials.ListReadyByCampaignsForStation(queryCtx, activeIDs, stationID)
	if err != nil {
		metrics.WorkerReconcileRuns.WithLabelValues(stationLabel, "error").Inc()
		s.log.Warn("supervisor.reconcile: list commercials failed; keeping worker",
			zap.String("station_id", stationLabel),
			zap.Error(err))
		return false
	}
	wantedIDs := make([]int32, 0, len(coms))
	for _, c := range coms {
		wantedIDs = append(wantedIDs, c.ShortID)
	}

	// Append materials linked via campaign_materials. On error we keep the
	// worker as-is for this cycle rather than restarting against a
	// commercials-only wanted list — otherwise a transient materials hiccup
	// would tear down a worker that was correctly loaded with both kinds.
	mats, err := s.materials.ListReadyByCampaignsForStation(queryCtx, activeIDs, stationID)
	if err != nil {
		metrics.WorkerReconcileRuns.WithLabelValues(stationLabel, "error").Inc()
		s.log.Warn("supervisor.reconcile: list materials failed; keeping worker",
			zap.String("station_id", stationLabel),
			zap.Error(err))
		return false
	}
	for _, m := range mats {
		wantedIDs = append(wantedIDs, m.ShortID)
	}

	s.mu.Lock()
	entry, ok := s.workers[stationID]
	if !ok || entry == nil || entry.worker == nil {
		s.mu.Unlock()
		return false // worker already gone — Pause/StopWorkersForCampaign handles it
	}
	currentIDs := entry.worker.CommercialShortIDs()
	currentURL := entry.worker.StreamURL()
	currentCancel := entry.cancel
	s.mu.Unlock()

	reason := reconcileReason(currentURL, station.StreamURL, currentIDs, wantedIDs)
	if reason == "" {
		metrics.WorkerReconcileRuns.WithLabelValues(stationLabel, "unchanged").Inc()
		metrics.WorkerCommercials.WithLabelValues(stationLabel).Set(float64(len(currentIDs)))
		return false
	}

	metrics.WorkerReconcileRuns.WithLabelValues(stationLabel, "restarted").Inc()
	s.log.Warn("supervisor.reconcile: drift detected — restarting worker",
		zap.String("station_id", stationLabel),
		zap.String("reason", reason),
		zap.String("current_stream_url", currentURL),
		zap.String("wanted_stream_url", station.StreamURL),
		zap.Int("current_count", len(currentIDs)),
		zap.Int("wanted_count", len(wantedIDs)),
		zap.Int32s("current_short_ids", currentIDs),
		zap.Int32s("wanted_short_ids", wantedIDs))

	currentCancel()
	s.mu.Lock()
	if cur, stillThere := s.workers[stationID]; stillThere && cur == entry {
		delete(s.workers, stationID)
		s.evidence.Unregister(stationID)
		metrics.WorkerActive.Dec()
	}
	s.mu.Unlock()

	go func() {
		if err := s.startStationWorker(context.Background(), stationID); err != nil {
			s.log.Error("supervisor.reconcile: rebuild failed",
				zap.String("station_id", stationLabel),
				zap.Error(err))
		}
	}()
	return true
}
