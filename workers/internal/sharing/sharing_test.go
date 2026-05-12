package sharing

import (
	"testing"

	"github.com/google/uuid"
)

// stingRanges helper: simulates the AMB30/JINGLE sting shape — ~4 consecutive
// 4s windows clustered at the end of a 30s commercial. Each window covers
// 32 frames (frame units). Returns ranges in [from, until) form.
func stingRanges() []frameRange {
	return []frameRange{
		{from: 178, until: 210},
		{from: 186, until: 218},
		{from: 194, until: 226},
		{from: 202, until: 234},
	}
}

// fullCoverageRanges helper: simulates the VERÃO 30 ⊂ VERÃO 60 shape — 27
// consecutive 4s windows covering the entire 30s commercial.
func fullCoverageRanges() []frameRange {
	out := make([]frameRange, 27)
	for i := 0; i < 27; i++ {
		out[i] = frameRange{from: int32(i * 8), until: int32(i*8 + 32)}
	}
	return out
}

// 30s commercial → ~234 frames total at 16k/2048.
const frames30s = 234

// 60s commercial → ~468 frames total.
const frames60s = 468

// 7s commercial → ~54 frames total.
const frames7s = 54

// TestClassifyAndFilter_StingPair: two 30s commercials sharing a ~6,25s sting.
// Both coverages well below threshold → flag normally.
func TestClassifyAndFilter_StingPair(t *testing.T) {
	own := uuid.New()
	other := uuid.New()

	report := scanReport{
		ownCommercialID: own,
		ownTotalFrames:  frames30s,
		perOther: map[uuid.UUID]*perOtherScan{
			other: {
				otherTotalFrames: frames30s,
				ownRanges:        stingRanges(),
				otherRanges:      stingRanges(),
			},
		},
	}

	out := classifyAndFilter(report, SubsetThreshold)
	if len(out[own]) == 0 || len(out[other]) == 0 {
		t.Errorf("sting pair must flag both sides, got %v", out)
	}
}

// TestClassifyAndFilter_SubsetPair_Symmetric: VERÃO 30 scan against VERÃO 60.
// 30s entirely contained in 60s → own coverage 100%, other coverage 50%.
// Max = 100% → subset → don't flag.
func TestClassifyAndFilter_SubsetPair_Symmetric(t *testing.T) {
	own := uuid.New()    // VERÃO 30
	other := uuid.New()  // VERÃO 60

	// Own (30s) coverage of self = full 30s.
	// Other (60s) coverage by match = ~30s of its 60s = 50%.
	otherRanges := make([]frameRange, 27)
	for i := 0; i < 27; i++ {
		otherRanges[i] = frameRange{from: int32(i * 8), until: int32(i*8 + 32)}
	}
	report := scanReport{
		ownCommercialID: own,
		ownTotalFrames:  frames30s,
		perOther: map[uuid.UUID]*perOtherScan{
			other: {
				otherTotalFrames: frames60s,
				ownRanges:        fullCoverageRanges(),
				otherRanges:      otherRanges,
			},
		},
	}

	out := classifyAndFilter(report, SubsetThreshold)
	if len(out) != 0 {
		t.Errorf("subset pair (own=100%%, other=50%%) must not flag, got %v", out)
	}
}

// TestClassifyAndFilter_AsymmetricSubset_PULSOInsideX: PULSO (7s) inside X (30s).
// When X is the one being scanned, X's perspective sees only ~7s out of 30s
// hitting PULSO. The OLD window-fraction heuristic would say "sting" (low
// fraction of X's windows) and flag. The new bidirectional rule sees the
// OTHER side: PULSO is 100% covered → subset → don't flag.
func TestClassifyAndFilter_AsymmetricSubset_PULSOInsideX(t *testing.T) {
	own := uuid.New()    // X (30s) being scanned
	other := uuid.New()  // PULSO (7s)

	// X has 27 windows; only ~4 of them hit PULSO.
	// → ownRanges: ~4 consecutive windows of X.
	// → otherRanges: covers ~all of PULSO's 7s (each X-window of 4s hits 4s
	//   of PULSO, and consecutive X-windows shift through PULSO's full range).
	ownRanges := []frameRange{
		{from: 50, until: 82},
		{from: 58, until: 90},
		{from: 66, until: 98},
		{from: 74, until: 106},
	}
	// PULSO has ~54 frames total. The matched ranges should cover ~all of it.
	otherRanges := []frameRange{
		{from: 0, until: 32},
		{from: 8, until: 40},
		{from: 16, until: 48},
		{from: 24, until: 54},
	}

	report := scanReport{
		ownCommercialID: own,
		ownTotalFrames:  frames30s,
		perOther: map[uuid.UUID]*perOtherScan{
			other: {
				otherTotalFrames: frames7s,
				ownRanges:        ownRanges,
				otherRanges:      otherRanges,
			},
		},
	}

	out := classifyAndFilter(report, SubsetThreshold)
	if len(out) != 0 {
		t.Errorf("asymmetric subset (PULSO inside X) must not flag — old window-fraction heuristic would have; got %v", out)
	}
}

