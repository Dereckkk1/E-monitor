package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"radiocheck/internal/calibration"
	"radiocheck/internal/catalog"
	"radiocheck/internal/events"
	"radiocheck/internal/evidence"
	"radiocheck/internal/index"
	"radiocheck/internal/ingestor"
	"radiocheck/internal/metrics"
	"radiocheck/internal/observability"
	"radiocheck/internal/segments"
)

const (
	sampleRate = 16000
	hopSize    = 2048
	// defaultMatchThreshold is used when station_thresholds has no row for the
	// station yet (or the lookup fails). 5 mirrors the floor used by the
	// calibration job (`max(noise_p99 * 1.5, 5)`).
	defaultMatchThreshold = 5
	// thresholdRefreshInterval is how often the supervisor re-reads
	// station_thresholds for each running worker. Must be longer than the
	// calibration job's cadence (daily) but short enough that operator-
	// initiated SQL tweaks propagate without a worker restart.
	thresholdRefreshInterval = 5 * time.Minute
)

// workerEntry holds a running worker and its cancellation function,
// plus the open down-event identity (for stream-health idempotency).
type workerEntry struct {
	worker     *ingestor.Worker
	cancel     context.CancelFunc
	lastDownID *int64
	lastDownAt *time.Time
	// startedAt is when the worker goroutine was launched. Read by the stall
	// watchdog to detect "never produced PCM since start" — see isStalled.
	// Zero until startStationWorker finalises the entry.
	startedAt time.Time
	// refreshNow signals the per-worker threshold refresh goroutine to
	// re-read station_thresholds immediately (used by RefreshThreshold).
	// Buffered (cap 1) so a signal is never lost and never blocks.
	refreshNow chan struct{}
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
	materials    *catalog.Materials
	healthEvents *catalog.HealthEvents
	log          *zap.Logger

	// segmentsRoot is the directory under which each station gets a
	// per-station subdir for ffmpeg's segment muxer output. See
	// docs/evidence-segments.md.
	segmentsRoot string

	mu                 sync.Mutex
	workers            map[uuid.UUID]*workerEntry // stationID → entry
	lastStallRestart   map[uuid.UUID]time.Time    // stationID → last stall-induced restart time
	stallRestartCounts map[uuid.UUID]uint32       // stationID → cumulative stall restarts (survives worker recreation)
	// connectFailures counts CONSECUTIVE never-connected restarts per station
	// (flavor B stall). Feeds the circuit-breaker backoff (connect_backoff.go);
	// reset to 0 by onStreamUp the moment a station produces PCM again.
	connectFailures map[uuid.UUID]uint32

	// Lifecycle (§18.2.1). Optional: nil when not configured.
	lifecycle *LifecycleScheduler

	// Version disambiguation (§18.2.2). The buffer keeps the last 60s of
	// confirmed publications keyed by (station_id, client_id) so duplicate
	// cuts of the same jingle don't both get published.
	//
	// §18.2.2-v2 (coverage-based reattribution) lives entirely in the evidence
	// audit stage, NOT here — the supervisor's suppress/retract path is
	// unchanged. See evidence.reattributeByCoverage.
	dedupBuffer *DedupBuffer
}

// dedupBufferRetention is how far back the supervisor keeps prior publications
// to detect overlap with newer confirmations (§18.2.2).
const dedupBufferRetention = 60 * time.Second

// New constructs a Supervisor.
//
// segmentsRoot is the directory under which each station's worker creates a
// subdir for ffmpeg's segment muxer to write evidence into. The directory
// must exist and be writable; the supervisor creates per-station subdirs on
// demand.
func New(
	db *pgxpool.Pool,
	store *index.Store,
	nc *nats.Conn,
	ev *evidence.Service,
	campaigns *catalog.Campaigns,
	stations *catalog.Stations,
	commercials *catalog.Commercials,
	materials *catalog.Materials,
	healthEvents *catalog.HealthEvents,
	segmentsRoot string,
	log *zap.Logger,
) *Supervisor {
	return &Supervisor{
		db:                 db,
		store:              store,
		nc:                 nc,
		evidence:           ev,
		campaigns:          campaigns,
		stations:           stations,
		commercials:        commercials,
		materials:          materials,
		healthEvents:       healthEvents,
		segmentsRoot:       segmentsRoot,
		log:                log,
		workers:            make(map[uuid.UUID]*workerEntry),
		lastStallRestart:   make(map[uuid.UUID]time.Time),
		stallRestartCounts: make(map[uuid.UUID]uint32),
		connectFailures:    make(map[uuid.UUID]uint32),
		dedupBuffer:        NewDedupBuffer(dedupBufferRetention),
	}
}

