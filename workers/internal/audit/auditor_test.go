package audit

import (
	"testing"

	"radiocheck/internal/match"
	"radiocheck/pkg/audio"
)

// TestRunMatch_PerfectAlignment: query hashes that all align to the same
// delta-bin against the master should produce a peak equal to the number of
// matching hashes and coverage = matched_frames / total_master_frames.
func TestRunMatch_PerfectAlignment(t *testing.T) {
	// Master: 5 hashes at frames 0..4, all variant=0, rate=0.
	masterByHash := map[uint32][]HashEntry{
		0x10: {{0, 0, 0}},
		0x20: {{0, 0, 1}},
		0x30: {{0, 0, 2}},
		0x40: {{0, 0, 3}},
		0x50: {{0, 0, 4}},
	}
	totals := map[vrKey]int{{0, 0}: 5}

	// Query: same hashes but offset by +10 frames (simulates evidence audio
	// starting 10 frames after the master). All deltas = -10, all land in
	// bin -5 (= -10 / DeltaBinSize(2)).
	query := []audio.Hash{
		{Value: 0x10, TimeFrame: 10},
		{Value: 0x20, TimeFrame: 11},
		{Value: 0x30, TimeFrame: 12},
		{Value: 0x40, TimeFrame: 13},
		{Value: 0x50, TimeFrame: 14},
	}

	res := runMatch(query, masterByHash, totals, 3, 0.5)
	if !res.Passed {
		t.Fatalf("expected Passed=true; got %+v", res)
	}
	if res.Score != 5 {
		t.Errorf("Score=%d, want 5", res.Score)
	}
	if res.Coverage != 1.0 {
		t.Errorf("Coverage=%f, want 1.0", res.Coverage)
	}
	wantBin := -10 / match.DeltaBinSize
	if res.DeltaBin != wantBin {
		t.Errorf("DeltaBin=%d, want %d", res.DeltaBin, wantBin)
	}
}

// TestRunMatch_Noise: query hashes that incidentally collide with the master
// at random offsets should not produce a histogram peak above threshold. This
// is the UNIFIQUE-style false positive case: ~12 hits scattered across many
// delta bins → peak=2 at best.
func TestRunMatch_Noise(t *testing.T) {
	// Master with 5 hashes, all at frame 100 (so deltas land in noisy bins).
	masterByHash := map[uint32][]HashEntry{
		0xAA: {{0, 0, 100}},
		0xBB: {{0, 0, 100}},
		0xCC: {{0, 0, 100}},
	}
	totals := map[vrKey]int{{0, 0}: 200}

	// Query: each hash at a wildly different frame — each one produces a
	// different delta, so they spread across bins.
	query := []audio.Hash{
		{Value: 0xAA, TimeFrame: 5},   // delta=95
		{Value: 0xBB, TimeFrame: 30},  // delta=70
		{Value: 0xCC, TimeFrame: 60},  // delta=40
		{Value: 0xAA, TimeFrame: 150}, // delta=-50
	}

	res := runMatch(query, masterByHash, totals, 5, 0.4)
	if res.Passed {
		t.Fatalf("expected Passed=false (scattered noise); got %+v", res)
	}
	if res.Score >= 5 {
		t.Errorf("Score=%d should be below threshold 5", res.Score)
	}
}

// TestRunMatch_PartialCoverage: enough peak hits, but they cover only a small
// fraction of the master timeline → should fail on coverage check even with
// score above threshold.
func TestRunMatch_PartialCoverage(t *testing.T) {
	// Master spans frames 0..99 (100 total). All hashes share value 0xAA but
	// the master has only 5 distinct frame slots out of 100.
	masterByHash := map[uint32][]HashEntry{
		0xAA: {
			{0, 0, 10}, {0, 0, 11}, {0, 0, 12}, {0, 0, 13}, {0, 0, 14},
		},
	}
	totals := map[vrKey]int{{0, 0}: 100}

	// Query: 5 hits all aligned at delta=10 (one hit per master frame).
	// Peak count = 5, but coverage = 5/100 = 5%.
	query := []audio.Hash{
		{Value: 0xAA, TimeFrame: 0},
		{Value: 0xAA, TimeFrame: 1},
		{Value: 0xAA, TimeFrame: 2},
		{Value: 0xAA, TimeFrame: 3},
		{Value: 0xAA, TimeFrame: 4},
	}

	res := runMatch(query, masterByHash, totals, 3, 0.4)
	if res.Score < 3 {
		t.Errorf("expected Score >= 3, got %d", res.Score)
	}
	if res.Passed {
		t.Errorf("Passed=true with coverage=%f, want false (coverage below 0.4)", res.Coverage)
	}
}