// TestClassifyAndFilter_BoundaryThreshold checks the rule at exactly the
// threshold. score ≥ SubsetThreshold counts as subset (no flag).
func TestClassifyAndFilter_BoundaryThreshold(t *testing.T) {
	own := uuid.New()
	other := uuid.New()

	// Construct ranges where the merged coverage on own equals exactly 50% of
	// ownTotalFrames. With ownTotalFrames=200 we need a merged length of 100.
	// One range [0, 100) does it.
	report := scanReport{
		ownCommercialID: own,
		ownTotalFrames:  200,
		perOther: map[uuid.UUID]*perOtherScan{
			other: {
				otherTotalFrames: 200, // other coverage will be tiny — score is dominated by own
				ownRanges:        []frameRange{{from: 0, until: 100}},
				otherRanges:      []frameRange{{from: 0, until: 1}},
			},
		},
	}
	out := classifyAndFilter(report, SubsetThreshold)
	if len(out) != 0 {
		t.Errorf("ownCoverage exactly 50%% must classify as subset, got %v", out)
	}

	// Below threshold: 99 frames covered out of 200 = 49.5%.
	report.perOther[other].ownRanges = []frameRange{{from: 0, until: 99}}
	out = classifyAndFilter(report, SubsetThreshold)
	if len(out[own]) == 0 || len(out[other]) == 0 {
		t.Errorf("ownCoverage 49.5%% with low otherCov must flag normally")
	}
}

// TestClassifyAndFilter_MixedPairs: same scan finds both a subset partner
// (full coverage) and a sting partner (low coverage). Only the sting gets
// flagged.
func TestClassifyAndFilter_MixedPairs(t *testing.T) {
	own := uuid.New()
	subsetOther := uuid.New()
	stingOther := uuid.New()

	report := scanReport{
		ownCommercialID: own,
		ownTotalFrames:  frames30s,
		perOther: map[uuid.UUID]*perOtherScan{
			subsetOther: {
				otherTotalFrames: frames60s,
				ownRanges:        fullCoverageRanges(),
				otherRanges:      fullCoverageRanges(),
			},
			stingOther: {
				otherTotalFrames: frames30s,
				ownRanges:        stingRanges(),
				otherRanges:      stingRanges(),
			},
		},
	}

	out := classifyAndFilter(report, SubsetThreshold)
	if _, ok := out[subsetOther]; ok {
		t.Errorf("subset partner must NOT be flagged")
	}
	if len(out[stingOther]) == 0 {
		t.Errorf("sting partner must be flagged")
	}
	if len(out[own]) == 0 {
		t.Errorf("own must accumulate the sting partner's ranges")
	}
}

// TestClassifyAndFilter_EmptyScan: scan against an empty catalog (or no overlap).
func TestClassifyAndFilter_EmptyScan(t *testing.T) {
	report := scanReport{
		ownCommercialID: uuid.New(),
		ownTotalFrames:  frames30s,
		perOther:        map[uuid.UUID]*perOtherScan{},
	}
	out := classifyAndFilter(report, SubsetThreshold)
	if len(out) != 0 {
		t.Errorf("empty scan should produce empty output, got %v", out)
	}
}

// TestClassifyAndFilter_ZeroFrames: guard against divide-by-zero when the PCM
// is shorter than a single analysis window.
func TestClassifyAndFilter_ZeroFrames(t *testing.T) {
	report := scanReport{
		ownCommercialID: uuid.New(),
		ownTotalFrames:  0,
		perOther: map[uuid.UUID]*perOtherScan{
			uuid.New(): {otherTotalFrames: 0},
		},
	}
	out := classifyAndFilter(report, SubsetThreshold)
	if len(out) != 0 {
		t.Errorf("ownTotalFrames=0 must short-circuit safely, got %v", out)
	}
}

// TestFrameCoverage_UnionMath exercises the merged-length calculation.
func TestFrameCoverage_UnionMath(t *testing.T) {
	cases := []struct {
		name string
		in   []frameRange
		want int
	}{
		{"empty", nil, 0},
		{"single", []frameRange{{0, 10}}, 10},
		{"disjoint", []frameRange{{0, 10}, {20, 30}}, 20},
		{"overlapping", []frameRange{{0, 15}, {10, 25}}, 25},
		{"contiguous", []frameRange{{0, 10}, {10, 20}}, 20},
		{"contained", []frameRange{{0, 30}, {5, 15}}, 30},
		{"unsorted", []frameRange{{20, 30}, {0, 10}, {5, 25}}, 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := frameCoverage(c.in)
			if got != c.want {
				t.Errorf("frameCoverage(%v) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}