// totalFrames converts a commercial's duration in seconds to a frame count
// using the fingerprinting constants (sampleRate / hopSize).
func totalFrames(durationSeconds float64) int {
	return int(durationSeconds * float64(sampleRate) / float64(hopSize))
}

// computePreventiveRestartDelay calculates time until next 3–5 AM window restart.
// Each call returns a different random value within the 2-hour window.
// Uses local time (TZ should be set to America/Sao_Paulo at process level).
func computePreventiveRestartDelay() time.Duration {
	offset := time.Duration(rand.Int63n(int64(2 * time.Hour))) // random 0–2h
	base := 3 * time.Hour
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	next := midnight.Add(24*time.Hour + base + offset)
	if next.Before(now) {
		next = next.Add(24 * time.Hour)
	}
	return time.Until(next)
}

// schedulePreventiveRestart runs in a goroutine and performs a graceful restart
// of the given station's worker once per day in the 3–5 AM window (§8.7).
// It exits after the restart fires (the new worker will spawn its own goroutine).
func (s *Supervisor) schedulePreventiveRestart(ctx context.Context, stationID uuid.UUID) {
	delay := computePreventiveRestartDelay()
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}
	s.log.Info("supervisor: preventive restart",
		zap.String("station_id", stationID.String()),
		zap.Duration("scheduled_delay", delay))
	s.mu.Lock()
	entry, ok := s.workers[stationID]
	s.mu.Unlock()
	if !ok {
		return // worker was stopped externally
	}
	entry.cancel()
	if err := s.startStationWorker(context.Background(), stationID); err != nil {
		s.log.Error("supervisor: preventive restart failed",
			zap.String("station_id", stationID.String()),
			zap.Error(err))
	}
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

	// 2. Update campaign status to ativa BEFORE starting workers so that
	// ActiveCampaignsForStation (called inside startStationWorker) finds it.
	if err := s.campaigns.UpdateStatus(ctx, campaignID, "ativa"); err != nil {
		return fmt.Errorf("supervisor: update campaign status: %w", err)
	}

	// 3. Trigger index reload for this campaign's ready spots (materials +
	// legacy commercials) so fingerprints generated before activation enter the
	// in-memory index now that the campaign is index-eligible.
	s.publishIndexReloadForCampaign(ctx, campaignID)

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
	ctx, span := observability.Tracer().Start(ctx, "supervisor.start_worker")
	span.SetAttributes(attribute.String("station_id", stationID.String()))
	defer span.End()
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

	// c2. Load ready materials linked to those campaigns that target this station.
	// Materials whose UUID also exists in commercials are excluded by the
	// catalog query — those go through the commercials path above.
	mats, err := s.materials.ListReadyByCampaignsForStation(ctx, activeCampaignIDs, stationID)
	if err != nil {
		return fmt.Errorf("list ready materials: %w", err)
	}

	// d. Build WorkerConfig.
	shortIDs := make([]int32, 0, len(coms)+len(mats))
	frames := make(map[int32]int, len(coms)+len(mats))
	for _, c := range coms {
		shortIDs = append(shortIDs, c.ShortID)
		frames[c.ShortID] = totalFrames(c.DurationSeconds)
	}
	for _, m := range mats {
		// The worker doesn't care whether short_id originated from commercials
		// or materials — it just uses it as the match key against the unified
		// in-memory index.
		shortIDs = append(shortIDs, m.ShortID)
		frames[m.ShortID] = totalFrames(m.DurationSeconds)
	}

	// e. Stop existing worker for this station if running. The entry may be a
	// backoff placeholder (worker==nil) parked by the stall watchdog — cancel it
	// (aborts the pending respawn timer) but only decrement WorkerActive for a
	// real running worker, since the placeholder never incremented it.
	s.mu.Lock()
	if old, ok := s.workers[stationID]; ok {
		old.cancel()
		delete(s.workers, stationID)
		s.evidence.Unregister(stationID)
		if old.worker != nil {
			metrics.WorkerActive.Dec()
		}
	}
	s.mu.Unlock()

	// f. Create new context with cancel.
	workerCtx, cancel := context.WithCancel(ctx)

	// g. Per-station segment dir for ffmpeg's evidence output. ffmpeg writes
	//    rotating ADTS files there; the evidence service reads them back at
	//    detection time. Created here so the dir is guaranteed to exist
	//    before ffmpeg starts.
	segmentsDir := segments.DirFor(s.segmentsRoot, stationID)
	if err := os.MkdirAll(segmentsDir, 0o755); err != nil {
		cancel()
		return fmt.Errorf("supervisor: mkdir segments dir %s: %w", segmentsDir, err)
	}
	segmentsPattern := segments.FFmpegOutputPattern(s.segmentsRoot, stationID)

	capturedStationID := stationID

	// ── Startup recovery: find open 'down' event from a previous crash ──────
	// Antes de adotar o último open, fecha quaisquer zumbis (open downs além
	// do mais recente). Defesa contra estado pré-existente bagunçado: se algum
	// crash/UPDATE-falho anterior deixou múltiplos opens, normalizamos pra
	// preservar a invariante "≤1 open down event por station" no DB.
	// Incidente 2026-05-18: Mix 93.70 FM acumulou 17 zumbis em 3 dias.
	entry := &workerEntry{cancel: cancel, refreshNow: make(chan struct{}, 1)}
	if closed, err := s.healthEvents.CloseOrphanedOpenDowns(context.Background(), capturedStationID); err != nil {
		s.log.Warn("supervisor: startup recovery — failed to close orphaned downs",
			zap.String("station_id", capturedStationID.String()),
			zap.Error(err),
		)
	} else if closed > 0 {
		s.log.Info("supervisor: startup recovery — closed orphan down events",
			zap.String("station_id", capturedStationID.String()),
			zap.Int("count", closed),
		)
	}
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
	metrics.WorkerActive.Inc()

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
			// Connected: clear any circuit-breaker backoff for this station so a
			// recovered / allow-listed stream is back to normal restart cadence
			// immediately (see connect_backoff.go).
			s.mu.Lock()
			hadBackoff := s.connectFailures[capturedStationID] > 0
			delete(s.connectFailures, capturedStationID)
			s.mu.Unlock()
			if hadBackoff {
				metrics.WorkerConnectBackoff.WithLabelValues(capturedStationID.String()).Set(0)
			}

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

			// IMPORTANTE: só limpa o lastDownID em memória se o UPDATE no DB
			// teve sucesso. Se UpdateDownDuration falhar (DB timeout, conexão
			// perdida) e limpássemos mesmo assim, o próximo onStreamDown não
			// veria event aberto na memória e criaria um NOVO event — o
			// anterior viraria zumbi pra sempre. Esse é o bug raiz do
			// incidente 2026-05-18 (Mix 93.70 FM acumulou 17 zumbis em 3 dias).
			updatedOK := true
			if ok && downID != nil && downAt != nil {
				dur := int(time.Since(*downAt).Seconds())
				if err := s.healthEvents.UpdateDownDuration(bgCtx, *downID, *downAt, dur); err != nil {
					updatedOK = false
					s.log.Warn("supervisor: update down duration failed; keeping lastDownID to retry on next up cycle",
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

			// Clear the tracked down event — outage is resolved. Skip if the
			// duration update failed: retain lastDownID so the next onStreamDown
			// stays idempotent and the next onStreamUp can retry the UPDATE.
			if updatedOK {
				s.mu.Lock()
				if e, ok := s.workers[capturedStationID]; ok {
					e.lastDownID = nil
					e.lastDownAt = nil
				}
				s.mu.Unlock()
			}
		}()
	}

	// ── Stream down callback ─────────────────────────────────────────────────
	// Idempotent per outage: if the worker already has an open down event
	// (lastDownID set), reconnect-loop callbacks are no-ops. A new event is
	// only recorded after the worker successfully comes up (which clears
	// lastDownID via onStreamUp) and then drops again.
	onStreamDown := func() {
		s.recordStreamDown(capturedStationID)
	}

	// ── Resolve dynamic threshold from station_thresholds (§9.4) ───────────
	// Fallback to defaultMatchThreshold (5) when no row exists yet or the
	// query errors — matches the calibration job's floor of 5. This atomic
	// is shared with the worker; the periodic refresh goroutine (below)
	// updates it in place so an in-flight worker picks up new values
	// without restart.
	threshold := defaultMatchThreshold
	if v, err := s.stations.GetThreshold(ctx, capturedStationID); err == nil {
		threshold = v
	} else {
		s.log.Warn("supervisor: threshold lookup failed; using default",
			zap.Stringer("station_id", capturedStationID),
			zap.Int("default", defaultMatchThreshold),
			zap.Error(err),
		)
	}
	thresholdAtomic := ingestor.NewMatchThreshold(threshold)
	metrics.StationThreshold.WithLabelValues(capturedStationID.String()).Set(float64(threshold))

	// ── Calibration noise sampling pipeline ────────────────────────────────
	// The worker emits the histogram peak across all commercials roughly
	// every 10 seconds. We funnel those samples through a buffered channel
	// to a drainer goroutine that does the actual DB write — this keeps a
	// slow Postgres response from ever stalling the matcher. If the channel
	// fills (drainer falling behind / DB hiccup) we drop samples; the 5000-
	// row cap on noise_samples means losing a few does not affect the p99
	// the calibration job eventually computes.
	noiseCh := make(chan int, 32)
	go func() {
		for {
			select {
			case <-workerCtx.Done():
				return
			case score := <-noiseCh:
				bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := calibration.RecordNoiseSample(bgCtx, s.db, capturedStationID, score); err != nil {
					s.log.Warn("calibration: record sample failed",
						zap.Stringer("station_id", capturedStationID),
						zap.Error(err),
					)
				}
				cancel()
			}
		}
	}()
	onNoiseSample := func(score int) {
		select {
		case noiseCh <- score:
		default:
			// Drop sample when the buffer is full — preserves the matcher's
			// real-time guarantee at the cost of a few missing data points
			// during DB pressure.
		}
	}

	cfg := ingestor.WorkerConfig{
		StationID:          station.ID,
		StreamURL:          station.StreamURL,
		CommercialShortIDs: shortIDs,
		CommercialFrames:   frames,
		MatchThreshold:     thresholdAtomic,
		// 0.02 = score >= 2% dos hashes da janela. Massa Joinville 10:43 mostrou
		// match real sustentado por 30s com pico 20 e vários frames 8-16 que o
		// 0.05 anterior rejeitava. Threshold absoluto (vindo de station_thresholds,
		// default 5) e MinTemporalCoverage (0.15 = 4.5s sustentados com mesmo
		// delta_bin) seguem como defesas principais contra falso positivo.
		MinScoreCoverage:      0.02,
		MinTemporalCoverage:   0.15,
		ConfirmTimeout:        30 * time.Second,
		SegmentsOutputPattern: segmentsPattern,
		HeartbeatFn:           heartbeatFn,
		OnStreamUp:            onStreamUp,
		OnStreamDown:          onStreamDown,
		OnNoiseSample:         onNoiseSample,
	}
	w := ingestor.NewWorker(cfg, s.store, s.nc, s.log)
	s.mu.Lock()
	entry.worker = w
	entry.startedAt = time.Now()
	s.mu.Unlock()

	// Register the segment dir with the evidence service before the worker
	// (and thus ffmpeg) starts writing segments — guarantees that any
	// detection's evidence lookup finds the path even if it fires before
	// the first segment file is flushed.
	s.evidence.Register(stationID, segmentsDir)

	// Start goroutine (entry was already stored in the map above).
	go w.Run(workerCtx)

	// ── Stall watchdog goroutine (fase2 hardening) ──────────────────────────
	// Restarts the worker if no PCM has been observed for >60s.
	// Cooldown of 2 minutes between consecutive restarts to avoid restart loops
	// when a stream is genuinely down (then the reconnect backoff in the worker
	// is the right mechanism, not stall restart).
	go s.runStallWatchdog(workerCtx, stationID, w, cancel)

	// ── Preventive restart goroutine (§8.7 fase2 hardening) ─────────────────
	// Once-per-day graceful restart in 3–5 AM window to mitigate memory leaks.
	go s.schedulePreventiveRestart(workerCtx, stationID)

	// ── Threshold refresher (§9.4 fase2 hardening) ──────────────────────────
	// Periodically re-reads station_thresholds and updates the worker's
	// atomic threshold in-place. Also reacts immediately to RefreshThreshold
	// calls (admin endpoint).
	go s.runThresholdRefresher(workerCtx, capturedStationID, w, entry.refreshNow)

	// ── Worker reconciler (post-2026-05-08 / 2026-05-15 hardening) ──────────
	// Periodically re-reads stations.stream_url AND the per-station commercial
	// list, rebuilding the worker on drift. Catches missed/failed Reload calls
	// (commercials) and direct PUT /stations updates (stream_url) that would
	// otherwise leave a worker hammering a stale URL or matching against a
	// stale list — see reconcile.go for the incident context.
	go s.runWorkerReconciler(workerCtx, capturedStationID)

	metrics.WorkerCommercials.WithLabelValues(stationID.String()).Set(float64(len(shortIDs)))

	s.log.Info("supervisor: worker started",
		zap.String("station_id", stationID.String()),
		zap.String("stream_url", station.StreamURL),
		zap.Int("commercials", len(shortIDs)),
	)
	return nil
}

