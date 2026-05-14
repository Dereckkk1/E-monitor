package similarity

import (
	"testing"

	"github.com/google/uuid"
)

// frameRange is a half-open [from, until) range on the time_frame axis,
// expressed in matcher frames (sampleRate/stftHop ≈ 7.8125 fps).
type testRange struct{ from, until int32 }

// helper: build a pairScan from raw range data.
func pair(otherTotal int, own, other []testRange) *pairScan {
	o := &pairScan{otherTotalFrames: otherTotal}
	for _, r := range own {
		o.ownRanges = append(o.ownRanges, frameRange{r.from, r.until})
	}
	for _, r := range other {
		o.otherRanges = append(o.otherRanges, frameRange{r.from, r.until})
	}
	return o
}

// Rôgga 30s vs 60s (clean cut): own coverage 100%, other coverage 50%.
// Expected score = max = 1.0.
func TestPickTop_SubsetCleanCut(t *testing.T) {
	otherID := uuid.New()
	// 30s = ~234 frames; full window coverage.
	ownRanges := make([]testRange, 27)
	for i := 0; i < 27; i++ {
		ownRanges[i] = testRange{int32(i * 8), int32(i*8 + 32)}
	}
	// 60s = ~468 frames; only first half matched (~234 frames).
	otherRanges := ownRanges // same shape, 234 frame coverage in 468 total
	report := scanReport{
		ownTotalFrames: 234,
		perOther: map[uuid.UUID]*pairScan{
			otherID: pair(468, ownRanges, otherRanges),
		},
	}
	id, score := pickTopMatch(report)
	if id != otherID {
		t.Fatalf("expected top=%v, got %v", otherID, id)
	}
	if score < 0.99 || score > 1.01 {
		t.Fatalf("expected score~1.0, got %f", score)
	}
}

// AMB30 vs JINGLE (sting overlap of ~6.25s in 30s): both coverages ~20%.
// Expected score ~0.20.
func TestPickTop_StingOverlap(t *testing.T) {
	otherID := uuid.New()
	// 6.25s ≈ 49 frames of overlap in each.
	ownRanges := []testRange{{178, 226}}
	otherRanges := []testRange{{178, 226}}
	report := scanReport{
		ownTotalFrames: 234,
		perOther: map[uuid.UUID]*pairScan{
			otherID: pair(234, ownRanges, otherRanges),
		},
	}
	_, score := pickTopMatch(report)
	if score < 0.15 || score > 0.30 {
		t.Fatalf("expected score in [0.15,0.30] for sting overlap, got %f", score)
	}
}

// Two unrelated commercials — almost no overlap. Score should fall below
// threshold (0.15).
func TestPickTop_NoOverlap(t *testing.T) {
	otherID := uuid.New()
	// 8 frames matched out of 234 = ~3%.
	report := scanReport{
		ownTotalFrames: 234,
		perOther: map[uuid.UUID]*pairScan{
			otherID: pair(234, []testRange{{0, 8}}, []testRange{{0, 8}}),
		},
	}
	_, score := pickTopMatch(report)
	if score >= 0.15 {
		t.Fatalf("expected score < 0.15 for noise overlap, got %f", score)
	}
}

// Multiple matches — should return the one with highest score.
func TestPickTop_PicksHighest(t *testing.T) {
	low := uuid.New()
	high := uuid.New()
	report := scanReport{
		ownTotalFrames: 234,
		perOther: map[uuid.UUID]*pairScan{
			low:  pair(234, []testRange{{0, 50}}, []testRange{{0, 50}}),   // ~21%
			high: pair(234, []testRange{{0, 200}}, []testRange{{0, 200}}), // ~85%
		},
	}
	id, score := pickTopMatch(report)
	if id != high {
		t.Fatalf("expected high to win, got %v", id)
	}
	if score < 0.80 {
		t.Fatalf("expected score >= 0.80, got %f", score)
	}
}

// Empty scan (no matches at all) returns nil UUID + zero score.
func TestPickTop_Empty(t *testing.T) {
	report := scanReport{ownTotalFrames: 234, perOther: map[uuid.UUID]*pairScan{}}
	id, score := pickTopMatch(report)
	if id != uuid.Nil {
		t.Fatalf("expected nil UUID for empty scan, got %v", id)
	}
	if score != 0 {
		t.Fatalf("expected score=0 for empty scan, got %f", score)
	}
}
