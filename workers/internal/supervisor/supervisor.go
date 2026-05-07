package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/events"
	"radiocheck/internal/evidence"
	"radiocheck/internal/index"
	"radiocheck/internal/ingestor"
	"radiocheck/pkg/ringbuffer"
)

const (
	sampleRate = 16000
	hopSize    = 2048
)

// workerEntry holds a running worker and its cancellation function.
type workerEntry struct {
	worker     *ingestor.Worker
	cancel     context.CancelFunc
	lastDownID *int64
	lastDownAt *time.Time
}

// Supervisor manages the lifecycle of stream workers.
// It implements the CampaignSupervisor interface used by the API handlers.
type Supervisor struct {
	db           *pgxpool.Pool
	store        *index.Store
	nc           *nats.Conn
	evidence     *evidence.Service
	campaigns    *catalog.Campaigns
	stations     *catalog.Stations
	commercials  *catalog.Commercials
	healthEvents *catalog.HealthEvents
	log          *zap.Logger

	mu      sync.Mutex
	workers map[uuid.UUID]*workerEntry // stationID → entry
}

// New constructs a Supervisor.
func New(
	db *pgxpool.Pool,
	store *index.Store,
	nc *nats.Conn,
	ev *evidence.Service,
	campaigns *catalog.Campaigns,
	stations *catalog.Stations,
	commercials *catalog.Commercials,
	healthEvents *catalog.HealthEvents,
	log *zap.Logger,
) *Supervisor {
	return &Supervisor{
		db:           db,
		store:        store,
		nc:           nc,
		evidence:     ev,
		campaigns:    campaigns,
		stations:     stations,
		commercials:  commercials,
		healthEvents: healthEvents,
		log:          log,
		workers:      make(map[uuid.UUID]*workerEntry),
	}
}

// totalFrames converts a commercial's duration in seconds to a frame count
// using the fingerprinting constants (sampleRate / hopSize).
func totalFrames(durationSeconds float64) int {
	return int(durationSeconds * float64(sampleRate) / float64(hopSize))
}

// Start launches workers for all stations targeted by the given campaign.
// It is idempotent: calling Start on an already-active campaign replaces
// existing workers so the commercial list is always up to date.
func (s *Supervisor) Start(campaignID uuid.UUID) error {
	ctx := context.Background()

	// 1. Load campaign.
	camp, err := s.campaigns.Get(ctx, campaignID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("supervisor: campaign not found: %s", campaignID)
		}
		return fmt.Errorf("supervisor: get campaign: %w", err)
	}

	// 2. Update campaign status to active BEFORE starting workers so that
	// ActiveCampaignsForStation (called inside startStationWorker) finds it.
	if err := s.campaigns.UpdateStatus(ctx, campaignID, "active"); err != nil {
		return fmt.Errorf("supervisor: update campaign status: %w", err)
	}

	// 3. Trigger index reload for all ready commercials in this campaign.
	// Handles the case where fingerprints were generated before the campaign was activated
	// (the index.reload handler requires ca.status = 'active', which is now satisfied).
	if coms, err := s.commercials.ListReadyByCampaigns(ctx, []uuid.UUID{campaignID}); err == nil {
		for _, c := range coms {
			payload, _ := json.Marshal(map[string]string{"commercial_id": c.ID.String()})
			if err := s.nc.Publish(events.SubjectIndexReload, payload); err != nil {
				s.log.Warn("supervisor: index reload publish failed",
					zap.String("commercial_id", c.ID.String()),
					zap.Error(err),
				)
			}
		}
	}

	// 4. For each station in campaign.TargetStations, start/replace worker.
	for _, stationID := range camp.TargetStations {
		if err := s.startStationWorker(ctx, stationID); err != nil {
			s.log.Error("supervisor: failed to start worker for station",
				zap.String("station_id", stationID.String()),
				zap.Error(err),
			)
		}
	}

	// 5. Update monitoring_status for all targeted stations.
	for _, stationID := range camp.TargetStations {
		if err := s.stations.UpdateMonitoringStatus(ctx, stationID, "active"); err != nil {
			s.log.Warn("supervisor: update station monitoring_status failed",
				zap.String("station_id", stationID.String()),
				zap.Error(err),
			)
		}
	}

	s.log.Info("supervisor: campaign started",
		zap.String("campaign_id", campaignID.String()),
		zap.Int("stations", len(camp.TargetStations)),
	)
	return nil
}

