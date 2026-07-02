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
// directly and asserts that broadcast-window overlaps are detected and
// that the longer-cut wins / shorter-cut loses semantics hold.
func TestSubmitDetectionFlow_BufferDedup(t *testing.T) {
	buf := NewDedupBuffer(60 * time.Second)
	station := uuid.New()
	client := uuid.New()
	now := time.Now()

	// First confirmation: 30s cut, broadcast starts at now.
	first := DedupEntry{
		Detection: match.ConfirmedDetection{
			CommercialShortID: 1,
			StationID:         station.String(),
			DetectedAt:        now.Add(3 * time.Second), // confirms ~3s into ad
		},
		ClientID:        client,
		DurationSeconds: 30,
		BroadcastStart:  now,
		InsertedAt:      now,
	}
	buf.Add(first)

	// Second confirmation: 60s cut starting 3s later (aligned start).
	// Broadcast window [now+3s, now+63s] overlaps [now, now+30s].
	conflict := buf.Find(station, client, now.Add(3*time.Second), 60)
	if conflict == nil {
		t.Fatalf("expected to find conflicting first entry via broadcast overlap")
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
			DetectedAt:        now.Add(12 * time.Second), // confirms ~9s into 60s ad
		},
		ClientID:        client,
		DurationSeconds: 60,
		BroadcastStart:  now.Add(3 * time.Second),
		InsertedAt:      now.Add(3 * time.Second),
	}
	buf.Replace(conflict, newer)

	if got := buf.Len(); got != 1 {
		t.Fatalf("expected buffer to hold a single entry after replace, got %d", got)
	}

	// Third confirmation: another 30s cut overlapping the 60s window → suppressed.
	// Candidate [now+5s, now+35s] overlaps entry [now+3s, now+63s].
	conflict2 := buf.Find(station, client, now.Add(5*time.Second), 30)
	if conflict2 == nil {
		t.Fatalf("expected to find newer entry as conflict for the late 30s cut")
	}
	if got := evaluateDedup(30, 3, conflict2); got != DedupActionSuppress {
		t.Fatalf("expected suppress for shorter late cut, got %v", got)
	}
}

// TestComputeBroadcastStart pins the offset arithmetic: evidenceWindowStart
// is commercialStart - 60s, so adding 60s must recover commercialStart.
func TestComputeBroadcastStart(t *testing.T) {
	commercialStart := time.Date(2026, 5, 8, 7, 22, 49, 0, time.UTC)
	evidenceStart := commercialStart.Add(-60 * time.Second)
	evidenceStartStr := evidenceStart.UTC().Format(time.RFC3339)

	got := computeBroadcastStart(evidenceStartStr, time.Time{})
	if !got.Equal(commercialStart) {
		t.Fatalf("computeBroadcastStart = %v, want %v", got, commercialStart)
	}
}

func TestComputeBroadcastStart_EmptyFallsBack(t *testing.T) {
	fallback := time.Now()
	if got := computeBroadcastStart("", fallback); !got.Equal(fallback) {
		t.Fatalf("expected fallback for empty string")
	}
}

func TestComputeBroadcastStart_InvalidFallsBack(t *testing.T) {
	fallback := time.Now()
	if got := computeBroadcastStart("not-a-date", fallback); !got.Equal(fallback) {
		t.Fatalf("expected fallback for invalid timestamp")
	}
}

// TestSubmitDetectionFlow_MisalignedCut20sGap is the regression test for the
// Itapoá/Rôgga incident (2026-05-08): the 30s cut was taken from ~20s into
// the 60s broadcast, causing DetectedAt values 20s apart — outside the old
// 5s Δ window, so dedup never fired and both detections were published.
// With broadcast-window overlap the two intervals intersect and dedup fires.
func TestSubmitDetectionFlow_MisalignedCut20sGap(t *testing.T) {
	buf := NewDedupBuffer(60 * time.Second)
	station := uuid.New()
	client := uuid.New()
	// Broadcast: 60s jingle starts at T.
	T := time.Date(2026, 5, 8, 7, 22, 40, 0, time.UTC)

	// 60s commercial confirms at T+9s (0.15 × 60s coverage).
	buf.Add(DedupEntry{
		Detection: match.ConfirmedDetection{
			CommercialShortID: 10,
			StationID:         station.String(),
			DetectedAt:        T.Add(9 * time.Second),
		},
		ClientID:        client,
		DurationSeconds: 60,
		BroadcastStart:  T,
		InsertedAt:      T.Add(9 * time.Second),
	})

	// 30s cut from seconds 20–50 of the broadcast. Starts matching at T+20s,
	// confirms at T+20s+4.5s ≈ T+24.5s → DetectedAt gap from 60s = 15.5s.
	// Old logic: |24.5s - 9s| = 15.5s > 5s → miss. New logic: overlap → hit.
	broadcastStart30 := T.Add(20 * time.Second) // = T+20s
	conflict := buf.Find(station, client, broadcastStart30, 30)
	if conflict == nil {
		t.Fatalf("30s misaligned cut should be caught by broadcast-window overlap (regression: Itapoá/Rôgga 2026-05-08)")
	}
	if got := evaluateDedup(30, 11, conflict); got != DedupActionSuppress {
		t.Fatalf("expected suppress (30s < 60s), got %v", got)
	}
}