// stallStartupGrace is how long a worker may exist without producing any PCM
// before the watchdog treats it as stalled. Before this constant existed, the
// watchdog bailed out whenever `LastPCMAt.IsZero()` — a worker whose first
// ffmpeg connect never succeeded (dead URL, DNS NXDOMAIN, 410 Gone) stayed
// zombified forever because nothing else flips the state. 2 minutes is the
// trade-off: comfortably longer than a slow normal startup (DNS + TLS + ICY
// metadata is typically <5s) and short enough that the second watchdog tick
// after grace catches the zombie.
const stallStartupGrace = 2 * time.Minute

// isStalled decides whether the watchdog should restart a worker. Pure
// function, deterministic in (last, startedAt, now). Two flavors of stall:
//   - last is set but >60s old: the worker WAS producing PCM and stopped.
//   - last is zero but the worker has been running longer than the startup
//     grace: it has NEVER produced PCM since it started.
//
// A zero startedAt is treated as "worker not registered yet" and never
// stalled — defensive, prevents a race in startStationWorker where the
// watchdog goroutine could see a not-yet-finalised entry.
func isStalled(last, startedAt, now time.Time) bool {
	if !last.IsZero() {
		return now.Sub(last) > 60*time.Second
	}
	if startedAt.IsZero() {
		return false
	}
	return now.Sub(startedAt) > stallStartupGrace
}