// startStationWorker builds and starts (or replaces) the worker for stationID,
// using all ready commercials from every active campaign targeting that station.
func (s *Supervisor) startStationWorker(ctx context.Context, stationID uuid.UUID) error {
	// a. Load station from DB (to get StreamURL, ShortID).
	station, err := s.stations.Get(ctx, stationID)
	if err != nil {
		return fmt.Errorf("get station: %w", err)
	}

	// b. Find all currently-active campaigns for this station.
	activeCampaignIDs, err := s.campaigns.ActiveCampaignsForStation(ctx, stationID)
	if err != nil {
		return fmt.Errorf("active campaigns for station: %w", err)
	}

	// c. Load ready commercials for those campaigns that target this station.
	// A commercial with empty target_stations runs on all campaign stations.
	coms, err := s.commercials.ListReadyByCampaignsForStation(ctx, activeCampaignIDs, stationID)
	if err != nil {
		return fmt.Errorf("list ready commercials: %w", err)
	}

	// d. Build WorkerConfig.
	shortIDs := make([]int32, 0, len(coms))
	frames := make(map[int32]int, len(coms))
	for _, c := range coms {
		shortIDs = append(shortIDs, c.ShortID)
		frames[c.ShortID] = totalFrames(c.DurationSeconds)
	}

	// e. Stop existing worker for this station if running.
	s.mu.Lock()
	if old, ok := s.workers[stationID]; ok {
		old.cancel()
		delete(s.workers, stationID)
		s.evidence.Unregister(stationID)
	}
	s.mu.Unlock()

	// f. Create new context with cancel.
	workerCtx, cancel := context.WithCancel(ctx)

	// g. Create AAC ring buffer for evidence (~5 min of ~1 chunk/100ms).
	aacBuf := ringbuffer.NewByteRing(3000)

	capturedStationID := stationID

	// ── Startup recovery: find open 'down' event from a previous crash ──────
	entry := &workerEntry{cancel: cancel}
	if ev, err := s.healthEvents.GetLastOpenDown(context.Background(), capturedStationID); err == nil {
		entry.lastDownID = &ev.ID
		entry.lastDownAt = &ev.EventAt
		s.log.Info("supervisor: startup recovery — found open down event",
			zap.String("station_id", capturedStationID.String()),
			zap.Time("event_at", ev.EventAt),
		)
	}

	// Store entry in map NOW so callbacks can find it (worker starts below).
	s.mu.Lock()
	s.workers[capturedStationID] = entry
	s.mu.Unlock()

	// ── Heartbeat ────────────────────────────────────────────────────────────
	heartbeatFn := func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.stations.UpdateHealthCheck(ctx, capturedStationID); err != nil {
				s.log.Warn("supervisor: heartbeat update failed",
					zap.String("station_id", capturedStationID.String()),
					zap.Error(err),
				)
			}
		}()
	}

	// ── Stream up callback ────────────────────────────────────────────────────
	onStreamUp := func() {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			s.mu.Lock()
			e, ok := s.workers[capturedStationID]
			var downID *int64
			var downAt *time.Time
			if ok {
				downID = e.lastDownID
				downAt = e.lastDownAt
			}
			s.mu.Unlock()

			if ok && downID != nil && downAt != nil {
				dur := int(time.Since(*downAt).Seconds())
				if err := s.healthEvents.UpdateDownDuration(bgCtx, *downID, *downAt, dur); err != nil {
					s.log.Warn("supervisor: update down duration failed",
						zap.String("station_id", capturedStationID.String()),
						zap.Error(err),
					)
				}
			}

			if err := s.healthEvents.RecordUp(bgCtx, capturedStationID); err != nil {
				s.log.Warn("supervisor: record up failed",
					zap.String("station_id", capturedStationID.String()),
					zap.Error(err),
				)
			}

			// Clear the tracked down event — outage is resolved.
			s.mu.Lock()
			if e, ok := s.workers[capturedStationID]; ok {
				e.lastDownID = nil
				e.lastDownAt = nil
			}
			s.mu.Unlock()
		}()
	}

	// ── Stream down callback ─────────────────────────────────────────────────
	onStreamDown := func() {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			id, at, err := s.healthEvents.RecordDown(bgCtx, capturedStationID)
			if err != nil {
				s.log.Warn("supervisor: record down failed",
					zap.String("station_id", capturedStationID.String()),
					zap.Error(err),
				)
				return
			}

			s.mu.Lock()
			if e, ok := s.workers[capturedStationID]; ok {
				e.lastDownID = &id
				e.lastDownAt = &at
			}
			s.mu.Unlock()
		}()
	}

	cfg := ingestor.WorkerConfig{
		StationID:           station.ID,
		StreamURL:           station.StreamURL,
		CommercialShortIDs:  shortIDs,
		CommercialFrames:    frames,
		MatchThreshold:      3,
		MinScoreCoverage:    0.05,
		MinTemporalCoverage: 0.15,
		ConfirmTimeout:      30 * time.Second,
		AACBuffer:           aacBuf,
		HeartbeatFn:         heartbeatFn,
		OnStreamUp:          onStreamUp,
		OnStreamDown:        onStreamDown,
	}
	w := ingestor.NewWorker(cfg, s.store, s.nc, s.log)
	entry.worker = w

	// Register ByteRing with evidence service before worker starts.
	s.evidence.Register(stationID, aacBuf)

	// Start goroutine (entry was already stored in the map above).
	go w.Run(workerCtx)

	s.log.Info("supervisor: worker started",
		zap.String("station_id", stationID.String()),
		zap.String("stream_url", station.StreamURL),
		zap.Int("commercials", len(shortIDs)),
	)
	return nil
}

