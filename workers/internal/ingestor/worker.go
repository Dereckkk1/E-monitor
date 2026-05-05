package ingestor

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
	"radiocheck/internal/events"
	"radiocheck/internal/index"
	"radiocheck/internal/match"
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
	MatchThreshold   int           // minimum score to count as hit (e.g. 5)
	MinCoverage      float64       // minimum coverage for confirmation (e.g. 0.4)
	ConfirmTimeout   time.Duration // max detecting window (e.g. 30s)
	// AACBuffer is an optional externally-owned ring buffer for AAC evidence.
	// If nil, Run() creates its own internal buffer (backward-compatible).
	AACBuffer *ringbuffer.ByteRing
}

// DetectionEvent is the payload published to NATS when a detection is confirmed.
type DetectionEvent struct {
	StationID           string  `json:"station_id"`
	CommercialShortID   int32   `json:"commercial_short_id"`
	DetectedAt          string  `json:"detected_at"`           // RFC3339
	OffsetFrames        int     `json:"offset_frames"`
	Confidence          float64 `json:"confidence"`
	EvidenceWindowStart string  `json:"evidence_window_start"` // DetectedAt - 60s
	EvidenceWindowEnd   string  `json:"evidence_window_end"`   // DetectedAt + 60s
}

// Worker is a goroutine-based stream ingestor for one radio station.
type Worker struct {
	cfg   WorkerConfig
	store *index.Store
	nc    *nats.Conn
	log   *zap.Logger
}

// NewWorker creates a new Worker with the given configuration.
func NewWorker(cfg WorkerConfig, store *index.Store, nc *nats.Conn, log *zap.Logger) *Worker {
	return &Worker{
		cfg:   cfg,
		store: store,
		nc:    nc,
		log:   log,
	}
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

		// 1. Start ffmpeg.
		proc, err := StartFFmpeg(ctx, w.cfg.StreamURL, w.log)
		if err != nil {
			w.log.Error("ffmpeg start failed", zap.String("stationID", stationIDStr), zap.Error(err))
			if sleep(ctx, jitter(backoff)); ctx.Err() != nil {
				return
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		// 2. Create ring buffers.
		// Use externally-provided AACBuffer if available (allows the supervisor to
		// register it with the evidence service before passing it here).
		aacBuf := w.cfg.AACBuffer
		if aacBuf == nil {
			aacBuf = ringbuffer.NewByteRing(3000) // ~5 min at ~1 chunk/100ms
		}
		pcmBuf := ringbuffer.NewPCMRing(16000 * 35)      // 35 seconds of PCM

		// 3. Create state machines: one per commercial short ID.
		machines := make(map[int32]*match.StateMachine, len(w.cfg.CommercialShortIDs))
		for _, id := range w.cfg.CommercialShortIDs {
			totalFrames := w.cfg.CommercialFrames[id]
			frameDur := time.Duration(float64(time.Second) * float64(totalFrames) * 2048 / 16000)
			cooldown := frameDur + 5*time.Second
			machines[id] = match.NewStateMachine(
				stationIDStr,
				id,
				totalFrames,
				w.cfg.MatchThreshold,
				w.cfg.MinCoverage,
				w.cfg.ConfirmTimeout,
				cooldown,
				w.log,
			)
		}

		var wg sync.WaitGroup

		// 4. Launch goroutine: AAC reader.
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.runAACReader(proc.AACReader(), aacBuf)
		}()

		// 5. Launch goroutine: PCM reader + matcher.
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.runPCMReader(proc.PCMReader(), pcmBuf, machines, stationIDStr)
		}()

		// 6. Wait for both goroutines to finish.
		wg.Wait()

		// 7. Stop ffmpeg (idempotent — kills if still running, reaps process).
		proc.Stop()

		// 8. If ctx done: exit outer loop.
		if ctx.Err() != nil {
			return
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

// runAACReader reads AAC chunks from r and stores them in aacBuf.
func (w *Worker) runAACReader(r io.Reader, aacBuf *ringbuffer.ByteRing) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			aacBuf.Write(buf[:n], time.Now())
		}
		if err != nil {
			if err != io.EOF {
				w.log.Warn("aac reader error", zap.Error(err))
			}
			return
		}
	}
}

// runPCMReader reads float32 PCM samples from r, feeds them into pcmBuf,
// and triggers matching every 32000 samples (2 seconds at 16kHz).
func (w *Worker) runPCMReader(
	r io.Reader,
	pcmBuf *ringbuffer.PCMRing,
	machines map[int32]*match.StateMachine,
	stationIDStr string,
) {
	const tickEvery = 32000  // samples per 2-second tick at 16kHz
	const windowSize = 64000 // 4 seconds at 16kHz

	sampleCount := 0

	for {
		var sample float32
		if err := binary.Read(r, binary.LittleEndian, &sample); err != nil {
			if err != io.EOF {
				w.log.Warn("pcm reader error", zap.Error(err))
			}
			return
		}

		pcmBuf.Write([]float32{sample})
		sampleCount++

		if sampleCount < tickEvery {
			continue
		}
		sampleCount = 0

		// Extract 4-second window.
		window := pcmBuf.ReadLast(windowSize)
		if len(window) < windowSize {
			// Not enough data accumulated yet.
			continue
		}

		results := match.MatchWindow(window, w.store, w.cfg.MatchThreshold)
		now := time.Now()

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
					w.publishDetection(confirmed, stationIDStr)
				}
			}
		}
	}
}

// publishDetection serialises a ConfirmedDetection and publishes it to NATS.
// Publish failures are logged but do not crash the worker.
func (w *Worker) publishDetection(det *match.ConfirmedDetection, stationIDStr string) {
	detectedAt := det.DetectedAt
	evt := DetectionEvent{
		StationID:           stationIDStr,
		CommercialShortID:   det.CommercialShortID,
		DetectedAt:          detectedAt.UTC().Format(time.RFC3339),
		OffsetFrames:        det.OffsetFrames,
		Confidence:          det.Confidence,
		EvidenceWindowStart: detectedAt.Add(-60 * time.Second).UTC().Format(time.RFC3339),
		EvidenceWindowEnd:   detectedAt.Add(60 * time.Second).UTC().Format(time.RFC3339),
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

	if err := w.nc.Publish(events.SubjectDetectionConfirmed, payload); err != nil {
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
