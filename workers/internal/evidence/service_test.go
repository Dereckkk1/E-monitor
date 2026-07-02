package evidence

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

// TestNewService_Defaults verifies the constructor wires a non-nil
// segmentDirs map so Register/Unregister never panic.
func TestNewService_Defaults(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil, false, false, false, false, zap.NewNop())
	if s == nil {
		t.Fatal("NewService returned nil")
	}
	if s.segmentDirs == nil {
		t.Fatal("expected segmentDirs map to be initialised")
	}
}

// TestRegisterUnregister_Roundtrip exercises the public segment-dir registry
// used by the supervisor and confirms Unregister clears the slot.
func TestRegisterUnregister_Roundtrip(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil, false, false, false, false, zap.NewNop())
	id := uuid.New()
	dir := "/tmp/segments/" + id.String()

	s.Register(id, dir)
	s.mu.RLock()
	got, ok := s.segmentDirs[id]
	s.mu.RUnlock()
	if !ok || got != dir {
		t.Fatal("Register did not store the dir")
	}

	s.Unregister(id)
	s.mu.RLock()
	_, ok = s.segmentDirs[id]
	s.mu.RUnlock()
	if ok {
		t.Fatal("Unregister did not clear the slot")
	}

	// Unregister of a missing id is a no-op.
	s.Unregister(uuid.New())
}

// TestRegister_Concurrent verifies Register/Unregister are safe under heavy
// concurrent use (mirrors the supervisor's startStationWorker calling these
// from multiple goroutines during reconciliation).
func TestRegister_Concurrent(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil, false, false, false, false, zap.NewNop())

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				id := uuid.New()
				s.Register(id, "/tmp/segments/"+id.String())
				s.Unregister(id)
			}
		}()
	}
	wg.Wait()

	if len(s.segmentDirs) != 0 {
		t.Fatalf("expected zero dirs after balanced register/unregister, got %d", len(s.segmentDirs))
	}
}

// TestHandle_InvalidJSON verifies that handle() never panics on malformed
// payloads — an upstream NATS bug must not crash the evidence service.
func TestHandle_InvalidJSON(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil, false, false, false, false, zap.NewNop())
	// handle takes a *nats.Msg; pass a synthetic one with garbage data.
	msg := &nats.Msg{Subject: "detections.confirmed", Data: []byte("not-json")}
	// Should return without panicking and without scheduling work.
	s.handle(msg)
}

// TestHandle_InvalidStationID verifies the uuid.Parse guard.
func TestHandle_InvalidStationID(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil, false, false, false, false, zap.NewNop())
	body, _ := json.Marshal(detectionEvent{
		StationID:           "not-a-uuid",
		CommercialShortID:   1,
		DetectedAt:          time.Now().UTC().Format(time.RFC3339),
		EvidenceWindowStart: time.Now().UTC().Format(time.RFC3339),
		EvidenceWindowEnd:   time.Now().UTC().Format(time.RFC3339),
	})
	s.handle(&nats.Msg{Subject: "detections.confirmed", Data: body})
}

// TestHandle_InvalidDetectedAt verifies the date-parsing guard.
func TestHandle_InvalidDetectedAt(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil, false, false, false, false, zap.NewNop())
	body, _ := json.Marshal(detectionEvent{
		StationID:           uuid.New().String(),
		CommercialShortID:   1,
		DetectedAt:          "not-a-date",
		EvidenceWindowStart: time.Now().UTC().Format(time.RFC3339),
		EvidenceWindowEnd:   time.Now().UTC().Format(time.RFC3339),
	})
	s.handle(&nats.Msg{Subject: "detections.confirmed", Data: body})
}

// TestHandle_InvalidEvidenceWindowStart verifies that an unparseable
// EvidenceWindowStart returns early without proceeding to the DB query.
func TestHandle_InvalidEvidenceWindowStart(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil, false, false, false, false, zap.NewNop())
	body, _ := json.Marshal(detectionEvent{
		StationID:           uuid.New().String(),
		CommercialShortID:   1,
		DetectedAt:          time.Now().UTC().Format(time.RFC3339),
		EvidenceWindowStart: "not-a-date",
		EvidenceWindowEnd:   time.Now().UTC().Format(time.RFC3339),
	})
	s.handle(&nats.Msg{Subject: "detections.confirmed", Data: body})
}

// TestHandle_InvalidEvidenceWindowEnd verifies the EvidenceWindowEnd guard.
func TestHandle_InvalidEvidenceWindowEnd(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil, false, false, false, false, zap.NewNop())
	body, _ := json.Marshal(detectionEvent{
		StationID:           uuid.New().String(),
		CommercialShortID:   1,
		DetectedAt:          time.Now().UTC().Format(time.RFC3339),
		EvidenceWindowStart: time.Now().UTC().Format(time.RFC3339),
		EvidenceWindowEnd:   "not-a-date",
	})
	s.handle(&nats.Msg{Subject: "detections.confirmed", Data: body})
}

// TestDetectionEvent_JSONShape pins the wire format consumed by handle().
func TestDetectionEvent_JSONShape(t *testing.T) {
	src := detectionEvent{
		StationID:           uuid.New().String(),
		CommercialShortID:   42,
		DetectedAt:          "2026-05-07T10:00:00Z",
		OffsetFrames:        12,
		Confidence:          0.92,
		EvidenceWindowStart: "2026-05-07T09:59:00Z",
		EvidenceWindowEnd:   "2026-05-07T10:01:00Z",
	}
	b, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, field := range []string{
		`"station_id"`,
		`"commercial_short_id"`,
		`"detected_at"`,
		`"offset_frames"`,
		`"confidence"`,
		`"evidence_window_start"`,
		`"evidence_window_end"`,
	} {
		if !containsBytes(b, field) {
			t.Fatalf("missing %s: %s", field, string(b))
		}
	}
	var dst detectionEvent
	if err := json.Unmarshal(b, &dst); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if dst != src {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", dst, src)
	}
}

func containsBytes(haystack []byte, needle string) bool {
	n := []byte(needle)
	if len(n) == 0 {
		return true
	}
	for i := 0; i+len(n) <= len(haystack); i++ {
		match := true
		for j := range n {
			if haystack[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