// TestRunMatch_CoverageUnionsAdjacentBins: a strong match whose aligned frames
// drift across adjacent delta bins (playout-speed jitter over a long spot) must
// still pass coverage. Each bin alone covers < minCoverage, but the union of the
// peak bin and its neighbors reconstructs the true coverage. This is the real
// prod failure: 30s cuts (ASAAS PLATAFORMA) scoring 50–176 (min 5) were rejected
// at single-bin coverage ~0.10 because drift spread the spot across delta bins.
func TestRunMatch_CoverageUnionsAdjacentBins(t *testing.T) {
	// Master: 9 distinct frames 0..8, one unique hash each.
	masterByHash := map[uint32][]HashEntry{
		0x100: {{0, 0, 0}}, 0x101: {{0, 0, 1}}, 0x102: {{0, 0, 2}},
		0x103: {{0, 0, 3}}, 0x104: {{0, 0, 4}}, 0x105: {{0, 0, 5}},
		0x106: {{0, 0, 6}}, 0x107: {{0, 0, 7}}, 0x108: {{0, 0, 8}},
	}
	totals := map[vrKey]int{{0, 0}: 9}

	// Drift: frames 0-2 align at delta -100 (bin -50), 3-5 at delta -98 (bin -49),
	// 6-8 at delta -96 (bin -48). Three adjacent bins, 3 distinct master frames
	// each → best single-bin coverage = 3/9 ≈ 0.33; union ≥ 6/9 = 0.67.
	query := []audio.Hash{
		{Value: 0x100, TimeFrame: 100}, {Value: 0x101, TimeFrame: 101}, {Value: 0x102, TimeFrame: 102},
		{Value: 0x103, TimeFrame: 101}, {Value: 0x104, TimeFrame: 102}, {Value: 0x105, TimeFrame: 103},
		{Value: 0x106, TimeFrame: 102}, {Value: 0x107, TimeFrame: 103}, {Value: 0x108, TimeFrame: 104},
	}

	res := runMatch(query, masterByHash, totals, 3, 0.4)
	if !res.Passed {
		t.Fatalf("expected Passed=true (drift across adjacent bins should union coverage); got %+v", res)
	}
	if res.Coverage < 0.4 {
		t.Errorf("Coverage=%f, want >= 0.4 after unioning adjacent bins", res.Coverage)
	}
}

// TestRunMatch_EmptyQuery: empty query hashes returns a zero-Result, no panic.
func TestRunMatch_EmptyQuery(t *testing.T) {
	masterByHash := map[uint32][]HashEntry{
		0xAA: {{0, 0, 0}},
	}
	totals := map[vrKey]int{{0, 0}: 1}

	res := runMatch(nil, masterByHash, totals, 5, 0.4)
	if res == nil {
		t.Fatal("expected non-nil Result")
	}
	if res.Passed || res.Score != 0 {
		t.Errorf("expected empty Result for empty query; got %+v", res)
	}
}

// TestRunMatch_NoMatchingHashes: query hashes don't collide with master at all
// → score=0, coverage=0, Passed=false.
func TestRunMatch_NoMatchingHashes(t *testing.T) {
	masterByHash := map[uint32][]HashEntry{
		0xAA: {{0, 0, 0}},
	}
	totals := map[vrKey]int{{0, 0}: 1}

	query := []audio.Hash{
		{Value: 0xBB, TimeFrame: 0}, // no master entry for 0xBB
	}

	res := runMatch(query, masterByHash, totals, 5, 0.4)
	if res.Passed {
		t.Errorf("Passed=true on disjoint hash sets; got %+v", res)
	}
	if res.Score != 0 {
		t.Errorf("Score=%d, want 0", res.Score)
	}
}

// TestNewAuditor_DefaultsFallback: zero values for thresholds should resolve
// to the §9.3 production constants.
func TestNewAuditor_DefaultsFallback(t *testing.T) {
	a := NewAuditor(nil, nil, 0, 0)
	if a.minScore != DefaultMinScore {
		t.Errorf("minScore=%d, want %d", a.minScore, DefaultMinScore)
	}
	if a.minCoverage != DefaultMinCoverage {
		t.Errorf("minCoverage=%f, want %f", a.minCoverage, DefaultMinCoverage)
	}
}

// TestNewAuditor_CustomThresholds: explicit non-zero values are honored.
func TestNewAuditor_CustomThresholds(t *testing.T) {
	a := NewAuditor(nil, nil, 12, 0.75)
	if a.minScore != 12 {
		t.Errorf("minScore=%d, want 12", a.minScore)
	}
	if a.minCoverage != 0.75 {
		t.Errorf("minCoverage=%f, want 0.75", a.minCoverage)
	}
}