// recordStreamDownSync opens a 'down' health event for the station, idempotent
// per open outage via the worker entry's lastDownID (no-op if a down event is
// already open). Extracted from the onStreamDown callback so the stall watchdog
// can record the outage on the hang/ban path too (audit 2026-07-02 F1): a hung
// or IP-banned stream never reaches the worker's OnStreamDown because
// runPCMReader blocks, so without this the outage showed zero down events
// (uptime 100%). Idempotency survives respawns because startStationWorker's
// startup recovery adopts the open down via GetLastOpenDown.
func (s *Supervisor) recordStreamDownSync(stationID uuid.UUID) {
	s.mu.Lock()
	alreadyOpen := false
	if e, ok := s.workers[stationID]; ok && e.lastDownID != nil {
		alreadyOpen = true
	}
	s.mu.Unlock()
	if alreadyOpen {
		return
	}

	bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, at, err := s.healthEvents.RecordDown(bgCtx, stationID)
	if err != nil {
		s.log.Warn("supervisor: record down failed",
			zap.String("station_id", stationID.String()),
			zap.Error(err),
		)
		return
	}

	s.mu.Lock()
	if e, ok := s.workers[stationID]; ok {
		e.lastDownID = &id
		e.lastDownAt = &at
	}
	s.mu.Unlock()
}

