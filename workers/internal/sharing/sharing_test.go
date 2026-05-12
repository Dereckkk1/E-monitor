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

// 7s commercial → ~54 frames total. Below MinShareableDurationFrames (~78).
const frames7s = 54

// 15s commercial → ~117 frames total. Above MinShareableDurationFrames.
const frames15s = 117

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

// TestClassifyAndFilter_AsymmetricSubset_15sInside30s: a 15s commercial fully
// contained inside a 30s commercial (both ≥ MinShareableDuration). When the
// 30s is being scanned, X sees ~15s of itself hitting the smaller. The
// bidirectional rule catches this via otherCov=100% even when ownCov is
// modest.
func TestClassifyAndFilter_AsymmetricSubset_15sInside30s(t *testing.T) {
	own := uuid.New()   // X (30s) being scanned
	other := uuid.New() // small (15s)

	// X has ~27 windows; ~14 of them hit the small commercial (those covering
	// the 15s region inside X).
	ownRanges := []frameRange{
		{from: 50, until: 82}, {from: 58, until: 90}, {from: 66, until: 98},
		{from: 74, until: 106}, {from: 82, until: 114}, {from: 90, until: 122},
		{from: 98, until: 130}, {from: 106, until: 138}, {from: 114, until: 146},
		{from: 122, until: 154}, {from: 130, until: 162},
	}
	// otherRanges cover ~all of the 15s commercial's frames.
	otherRanges := []frameRange{
		{from: 0, until: 32}, {from: 8, until: 40}, {from: 16, until: 48},
		{from: 24, until: 56}, {from: 32, until: 64}, {from: 40, until: 72},
		{from: 48, until: 80}, {from: 56, until: 88}, {from: 64, until: 96},
		{from: 72, until: 104}, {from: 80, until: 112},
	}

	report := scanReport{
		ownCommercialID: own,
		ownTotalFrames:  frames30s,
		perOther: map[uuid.UUID]*perOtherScan{
			other: {
				otherTotalFrames: frames15s,
				ownRanges:        ownRanges,
				otherRanges:      otherRanges,
			},
		},
	}

	out := classifyAndFilter(report, SubsetThreshold)
	if len(out) != 0 {
		t.Errorf("asymmetric subset (15s inside 30s) must not flag — bidirectional check should catch it via otherCov; got %v", out)
	}
}

// TestClassifyAndFilter_ShortCommercialOwnScanSkipped: a 7s commercial
// (below MinShareableDuration) scanning the catalog. The whole scan must
// short-circuit — no flags from either side. PULSO/ROGGA Pulso Sonoro on
// 2026-05-12 was the production case that exposed why this matters.
func TestClassifyAndFilter_ShortCommercialOwnScanSkipped(t *testing.T) {
	own := uuid.New()  // PULSO (7s) scanning
	other := uuid.New()

	// Even if there's a "sting"-looking pair (low coverages both sides), the
	// scan must be skipped because the scanning commercial is too short.
	report := scanReport{
		ownCommercialID: own,
		ownTotalFrames:  frames7s,
		perOther: map[uuid.UUID]*perOtherScan{
			other: {
				otherTotalFrames: frames30s,
				ownRanges:        []frameRange{{from: 0, until: 7}},
				otherRanges:      []frameRange{{from: 50, until: 57}},
			},
		},
	}

	out := classifyAndFilter(report, SubsetThreshold)
	if len(out) != 0 {
		t.Errorf("short-commercial scan must be skipped entirely, got %v", out)
	}
}

// TestClassifyAndFilter_ShortCommercialOtherSkipped: a 30s commercial scans
// the catalog and finds a "match" against a 7s commercial. The pair must be
// skipped on the OTHER side check — protecting the short victim from being
// flagged by an aggregated set of scans.
func TestClassifyAndFilter_ShortCommercialOtherSkipped(t *testing.T) {
	own := uuid.New()   // 30s scanning
	other := uuid.New() // 7s victim

	// A "sting-looking" pair: small slice of own, small slice of other.
	// Without the short-commercial skip, this would be flagged and PULSO
	// (the victim) accumulates fragments from multiple such scans.
	report := scanReport{
		ownCommercialID: own,
		ownTotalFrames:  frames30s,
		perOther: map[uuid.UUID]*perOtherScan{
			other: {
				otherTotalFrames: frames7s,
				ownRanges:        []frameRange{{from: 50, until: 82}},
				otherRanges:      []frameRange{{from: 0, until: 7}},
			},
		},
	}

	out := classifyAndFilter(report, SubsetThreshold)
	if len(out) != 0 {
		t.Errorf("pair with short other-commercial must be skipped, got %v", out)
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
