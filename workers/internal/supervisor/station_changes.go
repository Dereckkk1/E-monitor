package supervisor

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"radiocheck/internal/metrics"
)

// diffStations partitions oldS and newS into three sets used by
// UpdateStations to decide which workers to start, restart or stop.
//
//	removed = oldS \ newS  (worker may need to be stopped)
//	kept    = oldS ∩ newS  (worker stays; gets restarted to refresh commercials)
//	added   = newS \ oldS  (fresh worker)
//
// Order in inputs is irrelevant; outputs are deduplicated but unsorted —
// callers that care about determinism (tests) must sort themselves.
func diffStations(oldS, newS []uuid.UUID) (removed, kept, added []uuid.UUID) {
	oldSet := make(map[uuid.UUID]struct{}, len(oldS))
	for _, id := range oldS {
		oldSet[id] = struct{}{}
	}
	newSet := make(map[uuid.UUID]struct{}, len(newS))
	for _, id := range newS {
		newSet[id] = struct{}{}
	}
	for id := range oldSet {
		if _, ok := newSet[id]; ok {
			kept = append(kept, id)
		} else {
			removed = append(removed, id)
		}
	}
	for id := range newSet {
		if _, ok := oldSet[id]; !ok {
			added = append(added, id)
		}
	}
	return
}

// UpdateStations is the supervisor-side counterpart of the
// PUT /campaigns/{id}/stations handler. Replaces target_stations on a campaign
// and reconciles the worker fleet so the change takes effect immediately,
// WITHOUT bouncing the campaign through 'cancelada'.
//
// Pre-2026-05-08 the handler did Pause(id) + UpdateTargetStations + Start(id).
// That dance flipped status to 'cancelada' between Pause and Start — visible
// to any concurrent reader (lifecycle scheduler, dashboards, audit subscribers)
// during the gap, and any failure between the two calls left the campaign
// permanently cancelled. UpdateStations replaces that with a single atomic
// path: DB update first, then incrementally start/stop workers based on the
// diff.
//
// Behaviour:
//   - Campaign not in 'ativa' status → DB update only; no worker changes.
//   - Stations removed and uncovered by any other active campaign → worker stopped.
//   - Stations kept → worker is restarted via startStationWorker (idempotent;
//     refreshes the commercial list, which may also have changed).
//   - Stations added → fresh worker via startStationWorker.
//
// Returns the first error encountered. DB update failure is fatal; per-worker
// failures are logged and accumulated but don't abort the loop, mirroring the
// loose semantics of Start (best-effort on a per-station basis).
func (s *Supervisor) UpdateStations(campaignID uuid.UUID, newStations []uuid.UUID) error {
	ctx := context.Background()

	camp, err := s.campaigns.Get(ctx, campaignID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("supervisor.UpdateStations: campaign not found: %s", campaignID)
		}
		return fmt.Errorf("supervisor.UpdateStations: get campaign: %w", err)
	}

	if newStations == nil {
		newStations = []uuid.UUID{}
	}

	// Update DB FIRST so any subsequent ListReady...ForStation call sees the
	// fresh target_stations. The supervisor walks workers below using the
	// post-update view.
	if err := s.campaigns.UpdateTargetStations(ctx, campaignID, newStations); err != nil {
		return fmt.Errorf("supervisor.UpdateStations: update target_stations: %w", err)
	}

	// If campaign isn't active, nothing more to do — no workers to manage.
	if camp.Status != "ativa" {
		s.log.Info("supervisor.UpdateStations: campaign not active; DB-only update",
			zap.String("campaign_id", campaignID.String()),
			zap.String("status", camp.Status),
			zap.Int("stations", len(newStations)),
		)
		return nil
	}

	removed, kept, added := diffStations(camp.TargetStations, newStations)

	// 1. Stop workers for stations removed from this campaign IF no other
	// active campaign still covers them. Mirrors Pause's worker-stop path.
	for _, stationID := range removed {
		activeIDs, err := s.campaigns.ActiveCampaignsForStation(ctx, stationID)
		if err != nil {
			s.log.Warn("supervisor.UpdateStations: active campaigns query failed; leaving worker as-is",
				zap.String("station_id", stationID.String()),
				zap.Error(err))
			continue
		}
		// Filter out the campaign we're editing — its row was already updated
		// above so it should no longer reference stationID, but be defensive.
		stillCovered := false
		for _, id := range activeIDs {
			if id != campaignID {
				stillCovered = true
				break
			}
		}
		if stillCovered {
			// Another active campaign uses this station; must NOT stop its worker.
			// But the kept campaign's commercial list for this station may now
			// differ — let the reconciler catch up on the next tick (≤30s).
			continue
		}
		s.mu.Lock()
		if entry, ok := s.workers[stationID]; ok {
			entry.cancel()
			delete(s.workers, stationID)
			s.evidence.Unregister(stationID)
			metrics.WorkerActive.Dec()
			metrics.WorkerCommercials.DeleteLabelValues(stationID.String())
		}
		s.mu.Unlock()
		if err := s.stations.UpdateMonitoringStatus(ctx, stationID, "paused"); err != nil {
			s.log.Warn("supervisor.UpdateStations: update station monitoring_status failed",
				zap.String("station_id", stationID.String()),
				zap.Error(err))
		}
	}

	// 2. Start/restart workers for kept + added stations. startStationWorker
	// replaces any existing worker, so it's safe to call uniformly.
	for _, stationID := range append(kept, added...) {
		if err := s.startStationWorker(ctx, stationID); err != nil {
			s.log.Error("supervisor.UpdateStations: start worker failed",
				zap.String("campaign_id", campaignID.String()),
				zap.String("station_id", stationID.String()),
				zap.Error(err))
		}
	}

	// 3. Refresh monitoring_status for active stations (Start does this too,
	// but we bypass Start here).
	for _, stationID := range append(kept, added...) {
		if err := s.stations.UpdateMonitoringStatus(ctx, stationID, "active"); err != nil {
			s.log.Warn("supervisor.UpdateStations: update station monitoring_status failed",
				zap.String("station_id", stationID.String()),
				zap.Error(err))
		}
	}

	s.log.Info("supervisor.UpdateStations: applied",
		zap.String("campaign_id", campaignID.String()),
		zap.Int("removed", len(removed)),
		zap.Int("kept", len(kept)),
		zap.Int("added", len(added)),
	)
	return nil
}