// recordStreamDown runs recordStreamDownSync off-goroutine so callers (worker
// reconnect loop, stall watchdog) never block on Postgres.
func (s *Supervisor) recordStreamDown(stationID uuid.UUID) {
	go s.recordStreamDownSync(stationID)
}

// runStallWatchdog periodically checks whether the worker has produced PCM
// recently. See isStalled for the decision rule. A 2-minute cooldown between
// consecutive restarts prevents restart storms when a stream is genuinely
// down (then the reconnect backoff in the worker is the right mechanism).
func (s *Supervisor) runStallWatchdog(workerCtx context.Context, stationID uuid.UUID, w *ingestor.Worker, cancel context.CancelFunc) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-workerCtx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			entry, ok := s.workers[stationID]
			var startedAt time.Time
			if ok && entry != nil {
				startedAt = entry.startedAt
			}
			s.mu.Unlock()

			now := time.Now()
			if !isStalled(w.LastPCMAt(), startedAt, now) {
				continue
			}

			// Two stall flavors (see isStalled):
			//   A) was producing PCM, then went stale → flapping/transient.
			//   B) never connected since (re)start → blocked IP / dead URL.
			// Flavor B feeds the circuit breaker so a firewall-banned station
			// is respawned with a growing, capped delay instead of every ~2min.
			neverConnected := w.LastPCMAt().IsZero()

			s.mu.Lock()
			lastRestart, seen := s.lastStallRestart[stationID]
			// Flavor A keeps the fixed 2min cooldown so a flapping-but-connecting
			// stream isn't restart-stormed. Flavor B is paced by the backoff
			// below, so it skips this gate.
			if !neverConnected && seen && now.Sub(lastRestart) < 2*time.Minute {
				s.mu.Unlock()
				continue
			}
			s.lastStallRestart[stationID] = now
			s.stallRestartCounts[stationID]++
			var delay time.Duration
			if neverConnected {
				s.connectFailures[stationID]++
				delay = jitterDelay(backoffFor(s.connectFailures[stationID]))
			}
			s.mu.Unlock()

			metrics.WorkerStallRestarts.WithLabelValues(stationID.String()).Inc()
			s.log.Warn("supervisor: worker stall detected, restarting",
				zap.String("station_id", stationID.String()),
				zap.Bool("never_connected", neverConnected),
				zap.Duration("backoff", delay))

			// F1: record the outage BEFORE killing the worker. A hung or
			// IP-banned stream never reaches the worker's OnStreamDown
			// (runPCMReader blocks on PCM that never arrives), so without this
			// the outage showed zero down events and the station read as 100%
			// up. Idempotent (lastDownID) and survives the respawn below because
			// startStationWorker's startup recovery adopts the open down.
			s.recordStreamDown(stationID)

			// Kill the worker now — ffmpeg dies, so a blocked stream stops
			// hammering the panel's firewall during the backoff window.
			cancel()

			var boCtx context.Context
			s.mu.Lock()
			delete(s.workers, stationID)
			if delay > 0 {
				// Park a backoff placeholder so Pause/Stop/preventive-restart can
				// abort the pending respawn via its cancel (= boCancel). worker==nil
				// ⇒ not counted active and skipped by WorkerStatuses.
				var boCancel context.CancelFunc
				boCtx, boCancel = context.WithCancel(context.Background())
				s.workers[stationID] = &workerEntry{cancel: boCancel, refreshNow: make(chan struct{}, 1)}
			}
			s.mu.Unlock()
			s.evidence.Unregister(stationID)
			metrics.WorkerActive.Dec()
			if delay > 0 {
				metrics.WorkerConnectBackoff.WithLabelValues(stationID.String()).Set(delay.Seconds())
			}

			go func() {
				if delay > 0 {
					t := time.NewTimer(delay)
					defer t.Stop()
					select {
					case <-boCtx.Done():
						return // paused / removed during backoff
					case <-t.C:
					}
					metrics.WorkerConnectBackoff.WithLabelValues(stationID.String()).Set(0)
				}
				if err := s.startStationWorker(context.Background(), stationID); err != nil {
					s.log.Error("supervisor: stall restart failed",
						zap.String("station_id", stationID.String()),
						zap.Error(err))
				}
			}()
			return
		}
	}
}