// Pause stops workers for stations that have no other active campaign after
// this campaign is paused.
func (s *Supervisor) Pause(campaignID uuid.UUID) error {
	ctx := context.Background()

	// 1. Load campaign.
	camp, err := s.campaigns.Get(ctx, campaignID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("supervisor: campaign not found: %s", campaignID)
		}
		return fmt.Errorf("supervisor: get campaign: %w", err)
	}

	// 2. For each station in the campaign, check if another active campaign
	// still targets it.
	var stationsToPause []uuid.UUID

	for _, stationID := range camp.TargetStations {
		// ActiveCampaignsForStation returns campaigns with status = 'active'.
		// The current campaign is still 'active' at this point (we haven't
		// updated its status yet), so we need to exclude it from the check.
		activeCampaignIDs, err := s.campaigns.ActiveCampaignsForStation(ctx, stationID)
		if err != nil {
			s.log.Warn("supervisor: active campaigns query failed",
				zap.String("station_id", stationID.String()),
				zap.Error(err),
			)
			continue
		}

		// Check if any OTHER campaign (not the one being paused) is active for this station.
		otherActive := false
		for _, id := range activeCampaignIDs {
			if id != campaignID {
				otherActive = true
				break
			}
		}

		if !otherActive {
			// b. No other active campaign — stop the worker.
			s.mu.Lock()
			if entry, ok := s.workers[stationID]; ok {
				entry.cancel()
				delete(s.workers, stationID)
				s.evidence.Unregister(stationID)
			}
			s.mu.Unlock()
			stationsToPause = append(stationsToPause, stationID)
		}
		// c. If another active campaign uses this station, leave worker running.
	}

	// 3. Update campaign status to paused.
	if err := s.campaigns.UpdateStatus(ctx, campaignID, "paused"); err != nil {
		return fmt.Errorf("supervisor: update campaign status: %w", err)
	}

	// 4. Update monitoring_status for stations with no more active campaigns.
	for _, stationID := range stationsToPause {
		if err := s.stations.UpdateMonitoringStatus(ctx, stationID, "paused"); err != nil {
			s.log.Warn("supervisor: update station monitoring_status failed",
				zap.String("station_id", stationID.String()),
				zap.Error(err),
			)
		}
	}

	s.log.Info("supervisor: campaign paused",
		zap.String("campaign_id", campaignID.String()),
		zap.Int("stations_paused", len(stationsToPause)),
	)
	return nil
}

// Reload rebuilds workers for all stations of a campaign without changing its status.
// It is a no-op if the campaign is not currently active.
// Used when campaign stations or commercial station assignments change while active.
func (s *Supervisor) Reload(campaignID uuid.UUID) error {
	ctx := context.Background()
	camp, err := s.campaigns.Get(ctx, campaignID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("supervisor.Reload: get campaign: %w", err)
	}
	if camp.Status != "active" {
		return nil
	}
	for _, stationID := range camp.TargetStations {
		if err := s.startStationWorker(ctx, stationID); err != nil {
			s.log.Warn("supervisor.Reload: failed to restart worker",
				zap.String("station_id", stationID.String()),
				zap.Error(err),
			)
		}
	}
	return nil
}

// RestoreActive re-launches workers for all campaigns with status 'active'.
// Called once at startup after the index loader finishes, so workers resume
// after a process restart or crash.
func (s *Supervisor) RestoreActive(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM campaigns WHERE status = 'active'
	`)
	if err != nil {
		return fmt.Errorf("supervisor.RestoreActive: query: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("supervisor.RestoreActive: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("supervisor.RestoreActive: iterate: %w", err)
	}

	for _, id := range ids {
		if err := s.Start(id); err != nil {
			s.log.Error("supervisor.RestoreActive: failed to start campaign",
				zap.String("campaign_id", id.String()),
				zap.Error(err),
			)
		}
	}

	s.log.Info("supervisor.RestoreActive: done", zap.Int("campaigns", len(ids)))
	return nil
}