// ── evaluateDedupWithConfidence (audit A2, flag DISAMBIG_CONFIDENCE_AWARE) ──

// dedupConflict é um helper: conflito de `dur`s com cobertura `conf`.
func dedupConflict(dur int, shortID int32, conf float64) *DedupEntry {
	return &DedupEntry{
		DurationSeconds: dur,
		Detection:       match.ConfirmedDetection{CommercialShortID: shortID, Confidence: conf},
	}
}

// Flag OFF → idêntico ao evaluateDedup: o corte curto perde mesmo com cobertura
// muito maior (o bug 90fm).
func TestEvaluateDedupWithConfidence_FlagOff_UnchangedBehavior(t *testing.T) {
	conflict := dedupConflict(30, 1, 0.16) // 30s, cobertura fraca
	// candidato 15s cov 0.79, flag OFF → duração manda → suppress (bug atual).
	if got := evaluateDedupWithConfidence(15, 2, 0.79, conflict, false); got != DedupActionSuppress {
		t.Fatalf("flag OFF deve preservar o comportamento (suppress), got %v", got)
	}
}

// Flag ON + gap claro de cobertura → o corte curto de MAIOR cobertura vence
// (retract+publish). É o fix do 90fm/ASAAS: 15s cov 0.79 mata o 30s cov 0.16.
func TestEvaluateDedupWithConfidence_HigherCoverageShorterCutWins(t *testing.T) {
	conflict := dedupConflict(30, 1, 0.16)
	if got := evaluateDedupWithConfidence(15, 2, 0.79, conflict, true); got != DedupActionRetractAndPublish {
		t.Fatalf("candidato de cobertura muito maior deve vencer (retract+publish), got %v", got)
	}
}

// Flag ON, mantido claramente mais forte → suprime o candidato (duplicata fraca).
func TestEvaluateDedupWithConfidence_KeptClearlyStronger_Suppresses(t *testing.T) {
	conflict := dedupConflict(30, 1, 0.80)
	if got := evaluateDedupWithConfidence(15, 2, 0.10, conflict, true); got != DedupActionSuppress {
		t.Fatalf("candidato claramente mais fraco deve ser suprimido, got %v", got)
	}
}

// Flag ON, quase-empate de cobertura (gap < margem) → cai na regra de duração,
// nunca pior que hoje.
func TestEvaluateDedupWithConfidence_NearTie_FallsBackToDuration(t *testing.T) {
	conflict := dedupConflict(30, 1, 0.55)
	// candidato 15s cov 0.50 (gap -0.05, dentro da margem) → duração → suppress.
	if got := evaluateDedupWithConfidence(15, 2, 0.50, conflict, true); got != DedupActionSuppress {
		t.Fatalf("quase-empate deve cair na duração (suppress, 15<30), got %v", got)
	}
	// candidato 60s cov 0.50 (gap -0.05) → duração → longer wins.
	if got := evaluateDedupWithConfidence(60, 2, 0.50, conflict, true); got != DedupActionRetractAndPublish {
		t.Fatalf("quase-empate deve cair na duração (longer wins), got %v", got)
	}
}

// Sem conflito → publish (ambas as flags).
func TestEvaluateDedupWithConfidence_NoConflictPublishes(t *testing.T) {
	if got := evaluateDedupWithConfidence(30, 2, 0.9, nil, true); got != DedupActionPublish {
		t.Fatalf("sem conflito deve publicar, got %v", got)
	}
}