// runThresholdRefresher periodically re-reads station_thresholds for the
// given station and applies the value to the worker's atomic threshold.
// On lookup error the previous value is kept (we never *down*grade to the
// default once a worker has been calibrated). Exits when ctx is cancelled.
//
// trigger is an optional buffered channel that callers (e.g. RefreshThreshold)
// can use to force an immediate re-read between ticks. A closed or nil
// channel is treated as no manual trigger.
func (s *Supervisor) runThresholdRefresher(
	ctx context.Context,
	stationID uuid.UUID,
	w *ingestor.Worker,
	trigger <-chan struct{},
) {
	ticker := time.NewTicker(thresholdRefreshInterval)
	defer ticker.Stop()
	stationLabel := stationID.String()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-trigger:
		}
		s.refreshThresholdOnce(ctx, stationID, stationLabel, w)
	}
}

// refreshThresholdOnce performs a single GetThreshold lookup and applies the
// result. Extracted so the admin handler / tests can drive a refresh without
// going through the timer.
func (s *Supervisor) refreshThresholdOnce(
	ctx context.Context,
	stationID uuid.UUID,
	stationLabel string,
	w *ingestor.Worker,
) {
	ctx, span := observability.Tracer().Start(ctx, "threshold.refresh")
	span.SetAttributes(attribute.String("station_id", stationLabel))
	defer span.End()

	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	v, err := s.stations.GetThreshold(lookupCtx, stationID)
	if err != nil {
		metrics.StationThresholdRefreshes.WithLabelValues(stationLabel, "error").Inc()
		s.log.Warn("supervisor: threshold refresh failed; keeping previous value",
			zap.String("station_id", stationLabel),
			zap.Int32("current", w.Threshold()),
			zap.Error(err),
		)
		return
	}
	prev := w.SetThreshold(v)
	metrics.StationThreshold.WithLabelValues(stationLabel).Set(float64(v))
	if int(prev) == v {
		metrics.StationThresholdRefreshes.WithLabelValues(stationLabel, "unchanged").Inc()
		return
	}
	metrics.StationThresholdRefreshes.WithLabelValues(stationLabel, "updated").Inc()
	s.log.Info("supervisor: threshold updated",
		zap.String("station_id", stationLabel),
		zap.Int32("previous", prev),
		zap.Int("current", v),
	)
}

