package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
	"radiocheck/internal/catalog"
	"radiocheck/internal/events"
	"radiocheck/internal/storage"
	"radiocheck/pkg/ringbuffer"
)

type detectionEvent struct {
	StationID           string  `json:"station_id"`
	CommercialShortID   int32   `json:"commercial_short_id"`
	DetectedAt          string  `json:"detected_at"`
	OffsetFrames        int     `json:"offset_frames"`
	Confidence          float64 `json:"confidence"`
	EvidenceWindowStart string  `json:"evidence_window_start"`
	EvidenceWindowEnd   string  `json:"evidence_window_end"`
}

// Service listens for confirmed detections, extracts audio evidence from ring
// buffers, uploads it to S3, and persists detection records to Postgres.
type Service struct {
	db         *pgxpool.Pool
	store      *storage.Client
	nc         *nats.Conn
	detections *catalog.Detections
	log        *zap.Logger
	mu         sync.RWMutex
	buffers    map[uuid.UUID]*ringbuffer.ByteRing
}

// NewService constructs a ready-to-use evidence Service.
func NewService(
	db *pgxpool.Pool,
	store *storage.Client,
	nc *nats.Conn,
	detections *catalog.Detections,
	log *zap.Logger,
) *Service {
	return &Service{
		db:         db,
		store:      store,
		nc:         nc,
		detections: detections,
		log:        log,
		buffers:    make(map[uuid.UUID]*ringbuffer.ByteRing),
	}
}

// Register associates a ring buffer with a station so that evidence can be
// extracted when a detection is confirmed.
func (s *Service) Register(stationID uuid.UUID, buf *ringbuffer.ByteRing) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buffers[stationID] = buf
}

// Unregister removes the ring buffer association for a station.
func (s *Service) Unregister(stationID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.buffers, stationID)
}

// Subscribe begins consuming "detections.confirmed" NATS messages.
// The returned subscription can be used to unsubscribe when shutting down.
func (s *Service) Subscribe(ctx context.Context) (*nats.Subscription, error) {
	sub, err := s.nc.Subscribe(events.SubjectDetectionConfirmed, func(msg *nats.Msg) {
		s.handle(msg)
	})
	if err != nil {
		return nil, fmt.Errorf("evidence: subscribe: %w", err)
	}
	return sub, nil
}

// handle processes a single detections.confirmed message.
//
// Flow:
//  1. Insert detection right away with evidence_status='pending' so the API
//     can surface it immediately.
//  2. Spawn a goroutine that waits until the requested evidence window is
//     fully captured by the ring buffer, then extracts, encodes, uploads and
//     updates the detection row with the final status.
//
// Errors are logged but never panic.
func (s *Service) handle(msg *nats.Msg) {
	ctx := context.Background()

	var ev detectionEvent
	if err := json.Unmarshal(msg.Data, &ev); err != nil {
		s.log.Warn("evidence: unmarshal failed", zap.Error(err))
		return
	}

	stationID, err := uuid.Parse(ev.StationID)
	if err != nil {
		s.log.Warn("evidence: invalid station_id", zap.String("station_id", ev.StationID), zap.Error(err))
		return
	}
	detectedAt, err := time.Parse(time.RFC3339, ev.DetectedAt)
	if err != nil {
		s.log.Warn("evidence: invalid detected_at", zap.String("detected_at", ev.DetectedAt), zap.Error(err))
		return
	}
	windowStart, err := time.Parse(time.RFC3339, ev.EvidenceWindowStart)
	if err != nil {
		s.log.Warn("evidence: invalid evidence_window_start", zap.Error(err))
		return
	}
	windowEnd, err := time.Parse(time.RFC3339, ev.EvidenceWindowEnd)
	if err != nil {
		s.log.Warn("evidence: invalid evidence_window_end", zap.Error(err))
		return
	}

	// Look up commercial + campaign.
	var commercialID, campaignID uuid.UUID
	row := s.db.QueryRow(ctx,
		`SELECT c.id, c.campaign_id FROM commercials c WHERE c.short_id = $1 AND c.fingerprint_status = 'ready' LIMIT 1`,
		ev.CommercialShortID,
	)
	if err := row.Scan(&commercialID, &campaignID); err != nil {
		s.log.Warn("evidence: commercial not found", zap.Int32("short_id", ev.CommercialShortID), zap.Error(err))
		commercialID = uuid.Nil
		campaignID = uuid.Nil
	}

	det, err := s.detections.Create(ctx, catalog.CreateDetectionInput{
		StationID:          stationID,
		CommercialID:       commercialID,
		CampaignID:         campaignID,
		DetectedAt:         detectedAt,
		MatchStartOffsetMs: 0,
		MatchEndOffsetMs:   0,
		Confidence:         ev.Confidence,
		HashCount:          0,
		TemporalCoverage:   ev.Confidence,
		VariantUsed:        0,
		RateUsed:           0,
	})
	if err != nil {
		s.log.Error("evidence: db insert failed", zap.Error(err))
		return
	}

	s.log.Info("evidence: detection persisted (pending)",
		zap.String("detection_id", det.ID.String()),
		zap.String("station_id", stationID.String()),
		zap.Time("window_end", windowEnd),
	)

	// Async: wait for the window to be fully captured, then extract & upload.
	go s.processEvidence(det.ID, det.DetectedAt, stationID, windowStart, windowEnd)
}

