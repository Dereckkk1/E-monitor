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
// Errors are logged but never cause a panic.
func (s *Service) handle(msg *nats.Msg) {
	ctx := context.Background()

	// 1. Unmarshal JSON.
	var ev detectionEvent
	if err := json.Unmarshal(msg.Data, &ev); err != nil {
		s.log.Warn("evidence: unmarshal failed", zap.Error(err))
		return
	}

	// 2. Parse UUIDs and timestamps.
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
		s.log.Warn("evidence: invalid evidence_window_start", zap.String("value", ev.EvidenceWindowStart), zap.Error(err))
		return
	}
	windowEnd, err := time.Parse(time.RFC3339, ev.EvidenceWindowEnd)
	if err != nil {
		s.log.Warn("evidence: invalid evidence_window_end", zap.String("value", ev.EvidenceWindowEnd), zap.Error(err))
		return
	}

	// 3. Look up ring buffer for this station (read-only).
	s.mu.RLock()
	buf := s.buffers[stationID]
	s.mu.RUnlock()

	// 4. Initialise evidence state.
	evidenceStatus := "failed"
	evidenceKey := ""
	var aacData []byte

	// 5. Extract audio data if the buffer is registered.
	if buf != nil {
		aacData = buf.Extract(windowStart, windowEnd)
	}

	// 6. Look up commercial and campaign UUIDs by short_id.
	var commercialID, campaignID uuid.UUID
	row := s.db.QueryRow(ctx,
		`SELECT c.id, c.campaign_id FROM commercials c WHERE c.short_id = $1 AND c.fingerprint_status = 'ready' LIMIT 1`,
		ev.CommercialShortID,
	)
	if err := row.Scan(&commercialID, &campaignID); err != nil {
		s.log.Warn("evidence: commercial not found",
			zap.Int32("short_id", ev.CommercialShortID),
			zap.Error(err),
		)
		commercialID = uuid.Nil
		campaignID = uuid.Nil
	}

	// 8. Build S3 key.
	detectionID := uuid.New()
	key := fmt.Sprintf("evidences/%s/%s/%s/%s/%s.m4a",
		detectedAt.Format("2006"),
		detectedAt.Format("01"),
		detectedAt.Format("02"),
		stationID,
		detectionID,
	)

	// 9. Encode to m4a and upload to S3 if audio data is available.
	if len(aacData) > 0 {
		m4aData, err := encodeToM4A(aacData, detectionID, detectedAt)
		if err != nil {
			s.log.Error("evidence encoding failed", zap.Error(err))
			// fall through with evidenceStatus = "failed"
		} else {
			err = s.store.Put(ctx, key, bytes.NewReader(m4aData), "video/mp4")
			if err == nil {
				evidenceStatus = "available"
				evidenceKey = key
			} else {
				s.log.Error("evidence upload failed", zap.Error(err))
			}
		}
	}

	// 10. Insert detection into Postgres.
	det, err := s.detections.Create(ctx, catalog.CreateDetectionInput{
		StationID:    stationID,
		CommercialID: commercialID,
		CampaignID:   campaignID,
		DetectedAt:   detectedAt,
		// MatchStartOffsetMs / MatchEndOffsetMs are 0 — the worker does not
		// publish sub-ms offsets in the current event payload.
		MatchStartOffsetMs: 0,
		MatchEndOffsetMs:   0,
		Confidence:         ev.Confidence,
		HashCount:          0,
		TemporalCoverage:   0,
		VariantUsed:        0,
		RateUsed:           0,
	})
	if err != nil {
		s.log.Error("evidence: db insert failed", zap.Error(err))
		return
	}

	// 11. Update evidence fields if the upload succeeded.
	if evidenceStatus == "available" {
		sizeBytes := int64(len(aacData))
		if updateErr := s.detections.UpdateEvidence(ctx, det.ID, det.DetectedAt, "available", evidenceKey, sizeBytes); updateErr != nil {
			s.log.Error("evidence: update evidence failed",
				zap.String("detection_id", det.ID.String()),
				zap.Error(updateErr),
			)
		}
	}

	s.log.Info("evidence: detection persisted",
		zap.String("detection_id", det.ID.String()),
		zap.String("station_id", stationID.String()),
		zap.String("evidence_status", evidenceStatus),
	)
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
		"-f", "adts",
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
