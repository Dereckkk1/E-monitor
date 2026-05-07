package supervisor

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"radiocheck/internal/match"
)

func TestEvaluateDedup_NoConflictPublishes(t *testing.T) {
	got := evaluateDedup(60, 100, nil)
	if got != DedupActionPublish {
		t.Fatalf("expected publish, got %v", got)
	}
}

func TestEvaluateDedup_LongerCutWins(t *testing.T) {
	conflict := &DedupEntry{DurationSeconds: 30,
		Detection: match.ConfirmedDetection{CommercialShortID: 1}}
	got := evaluateDedup(60, 2, conflict)
	if got != DedupActionRetractAndPublish {
		t.Fatalf("expected retract+publish, got %v", got)
	}
}

func TestEvaluateDedup_ShorterCutSuppressed(t *testing.T) {
	conflict := &DedupEntry{DurationSeconds: 60,
		Detection: match.ConfirmedDetection{CommercialShortID: 1}}
	got := evaluateDedup(30, 2, conflict)
	if got != DedupActionSuppress {
		t.Fatalf("expected suppress, got %v", got)
	}
}

func TestEvaluateDedup_TiePrefersLowerShortID(t *testing.T) {
	conflict := &DedupEntry{DurationSeconds: 30,
		Detection: match.ConfirmedDetection{CommercialShortID: 5}}

	// Candidate has lower short_id → wins.
	if got := evaluateDedup(30, 2, conflict); got != DedupActionRetractAndPublish {
		t.Fatalf("expected retract+publish for lower short_id, got %v", got)
	}

	// Candidate has higher short_id → loses.
	if got := evaluateDedup(30, 9, conflict); got != DedupActionSuppress {
		t.Fatalf("expected suppress for higher short_id, got %v", got)
	}
}

// TestSubmitDetectionFlow_BufferDedup exercises the dedup-buffer side of
// SubmitDetection without touching the network/DB: it constructs entries
// directly and asserts that conflicts within the window are detected and
// that the longer-cut wins / shorter-cut loses semantics matches.
func TestSubmitDetectionFlow_BufferDedup(t *testing.T) {
	buf := NewDedupBuffer(60 * time.Second)
	station := uuid.New()
	client := uuid.New()
	now := time.Now()

	// First confirmation: 30s cut.
	first := DedupEntry{
		Detection: match.ConfirmedDetection{
			CommercialShortID: 1,
			StationID:         station.String(),
			DetectedAt:        now,
		},
		ClientID:        client,
		DurationSeconds: 30,
		InsertedAt:      now,
	}
	buf.Add(first)

	// Second confirmation: 60s cut, 3s later → conflict within Δ=5s.
	conflict := buf.Find(station, client, now.Add(3*time.Second), 5*time.Second)
	if conflict == nil {
		t.Fatalf("expected to find conflicting first entry within 5s window")
	}
	action := evaluateDedup(60, 2, conflict)
	if action != DedupActionRetractAndPublish {
		t.Fatalf("expected retract+publish (longer cut wins), got %v", action)
	}

	// Replace the buffer entry with the longer cut.
	newer := DedupEntry{
		Detection: match.ConfirmedDetection{
			CommercialShortID: 2,
			StationID:         station.String(),
			DetectedAt:        now.Add(3 * time.Second),
		},
		ClientID:        client,
		DurationSeconds: 60,
		InsertedAt:      now.Add(3 * time.Second),
	}
	buf.Replace(conflict, newer)

	if got := buf.Len(); got != 1 {
		t.Fatalf("expected buffer to hold a single entry after replace, got %d", got)
	}

	// Third confirmation: another 30s cut, shortly after → must be suppressed.
	conflict2 := buf.Find(station, client, now.Add(5*time.Second), 5*time.Second)
	if conflict2 == nil {
		t.Fatalf("expected to find newer entry as conflict for the late 30s cut")
	}
	if got := evaluateDedup(30, 3, conflict2); got != DedupActionSuppress {
		t.Fatalf("expected suppress for shorter late cut, got %v", got)
	}
}
