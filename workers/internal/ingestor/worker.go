package ingestor

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"radiocheck/internal/events"
	"radiocheck/internal/index"
	"radiocheck/internal/match"
	"radiocheck/internal/metrics"
	"radiocheck/internal/observability"
	"radiocheck/pkg/ringbuffer"
)

// WorkerConfig holds static configuration for one stream worker.
type WorkerConfig struct {
	StationID  uuid.UUID
	StreamURL  string
	// CommercialShortIDs lists active commercial short IDs for this station
	// (populated from campaign.target_stations lookup by supervisor).
	CommercialShortIDs []int32
	// CommercialFrames maps each commercial short ID to its total frame count
	// (used for coverage window sizing).
	CommercialFrames map[int32]int
	// MatchThreshold is the minimum absolute histogram score to count a window
	// as a hit (noise floor). Backed by an atomic so the supervisor can hot-
	// reload it from station_thresholds without restarting the worker. If nil,
	// NewWorker defaults to a constant of 5 (matches the calibration job's
	// minimum). Callers should typically construct it via NewMatchThreshold.
	MatchThreshold *atomic.Int32
	// MinScoreCoverage is the per-window filter: score / totalHashes must reach
	// this fraction for the window to count. 0.05 is well above noise (~0.005)
	// while still admitting real-broadcast matches that typically run 0.05-0.30.
	MinScoreCoverage float64
	// MinTemporalCoverage is the state-machine confirmation filter: the elapsed
	// time between the first and last sustained hit must reach this fraction of
	// the commercial's duration before a detection is emitted. This is the main
	// false-positive defense — random audio cannot sustain delta-aligned hits.
	MinTemporalCoverage float64
	ConfirmTimeout time.Duration // max detecting window (e.g. 30s)
	// SegmentsOutputPattern is the absolute strftime path passed to ffmpeg's
	// segment muxer; ffmpeg writes ADTS-AAC evidence files there at
	// SegmentDuration cadence. The directory must already exist when the
	// worker starts. See internal/segments.FFmpegOutputPattern.
	SegmentsOutputPattern string
	// HeartbeatFn is called every ~30s while PCM audio is flowing.
	// Nil means no heartbeat. Used by the supervisor to update last_health_check.
	HeartbeatFn func()
	// OnStreamUp is called once per connect attempt, ~2s after audio starts flowing.
	OnStreamUp func()
	// OnStreamDown is called when the stream disconnects unexpectedly (not on ctx cancel).
	OnStreamDown func()
	// OnNoiseSample is called periodically (every NoiseSampleEvery windows)
	// with the highest histogram peak across all commercials in the live
	// matching index. The supervisor wires this to
	// calibration.RecordNoiseSample so per-station thresholds get the
	// observation feed they need to converge out of calibration_mode after
	// 7 days. Nil disables sampling for that worker.
	OnNoiseSample func(score int)
}

// NoiseSampleEvery is the number of analysis windows between noise samples.
// 5 = every 10 seconds at the matcher's 2 Hz cadence. Picked to keep the
// per-station write rate at ~6 UPDATE/min while still filling the 5000-row
// noise_samples cap in roughly 14 hours of continuous operation — well
// within the 7-day calibration window.
const NoiseSampleEvery = 5

// DetectionEvent is the payload published to NATS when a detection is confirmed.
type DetectionEvent struct {
	StationID           string  `json:"station_id"`
	CommercialShortID   int32   `json:"commercial_short_id"`
	DetectedAt          string  `json:"detected_at"` // RFC3339
	OffsetFrames        int     `json:"offset_frames"`
	Confidence          float64 `json:"confidence"`
	EvidenceWindowStart string  `json:"evidence_window_start"` // DetectedAt - 60s
	EvidenceWindowEnd   string  `json:"evidence_window_end"`   // DetectedAt + 60s

	// Forensic fields propagated from the state machine. Older clients that
	// don't know these tags ignore them on unmarshal; newer ones (evidence
	// service) consume them to populate the detections row beyond Confidence.
	HashCount          int32   `json:"hash_count"`
	TemporalCoverage   float64 `json:"temporal_coverage"`
	MatchStartOffsetMs int32   `json:"match_start_offset_ms"` // first match offset within the commercial
	MatchEndOffsetMs   int32   `json:"match_end_offset_ms"`   // last match offset within the commercial
	VariantUsed        int16   `json:"variant_used"`
	RateUsed           int16   `json:"rate_used"`
}

