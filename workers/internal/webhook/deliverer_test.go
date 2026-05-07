package webhook

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/zap"
)

// TestDetectionEvent_JSONRoundTrip pins the wire shape that Deliverer expects
// from NATS detections.confirmed messages — ingestor.publishDetection is the
// producer, this struct is the consumer; any drift breaks delivery.
func TestDetectionEvent_JSONRoundTrip_Webhook(t *testing.T) {
	src := detectionEvent{
		StationID:           "11111111-2222-3333-4444-555555555555",
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
		`"station_id"`, `"commercial_short_id"`, `"detected_at"`,
		`"offset_frames"`, `"confidence"`,
		`"evidence_window_start"`, `"evidence_window_end"`,
	} {
		if !contains(b, []byte(field)) {
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

// TestRetractedEvent_JSONRoundTrip pins the wire shape consumed by the
// Deliverer when forwarding §18.2.2 retractions.
func TestRetractedEvent_JSONRoundTrip(t *testing.T) {
	src := retractedEvent{
		StationID:         "11111111-2222-3333-4444-555555555555",
		CommercialShortID: 17,
		DetectedAt:        "2026-05-07T10:00:00Z",
		RetractedAt:       "2026-05-07T10:00:05Z",
		Reason:            "longer_cut_detected",
		Confidence:        0.91,
	}
	b, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, field := range []string{
		`"station_id"`, `"commercial_short_id"`, `"detected_at"`,
		`"retracted_at"`, `"reason"`, `"confidence"`,
	} {
		if !contains(b, []byte(field)) {
			t.Fatalf("missing %s: %s", field, string(b))
		}
	}
	var dst retractedEvent
	if err := json.Unmarshal(b, &dst); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if dst != src {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", dst, src)
	}
}

// TestHandleDetection_InvalidJSON verifies the unmarshal guard.
func TestHandleDetection_InvalidJSON(t *testing.T) {
	d := &Deliverer{log: zap.NewNop()}
	err := d.handleDetection(context.Background(), []byte("not-json"))
	if err == nil {
		t.Fatal("expected unmarshal error on garbage input")
	}
}

// TestHandleRetracted_InvalidJSON verifies the unmarshal guard.
func TestHandleRetracted_InvalidJSON(t *testing.T) {
	d := &Deliverer{log: zap.NewNop()}
	err := d.handleRetracted(context.Background(), []byte("not-json"))
	if err == nil {
		t.Fatal("expected unmarshal error on garbage input")
	}
}

// TestOutbox_Accessor exercises the read-only Outbox() getter that callers
// (e.g. the API "send test webhook" handler) use.
func TestOutbox_Accessor(t *testing.T) {
	d := &Deliverer{outbox: &Outbox{log: zap.NewNop()}}
	if d.Outbox() != d.outbox {
		t.Fatal("Outbox() should return the embedded *Outbox")
	}
}

func contains(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
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
