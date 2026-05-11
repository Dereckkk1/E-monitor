package sharing

import (
	"testing"

	"github.com/google/uuid"
)

// TestClassifyAndFilter_StingPair verifies the original AMB30/JINGLE shape:
// two 30s commercials sharing a ~6,25s sting at the end. Window-hit ratio is
// well below SubsetThreshold, so both sides MUST get flagged for the sting
// region.
func TestClassifyAndFilter_StingPair(t *testing.T) {
	own := uuid.New()
	other := uuid.New()

	// 27 windows total (representative of a 30s @ 1s hop scan).
	// 4 windows hit the other commercial (~15% — typical sting).
	report := scanReport{
		ownCommercialID: own,
		totalWindows:    27,
		perOther: map[uuid.UUID]*perOtherScan{
			other: {
				windowsWithHits: 4,
				ownRanges: []frameRange{
					{from: 100, until: 132},
					{from: 108, until: 140},
					{from: 116, until: 148},
					{from: 124, until: 156},
				},
				otherRanges: []frameRange{
					{from: 50, until: 82},
					{from: 58, until: 90},
					{from: 66, until: 98},
					{from: 74, until: 106},
				},
			},
		},
	}

	out := classifyAndFilter(report, SubsetThreshold)
	if len(out[own]) == 0 {
		t.Errorf("expected sting pair to flag own (got 0 ranges)")
	}
	if len(out[other]) == 0 {
		t.Errorf("expected sting pair to flag other (got 0 ranges)")
	}
	if got, want := len(out[own]), 4; got != want {
		t.Errorf("own ranges: got %d, want %d", got, want)
	}
}

// TestClassifyAndFilter_SubsetPair reproduces the JINGLE ROGGA VERÃO 30
// vs VERÃO 60 case: a 30s cut taken verbatim from a 60s master. Every
// window of the 30s scan matches the 60s, so window-hit ratio is 100%.
// Result: nothing flagged (the disambiguation-by-duration layer will deal
// with overlapping confirmations).
func TestClassifyAndFilter_SubsetPair(t *testing.T) {
	own := uuid.New() // the 30s cut
	other := uuid.New() // the 60s master

	report := scanReport{
		ownCommercialID: own,
		totalWindows:    27,
		perOther: map[uuid.UUID]*perOtherScan{
			other: {
				windowsWithHits: 27, // 100% — every window matches
				ownRanges:       buildRanges(27),
				otherRanges:     buildRanges(27),
			},
		},
	}

	out := classifyAndFilter(report, SubsetThreshold)
	if len(out) != 0 {
		t.Errorf("expected subset pair to skip flagging entirely, got %v", out)
	}
}

// TestClassifyAndFilter_BoundaryThreshold checks the boundary at exactly
// the threshold. ≥ SubsetThreshold counts as subset (no flag).
func TestClassifyAndFilter_BoundaryThreshold(t *testing.T) {
	own := uuid.New()
	other := uuid.New()

	// 27 windows, 14 hits → ~51.8% (just above 0.5).
	report := scanReport{
		ownCommercialID: own,
		totalWindows:    27,
		perOther: map[uuid.UUID]*perOtherScan{
			other: {
				windowsWithHits: 14,
				ownRanges:       buildRanges(14),
				otherRanges:     buildRanges(14),
			},
		},
	}
	out := classifyAndFilter(report, SubsetThreshold)
	if len(out) != 0 {
		t.Errorf("ratio 14/27 = 51.8%% is above subset threshold; should not flag, got %v", out)
	}

	// 27 windows, 13 hits → ~48.1% (just below 0.5).
	report.perOther[other].windowsWithHits = 13
	out = classifyAndFilter(report, SubsetThreshold)
	if len(out[own]) == 0 || len(out[other]) == 0 {
		t.Errorf("ratio 13/27 = 48.1%% is below subset threshold; should flag normally")
	}
}

// TestClassifyAndFilter_MixedPairs verifies the partitioning: when the
// commercial under scan has BOTH a subset relationship (cut version) AND
// a sting relationship (with a different unrelated commercial), only the
// sting pair gets flagged; the subset pair is skipped.
func TestClassifyAndFilter_MixedPairs(t *testing.T) {
	own := uuid.New()
	subsetOther := uuid.New()
	stingOther := uuid.New()

	report := scanReport{
		ownCommercialID: own,
		totalWindows:    27,
		perOther: map[uuid.UUID]*perOtherScan{
			subsetOther: {
				windowsWithHits: 27, // 100% — full subset
				ownRanges:       buildRanges(27),
				otherRanges:     buildRanges(27),
			},
			stingOther: {
				windowsWithHits: 4, // ~15% — sting
				ownRanges:       buildRanges(4),
				otherRanges:     buildRanges(4),
			},
		},
	}

	out := classifyAndFilter(report, SubsetThreshold)
	if _, ok := out[subsetOther]; ok {
		t.Errorf("subset pair should NOT be flagged, but subsetOther appeared in output")
	}
	if got, want := len(out[stingOther]), 4; got != want {
		t.Errorf("sting otherRanges: got %d, want %d", got, want)
	}
	// own should accumulate ONLY the sting's ranges (subset's are dropped).
	if got, want := len(out[own]), 4; got != want {
		t.Errorf("own ranges (sting only): got %d, want %d", got, want)
	}
}

// TestClassifyAndFilter_EmptyScan covers a scan that found no matches at all
// (first commercial in an empty catalog, or no spectral overlap with anything).
func TestClassifyAndFilter_EmptyScan(t *testing.T) {
	report := scanReport{
		ownCommercialID: uuid.New(),
		totalWindows:    27,
		perOther:        map[uuid.UUID]*perOtherScan{},
	}
	out := classifyAndFilter(report, SubsetThreshold)
	if len(out) != 0 {
		t.Errorf("empty scan should produce empty output, got %v", out)
	}
}

// TestClassifyAndFilter_ZeroWindows guards against divide-by-zero when the
// PCM is shorter than one analysis window.
func TestClassifyAndFilter_ZeroWindows(t *testing.T) {
	report := scanReport{
		ownCommercialID: uuid.New(),
		totalWindows:    0,
		perOther: map[uuid.UUID]*perOtherScan{
			uuid.New(): {windowsWithHits: 0},
		},
	}
	out := classifyAndFilter(report, SubsetThreshold)
	if len(out) != 0 {
		t.Errorf("totalWindows=0 should produce empty output (no division), got %v", out)
	}
}

func buildRanges(n int) []frameRange {
	out := make([]frameRange, n)
	for i := 0; i < n; i++ {
		out[i] = frameRange{from: int32(i * 8), until: int32(i*8 + 32)}
	}
	return out
}