// Worker is a goroutine-based stream ingestor for one radio station.
type Worker struct {
	cfg           WorkerConfig
	store         *index.Store
	nc            *nats.Conn
	log           *zap.Logger
	streamUpFired bool // true after OnStreamUp fired for current connect attempt

	// lastPCMAt protects access to the most recent PCM sample timestamp.
	// Used by the supervisor's stall watchdog to decide when to restart a
	// worker that stopped producing audio (LastPCMAt() / UpdateLastPCMAt()).
	lastPCMMu sync.Mutex
	lastPCMAt time.Time
}

// UpdateLastPCMAt records the time of the most recent PCM sample received.
// Called from the PCM reader on each successful read.
func (w *Worker) UpdateLastPCMAt(t time.Time) {
	w.lastPCMMu.Lock()
	w.lastPCMAt = t
	w.lastPCMMu.Unlock()
}

// LastPCMAt returns the time of the most recent PCM sample received,
// or the zero Time if no audio has been received yet.
func (w *Worker) LastPCMAt() time.Time {
	w.lastPCMMu.Lock()
	defer w.lastPCMMu.Unlock()
	return w.lastPCMAt
}

// NewMatchThreshold returns an *atomic.Int32 pre-loaded with v. Helper used by
// the supervisor (and tests) to build WorkerConfig without manual atomic dance.
func NewMatchThreshold(v int) *atomic.Int32 {
	a := new(atomic.Int32)
	a.Store(int32(v))
	return a
}

// NewWorker creates a new Worker with the given configuration.
// If cfg.MatchThreshold is nil it is replaced with NewMatchThreshold(5) so the
// worker remains usable when callers haven't wired the dynamic threshold path.
func NewWorker(cfg WorkerConfig, store *index.Store, nc *nats.Conn, log *zap.Logger) *Worker {
	if cfg.MatchThreshold == nil {
		cfg.MatchThreshold = NewMatchThreshold(5)
	}
	return &Worker{
		cfg:   cfg,
		store: store,
		nc:    nc,
		log:   log,
	}
}

// SetThreshold atomically updates the MatchThreshold used by the running
// worker. The next match window — and any state machines created on the
// next reconnect — will see the new value. Returns the previous value so
// the caller can log/expose drift.
func (w *Worker) SetThreshold(v int) int32 {
	if w.cfg.MatchThreshold == nil {
		w.cfg.MatchThreshold = NewMatchThreshold(v)
		return 0
	}
	return w.cfg.MatchThreshold.Swap(int32(v))
}

// Threshold returns the current MatchThreshold value (atomic read).
func (w *Worker) Threshold() int32 {
	if w.cfg.MatchThreshold == nil {
		return 0
	}
	return w.cfg.MatchThreshold.Load()
}

// CommercialShortIDs returns a copy of the short ids the worker is currently
// matching against. Used by the supervisor's reconciler (reconcile.go) to
// detect drift between the in-memory list and the DB. Safe to mutate; the
// slice returned does not share backing storage with the live config.
func (w *Worker) CommercialShortIDs() []int32 {
	if len(w.cfg.CommercialShortIDs) == 0 {
		return nil
	}
	out := make([]int32, len(w.cfg.CommercialShortIDs))
	copy(out, w.cfg.CommercialShortIDs)
	return out
}