// processEvidence sleeps until the evidence window is fully captured by the
// ring buffer, then extracts the audio, encodes it to m4a, uploads to S3 and
// updates the detection row with the final evidence status.
func (s *Service) processEvidence(
	detectionID uuid.UUID,
	detectedAt time.Time,
	stationID uuid.UUID,
	windowStart, windowEnd time.Time,
) {
	ctx := context.Background()

	// Small margin so we don't race the buffer writer.
	if delay := time.Until(windowEnd) + 2*time.Second; delay > 0 {
		time.Sleep(delay)
	}

	s.mu.RLock()
	buf := s.buffers[stationID]
	s.mu.RUnlock()

	if buf == nil {
		s.markFailed(ctx, detectionID, detectedAt, "no buffer")
		return
	}

	aacData := buf.Extract(windowStart, windowEnd)
	if len(aacData) == 0 {
		s.markFailed(ctx, detectionID, detectedAt, "buffer extract empty")
		return
	}

	m4aData, err := encodeToM4A(aacData, detectionID, detectedAt)
	if err != nil {
		s.log.Error("evidence encoding failed", zap.String("detection_id", detectionID.String()), zap.Error(err))
		s.markFailed(ctx, detectionID, detectedAt, "encode failed")
		return
	}

	key := fmt.Sprintf("evidences/%s/%s/%s/%s/%s.m4a",
		detectedAt.Format("2006"), detectedAt.Format("01"), detectedAt.Format("02"),
		stationID, detectionID,
	)

	if err := s.store.Put(ctx, key, bytes.NewReader(m4aData), "video/mp4"); err != nil {
		s.log.Error("evidence upload failed", zap.String("detection_id", detectionID.String()), zap.Error(err))
		s.markFailed(ctx, detectionID, detectedAt, "upload failed")
		return
	}

	if err := s.detections.UpdateEvidence(ctx, detectionID, detectedAt, "available", key, int64(len(m4aData))); err != nil {
		s.log.Error("evidence: update evidence failed", zap.String("detection_id", detectionID.String()), zap.Error(err))
		return
	}

	s.log.Info("evidence: ready",
		zap.String("detection_id", detectionID.String()),
		zap.Int("bytes", len(m4aData)),
	)
}

func (s *Service) markFailed(ctx context.Context, detectionID uuid.UUID, detectedAt time.Time, reason string) {
	s.log.Warn("evidence: marking failed",
		zap.String("detection_id", detectionID.String()),
		zap.String("reason", reason),
	)
	if err := s.detections.UpdateEvidence(ctx, detectionID, detectedAt, "failed", "", 0); err != nil {
		s.log.Error("evidence: failed-status update failed", zap.Error(err))
	}
}

// encodeToM4A wraps raw ADTS-format AAC bytes in an m4a (MP4) container using
// ffmpeg. Temporary files are created and cleaned up via defer.
func encodeToM4A(aacData []byte, detectionID uuid.UUID, detectedAt time.Time) ([]byte, error) {
	// Write raw ADTS to a temp file.
	tmpIn, err := os.CreateTemp("", "evidence-*.aac")
	if err != nil {
		return nil, fmt.Errorf("create temp input: %w", err)
	}
	defer os.Remove(tmpIn.Name())
	if _, err := tmpIn.Write(aacData); err != nil {
		tmpIn.Close()
		return nil, fmt.Errorf("write temp input: %w", err)
	}
	tmpIn.Close()

	tmpOut, err := os.CreateTemp("", "evidence-*.m4a")
	if err != nil {
		return nil, fmt.Errorf("create temp output: %w", err)
	}
	defer os.Remove(tmpOut.Name())
	tmpOut.Close()

	cmd := exec.Command("ffmpeg", "-y",
		"-f", "aac",
		"-i", tmpIn.Name(),
		"-c:a", "copy",
		"-movflags", "+faststart",
		"-metadata", fmt.Sprintf("title=Evidência %s", detectionID),
		"-metadata", "artist=Sistema de Monitoramento",
		"-metadata", fmt.Sprintf("date=%s", detectedAt.Format(time.RFC3339)),
		tmpOut.Name(),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg encode: %w: %s", err, out)
	}

	return os.ReadFile(tmpOut.Name())
}