// RefreshThreshold forces the worker for stationID to re-read its threshold
// from station_thresholds immediately, returning a sentinel error when no
// worker is running for that station. Wired to the admin endpoint
// POST /v1/internal/admin/stations/{id}/threshold/refresh.
//
// The actual lookup runs asynchronously inside runThresholdRefresher so the
// HTTP handler stays cheap; the signal channel is buffered (cap 1) so a
// burst of refresh requests collapses into a single re-read.
func (s *Supervisor) RefreshThreshold(stationID uuid.UUID) error {
	s.mu.Lock()
	entry, ok := s.workers[stationID]
	s.mu.Unlock()
	if !ok || entry == nil || entry.refreshNow == nil {
		return fmt.Errorf("supervisor: no worker running for station %s", stationID)
	}
	select {
	case entry.refreshNow <- struct{}{}:
	default:
		// already pending — coalesce.
	}
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
		// ActiveCampaignsForStation returns campaigns with status = 'ativa'.
		// The current campaign is still 'ativa' at this point (we haven't
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
				delete(s.connectFailures, stationID)
				s.evidence.Unregister(stationID)
				if entry.worker != nil {
					metrics.WorkerActive.Dec()
				}
				metrics.WorkerCommercials.DeleteLabelValues(stationID.String())
				metrics.WorkerConnectBackoff.DeleteLabelValues(stationID.String())
			}
			s.mu.Unlock()
			stationsToPause = append(stationsToPause, stationID)
		}
		// c. If another active campaign uses this station, leave worker running.
	}

	// 3. Update campaign status to cancelada.
	// (Pause is now an alias for Cancel — see §18.2.1: 'paused' was collapsed
	// into 'cancelada' since pause-as-deactivation was the de-facto usage.)
	if err := s.campaigns.UpdateStatus(ctx, campaignID, "cancelada"); err != nil {
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

// publishIndexReloadForCampaign publishes an index.reload for every ready spot
// linked to the campaign — materials (via campaign_materials) and legacy
// commercials — so their fingerprints are (re)loaded into the in-memory
// matching index. Fire-and-forget over NATS core; the periodic reconcile
// (loader.RunReconcileLoop) is the safety net if a publish is dropped.
func (s *Supervisor) publishIndexReloadForCampaign(ctx context.Context, campaignID uuid.UUID) {
	if matIDs, err := s.materials.ListReadyIDsByCampaign(ctx, campaignID); err == nil {
		for _, id := range matIDs {
			payload, _ := json.Marshal(map[string]string{"material_id": id.String()})
			if err := s.nc.Publish(events.SubjectIndexReload, payload); err != nil {
				s.log.Warn("supervisor: index reload publish (material) failed",
					zap.String("material_id", id.String()), zap.Error(err))
			}
		}
	} else {
		s.log.Warn("supervisor: list ready materials for index reload failed",
			zap.String("campaign_id", campaignID.String()), zap.Error(err))
	}
	if coms, err := s.commercials.ListReadyByCampaigns(ctx, []uuid.UUID{campaignID}); err == nil {
		for _, c := range coms {
			payload, _ := json.Marshal(map[string]string{"commercial_id": c.ID.String()})
			if err := s.nc.Publish(events.SubjectIndexReload, payload); err != nil {
				s.log.Warn("supervisor: index reload publish (commercial) failed",
					zap.String("commercial_id", c.ID.String()), zap.Error(err))
			}
		}
	}
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
	if camp.Status != "ativa" {
		return nil
	}
	// Publish index.reload for the campaign's ready spots so a freshly-linked or
	// reused/backfilled material's fingerprints enter the in-memory index
	// immediately — before this Reload only restarted workers, leaving the
	// hashes absent until a full restart (audit E3, INFINITE PAY case).
	s.publishIndexReloadForCampaign(ctx, campaignID)
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

// WorkerStatus holds runtime status of a single station worker.
//
// The first four fields are the original wire contract. The remaining fields
// were added so the /workers handler can populate the OperationsPage tiles
// (bytes / reconnects / stall restarts / min_hashes) without scraping
// Prometheus — see frontend/src/pages/OperationsPage.jsx.
//
// Semantics:
//   - BytesReceived and Reconnects come from the live Worker and reset when
//     the supervisor recreates it (stall restart).
//   - StallRestarts is cumulative per station for the supervisor's lifetime
//     and survives worker recreation — it's the count the operator cares about.
//   - MinHashes mirrors the per-station threshold currently applied by the
//     matcher (also exported as the radiocheck_station_threshold gauge).
type WorkerStatus struct {
	StationID     string    `json:"station_id"`
	Active        bool      `json:"active"`
	LastPCMAt     time.Time `json:"last_pcm_at"`
	StallRisk     bool      `json:"stall_risk"`
	BytesReceived uint64    `json:"bytes_received"`
	Reconnects    uint32    `json:"reconnects"`
	StallRestarts uint32    `json:"stall_restarts"`
	MinHashes     int32     `json:"min_hashes"`
}

// WorkerStatuses returns a snapshot of all currently running workers.
// Used by the /v1/internal/workers handler.
func (s *Supervisor) WorkerStatuses() []WorkerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	var statuses []WorkerStatus
	for id, entry := range s.workers {
		if entry.worker == nil {
			continue
		}
		last := entry.worker.LastPCMAt()
		statuses = append(statuses, WorkerStatus{
			StationID:     id.String(),
			Active:        true,
			LastPCMAt:     last,
			StallRisk:     !last.IsZero() && time.Since(last) > 30*time.Second,
			BytesReceived: entry.worker.BytesReceived(),
			Reconnects:    entry.worker.Reconnects(),
			StallRestarts: s.stallRestartCounts[id],
			MinHashes:     entry.worker.Threshold(),
		})
	}
	return statuses
}

// RestoreActive re-launches workers for all campaigns with status 'ativa'.
// Called once at startup after the index loader finishes, so workers resume
// after a process restart or crash.
func (s *Supervisor) RestoreActive(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM campaigns WHERE status = 'ativa'
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

// StartLifecycle wires up the LifecycleScheduler (§18.2.1) and runs it in a
// goroutine. Activation/end events trigger worker start/pause via the
// existing Start/Pause methods, so no other code path is touched.
//
// Idempotent: a second call is a no-op.
func (s *Supervisor) StartLifecycle(ctx context.Context) {
	if s.lifecycle != nil {
		return
	}
	bus := NewNATSEventBus(s.nc, s.log)
	sched := NewLifecycleScheduler(s.db, s.campaigns, bus, s.log)

	sched.OnActivated = func(_ context.Context, campaignID uuid.UUID) {
		// The scheduler already moved the row to 'ativa'. Start() expects to
		// flip the status itself, but doing it again is a harmless idempotent
		// UPDATE — and reusing Start() means we get the same worker-spawn
		// path used by the manual /start endpoint.
		if err := s.Start(campaignID); err != nil {
			s.log.Error("lifecycle: auto-start failed",
				zap.String("campaign_id", campaignID.String()),
				zap.Error(err))
		}
	}
	sched.OnEnded = func(_ context.Context, campaignID uuid.UUID) {
		// Pause() stops workers for stations no longer covered by any active
		// campaign and updates the status to 'cancelada' — but the scheduler
		// already moved this campaign to 'concluida'. We only need the
		// worker-stop side of Pause; do it inline.
		s.StopWorkersForCampaign(campaignID)
	}

	s.lifecycle = sched
	go func() {
		if err := sched.Run(ctx); err != nil {
			s.log.Error("lifecycle scheduler exited with error", zap.Error(err))
		}
	}()
}

// StopWorkersForCampaign cancels workers for stations that have no other
// active campaign once campaignID has left the 'ativa' state. Mirrors the
// worker-stopping half of Pause() but does NOT mutate campaign.status —
// callers (lifecycle scheduler, Cancel handler) already moved the row to
// its terminal state.
func (s *Supervisor) StopWorkersForCampaign(campaignID uuid.UUID) {
	ctx := context.Background()
	camp, err := s.campaigns.Get(ctx, campaignID)
	if err != nil {
		s.log.Warn("supervisor.stopWorkersForCampaign: get campaign failed",
			zap.String("campaign_id", campaignID.String()),
			zap.Error(err))
		return
	}
	for _, stationID := range camp.TargetStations {
		activeIDs, err := s.campaigns.ActiveCampaignsForStation(ctx, stationID)
		if err != nil {
			s.log.Warn("supervisor.stopWorkersForCampaign: active campaigns query failed",
				zap.String("station_id", stationID.String()),
				zap.Error(err))
			continue
		}
		// activeIDs no longer contains campaignID (it's now 'concluida'),
		// so any leftover entry means another campaign still uses this station.
		if len(activeIDs) > 0 {
			continue
		}
		s.mu.Lock()
		if entry, ok := s.workers[stationID]; ok {
			entry.cancel()
			delete(s.workers, stationID)
			delete(s.connectFailures, stationID)
			s.evidence.Unregister(stationID)
			if entry.worker != nil {
				metrics.WorkerActive.Dec()
			}
			metrics.WorkerCommercials.DeleteLabelValues(stationID.String())
			metrics.WorkerConnectBackoff.DeleteLabelValues(stationID.String())
		}
		s.mu.Unlock()
		if err := s.stations.UpdateMonitoringStatus(ctx, stationID, "paused"); err != nil {
			s.log.Warn("supervisor.stopWorkersForCampaign: update station monitoring_status failed",
				zap.String("station_id", stationID.String()),
				zap.Error(err))
		}
	}
}