// Run starts the worker. Blocks until ctx is cancelled.
// Internally: starts ffmpeg, runs two goroutines (AAC reader, PCM reader/matcher),
// handles reconnection with exponential backoff.
func (w *Worker) Run(ctx context.Context) {
	backoff := 2 * time.Second
	const maxBackoff = 60 * time.Second

	stationIDStr := w.cfg.StationID.String()

	for {
		// Check if context is done before starting.
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Reset per-attempt flag — OnStreamUp must fire again after each connect.
		w.streamUpFired = false

		// 1. Start ffmpeg.
		proc, err := StartFFmpeg(ctx, w.cfg.StreamURL, w.cfg.SegmentsOutputPattern, w.log)
		if err != nil {
			w.log.Error("ffmpeg start failed", zap.String("stationID", stationIDStr), zap.Error(err))
			// Fire OnStreamDown on the FIRST failure of an outage. The supervisor
			// keeps an open down event in health_events; subsequent failures during
			// the same outage don't create new events (idempotent at the catalog
			// layer via GetLastOpenDown / RecordDown).
			if w.cfg.OnStreamDown != nil {
				w.cfg.OnStreamDown()
			}
			if sleep(ctx, jitter(backoff)); ctx.Err() != nil {
				return
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		// 2. Create the PCM ring used by the matcher. AAC evidence is written
		//    by ffmpeg directly to disk via the segment muxer (see
		//    SegmentsOutputPattern + internal/segments) — no in-memory ring.
		pcmBuf := ringbuffer.NewPCMRing(16000 * 35) // 35 seconds of PCM

		// 3. Create state machines: one per commercial short ID.
		// Snapshot the threshold *once* per connect attempt for the state
		// machines: the in-flight detection window must not change minScore
		// midway. The MatchWindow call below reads the atomic on every tick
		// so threshold updates take effect for the noise-floor filter.
		smThreshold := int(w.cfg.MatchThreshold.Load())
		machines := make(map[int32]*match.StateMachine, len(w.cfg.CommercialShortIDs))
		for _, id := range w.cfg.CommercialShortIDs {
			totalFrames := w.cfg.CommercialFrames[id]
			frameDur := time.Duration(float64(time.Second) * float64(totalFrames) * 2048 / 16000)
			cooldown := frameDur + 5*time.Second
			machines[id] = match.NewStateMachine(
				stationIDStr,
				id,
				totalFrames,
				smThreshold,
				w.cfg.MinTemporalCoverage,
				w.cfg.ConfirmTimeout,
				cooldown,
				w.log,
			)
		}

		// 4. Run PCM reader + matcher in this goroutine. The AAC stream is
		//    handled inside ffmpeg via the segment muxer; nothing for us to
		//    pump in user-space.
		w.runPCMReader(proc.PCMReader(), pcmBuf, machines, stationIDStr)

		// 7. Stop ffmpeg (idempotent — kills if still running, reaps process).
		proc.Stop()

		// 8. If ctx done: exit outer loop.
		if ctx.Err() != nil {
			return
		}

		// Readers exited (either ffmpeg dropped or never delivered any PCM).
		// Always fire OnStreamDown — the supervisor dedups so it only opens
		// a single down event per continuous outage. This catches both:
		//   1) was-up-then-dropped (clean disconnect during operation)
		//   2) ffmpeg-connected-but-zero-PCM (ND FM-style: HTTP OK, no body)
		if w.cfg.OnStreamDown != nil {
			w.cfg.OnStreamDown()
		}

		// 9. Reconnect: log and apply backoff.
		w.log.Info("ffmpeg exited unexpectedly, reconnecting",
			zap.String("stationID", stationIDStr),
			zap.Duration("backoff", backoff),
		)
		if sleep(ctx, jitter(backoff)); ctx.Err() != nil {
			return
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// runPCMReader reads float32 PCM samples from r, feeds them into pcmBuf,
// and triggers matching every 32000 samples (2 seconds at 16kHz).
//
// We deliberately DO NOT open a long-lived span around w.Run — that span would
// last for hours and dominate trace UIs. Instead, every 2-second matching
// iteration opens its own short-lived "worker.window" span as the trace root,
// with sample windows that contain a hit producing additional spans for the
// downstream publish.
func (w *Worker) runPCMReader(
	r io.Reader,
	pcmBuf *ringbuffer.PCMRing,
	machines map[int32]*match.StateMachine,
	stationIDStr string,
) {
	const tickEvery = 32000   // samples per 2-second tick at 16kHz
	const windowSize = 64000  // 4 seconds at 16kHz
	const heartbeatEvery = 15 // ticks ≈ 30s of flowing audio
	// Read 4096 float32 samples at a time to avoid per-sample syscall overhead.
	const readChunk = 4096

	rawBuf := make([]byte, readChunk*4)
	floatBuf := make([]float32, readChunk)

	sampleCount := 0
	heartbeatTick := 0
	noiseSampleTick := 0

	for {
		n, err := io.ReadFull(r, rawBuf)
		if err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				w.log.Warn("pcm reader error", zap.Error(err))
			}
			return
		}
		samplesRead := n / 4
		for i := 0; i < samplesRead; i++ {
			bits := binary.LittleEndian.Uint32(rawBuf[i*4:])
			floatBuf[i] = math.Float32frombits(bits)
		}
		pcmBuf.Write(floatBuf[:samplesRead])
		w.UpdateLastPCMAt(time.Now())
		sampleCount += samplesRead

		if sampleCount < tickEvery {
			continue
		}
		sampleCount -= tickEvery

		// First successful tick (~2s of PCM): stream is officially up.
		// HeartbeatFn updates last_health_check (UI cue, fires immediately so
		// UI shows "Ao vivo" within 2s instead of waiting 30s for the regular
		// heartbeat cadence). OnStreamUp records the recovery in health_events
		// (closes any open down event).
		if !w.streamUpFired {
			w.streamUpFired = true
			if w.cfg.OnStreamUp != nil {
				w.cfg.OnStreamUp()
			}
			if w.cfg.HeartbeatFn != nil {
				w.cfg.HeartbeatFn()
			}
		}

		heartbeatTick++
		if heartbeatTick >= heartbeatEvery {
			heartbeatTick = 0
			if w.cfg.HeartbeatFn != nil {
				w.cfg.HeartbeatFn()
			}
		}

		// Extract 4-second window.
		window := pcmBuf.ReadLast(windowSize)
		if len(window) < windowSize {
			// Not enough data accumulated yet.
			continue
		}

		// Span per analysis window — short-lived so it never dominates the
		// trace UI. The current sampling default is parent-based ratio 1.0
		// in dev; production should drop the ratio so this 2 Hz span source
		// doesn't flood the collector.
		windowCtx, windowSpan := observability.Tracer().Start(context.Background(), "worker.window",
		)
		windowSpan.SetAttributes(attribute.String("station_id", stationIDStr))

		matchStart := time.Now()
		results := match.MatchWindow(window, w.store, int(w.cfg.MatchThreshold.Load()), w.cfg.MinScoreCoverage)
		matchElapsed := time.Since(matchStart)
		// Histogram observation with trace_id exemplar so Grafana can jump
		// from a long-tail bucket directly to the offending trace.
		observability.ObserveWithTraceExemplar(windowCtx,
			metrics.MatchWindowDuration.WithLabelValues(stationIDStr),
			matchElapsed.Seconds(),
		)
		windowSpan.SetAttributes(
			attribute.Int("results_count", len(results)),
			attribute.Float64("duration_seconds", matchElapsed.Seconds()),
		)
		now := time.Now()

		// Log every window that passes the threshold so we can see score/ratio.
		for _, r := range results {
			w.log.Info("window match",
				zap.String("station_id", stationIDStr),
				zap.Int32("commercial_short_id", r.CommercialShortID),
				zap.Int("score", r.Score),
				zap.Int("total_hashes", r.TotalHashes),
				zap.Float64("ratio", float64(r.Score)/float64(r.TotalHashes)),
				zap.Uint8("variant", r.VariantID),
			)
		}

		// Always emit the top raw score for this window so we can audit any
		// timestamp later. ScanScores ignores the coverage filter, so this
		// reflects the true peak the matcher saw — useful when the external
		// reference system reports a detection and we want to know what we
		// scored during that exact window.
		if len(results) == 0 {
			scores := match.ScanScores(window, w.store)
			var topID int32
			topScore := 0
			for id, sc := range scores {
				if sc > topScore {
					topScore = sc
					topID = id
				}
			}
			w.log.Info("window scan",
				zap.String("station_id", stationIDStr),
				zap.Int32("top_commercial_short_id", topID),
				zap.Int("top_score", topScore),
			)
		}

		// Tick all state machines first (timeout check).
		for _, sm := range machines {
			sm.Tick(now)
		}

		// Build a result lookup for fast access.
		resultByID := make(map[int32]match.MatchResult, len(results))
		for _, r := range results {
			resultByID[r.CommercialShortID] = r
		}

		// Feed results into state machines; publish any confirmed detections.
		for id, sm := range machines {
			if res, ok := resultByID[id]; ok {
				if confirmed := sm.Update(res, now); confirmed != nil {
					w.publishDetection(windowCtx, confirmed, stationIDStr)
				}
			}
		}

		// Calibration noise sampling. Every NoiseSampleEvery-th window we
		// take the highest histogram peak across all commercials in the
		// matching index — that's the per-station noise signal the
		// calibration job needs to compute noise_p99 and lift min_hashes
		// off its permissive default. The sample is delivered through a
		// non-blocking hook so a slow DB write never stalls matching.
		noiseSampleTick++
		if w.cfg.OnNoiseSample != nil && noiseSampleTick >= NoiseSampleEvery {
			noiseSampleTick = 0
			scores := match.ScanScores(window, w.store)
			topScore := 0
			for _, sc := range scores {
				if sc > topScore {
					topScore = sc
				}
			}
			w.cfg.OnNoiseSample(topScore)
		}

		windowSpan.End()
	}
}

// publishDetection serialises a ConfirmedDetection and publishes it to NATS.
// Publish failures are logged but do not crash the worker.
//
// Evidence window: 60s before the commercial started + the full commercial +
// 60s after it ends. Uses FirstMatchAt as a proxy for start (offset by the
// analysis window length so we capture audio just before the first frame
// that produced a hit).
func (w *Worker) publishDetection(ctx context.Context, det *match.ConfirmedDetection, stationIDStr string) {
	ctx, span := observability.Tracer().Start(ctx, "worker.publish_pending",
	)
	span.SetAttributes(
		attribute.String("station_id", stationIDStr),
		attribute.Int("commercial_short_id", int(det.CommercialShortID)),
		attribute.Float64("confidence", det.Confidence),
	)
	defer span.End()
	totalFrames := w.cfg.CommercialFrames[det.CommercialShortID]
	durationSec := float64(totalFrames) * 2048.0 / 16000.0
	duration := time.Duration(durationSec * float64(time.Second))
	commercialStart := det.FirstMatchAt.Add(-4 * time.Second) // analysis window length
	commercialEnd := commercialStart.Add(duration)
	evidenceStart := commercialStart.Add(-60 * time.Second)
	evidenceEnd := commercialEnd.Add(60 * time.Second)

	// Frame → ms: 2048 samples per frame at 16 kHz = 128 ms.
	const frameMs = 128
	matchStartMs := int32(det.FirstOffsetFrames * frameMs)
	matchEndMs := int32(det.OffsetFrames * frameMs)

	evt := DetectionEvent{
		StationID:           stationIDStr,
		CommercialShortID:   det.CommercialShortID,
		DetectedAt:          det.DetectedAt.UTC().Format(time.RFC3339),
		OffsetFrames:        det.OffsetFrames,
		Confidence:          det.Confidence,
		EvidenceWindowStart: evidenceStart.UTC().Format(time.RFC3339),
		EvidenceWindowEnd:   evidenceEnd.UTC().Format(time.RFC3339),
		HashCount:           int32(det.HashCount),
		TemporalCoverage:    det.TemporalCoverage,
		MatchStartOffsetMs:  matchStartMs,
		MatchEndOffsetMs:    matchEndMs,
		VariantUsed:         int16(det.VariantID),
		RateUsed:            int16(det.RateID),
	}

	payload, err := json.Marshal(evt)
	if err != nil {
		w.log.Error("detection event marshal failed",
			zap.String("stationID", stationIDStr),
			zap.Int32("commercialShortID", det.CommercialShortID),
			zap.Error(err),
		)
		return
	}

	// Route through the supervisor for §18.2.2 version disambiguation. The
	// supervisor decides whether to publish on detections.confirmed,
	// suppress, or retract a previous publication. Workers no longer
	// publish directly to detections.confirmed.
	if err := observability.PublishWithTracing(ctx, w.nc, events.SubjectDetectionPending, payload); err != nil {
		w.log.Error("nats publish failed",
			zap.String("stationID", stationIDStr),
			zap.Int32("commercialShortID", det.CommercialShortID),
			zap.Error(err),
		)
	}
}

// sleep blocks for d, but returns early if ctx is cancelled.
func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// jitter adds ±20% random variation to d.
func jitter(d time.Duration) time.Duration {
	factor := 0.8 + rand.Float64()*0.4 // [0.8, 1.2)
	return time.Duration(float64(d) * factor)
}

// min returns the smaller of two durations.
func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
