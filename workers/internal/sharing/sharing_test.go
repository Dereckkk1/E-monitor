package sharing

import (
	"math/rand"
	"testing"

	"github.com/google/uuid"

	"radiocheck/internal/audit"
	"radiocheck/internal/fingerprint"
	"radiocheck/internal/index"
	"radiocheck/pkg/audio"
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
	own := uuid.New()   // VERÃO 30
	other := uuid.New() // VERÃO 60

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
	own := uuid.New() // PULSO (7s) scanning
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

// TestMinScoreFromEnv covers resolution of the shared-region qualifying score
// from SHARING_MIN_SCORE. Uses an injected getenv so it never mutates process
// state. The value was raised from the historical 5 after the #2 density
// change lifted the material-vs-material noise floor (incident 2026-06-15).
func TestMinScoreFromEnv(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"unset falls back to default", "", DefaultMinScore},
		{"valid value", "15", 15},
		{"trims whitespace", "  18  ", 18},
		{"non-numeric falls back", "abc", DefaultMinScore},
		{"zero falls back", "0", DefaultMinScore},
		{"negative falls back", "-3", DefaultMinScore},
		{"default itself", "20", 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := minScoreFromEnv(func(string) string { return c.raw })
			if got != c.want {
				t.Errorf("minScoreFromEnv(%q) = %d, want %d", c.raw, got, c.want)
			}
		})
	}
}

// --- Task 4: ComputeTwinOverlap pure-core tests -------------------------------
//
// The DB+decode shell (ComputeTwinOverlap) is intentionally untested — it is
// thin glue mirroring MarkSharedHashes' shell (which also has no test). We test
// the pure cores overlapRangesForOther/overlapRangesXvsY here with synthetic
// dense noise, mirroring similarity/dense_audio_test.go.

// makeDenseNoise builds deterministic broadband noise. Run through the
// fingerprint pipeline it yields ~300+ hashes/s — the dense regime that gives
// the matcher a real signal to align window-by-window (as opposed to a sparse
// jingle where a single window covers the whole clip).
func makeDenseNoise(seed int64, seconds int) []float32 {
	r := rand.New(rand.NewSource(seed))
	n := seconds * fingerprint.SampleRate
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(r.Float64()*2 - 1)
	}
	return out
}

func denseHashes(pcm []float32) []audio.Hash {
	filtered := audio.ApplyHighPass(pcm, 100.0, 16000)
	normalized := audio.NormalizeRMS(filtered, -20.0)
	spec := audio.STFT(normalized)
	peaks := audio.PickPeaks(spec)
	return audio.GenerateHashes(peaks)
}

func indexFromHashes(hs []audio.Hash, short int32) index.Index {
	idx := make(index.Index)
	for _, h := range hs {
		idx[h.Value] = append(idx[h.Value], index.Entry{
			CommercialShortID: short,
			TimeFrame:         int32(h.TimeFrame),
		})
	}
	return idx
}

// TestOverlapRangesForOther exercises the pure merge+convert helper against a
// synthetic scanReport (no audio, no DB) — same style as the classifyAndFilter
// tests. Two overlapping X-side ranges must merge into one audit.FrameRange,
// and an absent other must yield nil.
func TestOverlapRangesForOther(t *testing.T) {
	other := uuid.New()
	report := scanReport{
		ownCommercialID: uuid.New(),
		ownTotalFrames:  frames30s,
		perOther: map[uuid.UUID]*perOtherScan{
			other: {
				otherTotalFrames: frames30s,
				ownRanges:        []frameRange{{10, 42}, {40, 80}},
			},
		},
	}

	got := overlapRangesForOther(report, other)
	want := []audit.FrameRange{{Lo: 10, Hi: 80}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("overlapRangesForOther merged wrong: got %v, want %v", got, want)
	}

	if nilOut := overlapRangesForOther(report, uuid.New()); nilOut != nil {
		t.Errorf("absent other must yield nil, got %v", nilOut)
	}
}

// TestOverlapRangesXvsY_ExcludesDivergentTail is the confidence-builder for the
// whole feature: it proves the overlap concentrates in the SHARED region of two
// acoustic twins and essentially none of it lands in the DISCRIMINATIVE tail.
//
// X = shared(seed 1, 24s) ++ tailX(seed 2, 6s)
// Y = shared(seed 1, 24s) ++ tailY(seed 3, 6s)
// The first 24s of X and Y are byte-identical; the last 6s differ. Sliding X's
// PCM against a Y-only index must return overlap ranges that cover the shared
// head and stop at the seam (± a few frames of window straddle).
func TestOverlapRangesXvsY_ExcludesDivergentTail(t *testing.T) {
	shared := makeDenseNoise(1, 24)
	pcmX := append(append([]float32{}, shared...), makeDenseNoise(2, 6)...)
	pcmY := append(append([]float32{}, shared...), makeDenseNoise(3, 6)...)

	const (
		xShort int32 = 3
		yShort int32 = 7
	)
	xID := uuid.New()
	yID := uuid.New()

	storeY := index.New()
	storeY.Swap(indexFromHashes(denseHashes(pcmY), yShort))

	overlap, totalX := overlapRangesXvsY(pcmX, storeY, xShort, yShort, xID, yID, len(pcmY)/2048, DefaultMinScore)

	// X is 30s → ~234 frames.
	const wantTotalX = 30 * fingerprint.SampleRate / 2048 // 234
	if totalX < wantTotalX-2 || totalX > wantTotalX+2 {
		t.Errorf("totalX = %d, want ≈ %d", totalX, wantTotalX)
	}

	sharedFrames := 24 * fingerprint.SampleRate / 2048 // ≈187

	// Convert overlap back to frameRange to measure coverage within regions.
	frs := make([]frameRange, len(overlap))
	for i, r := range overlap {
		frs[i] = frameRange{from: r.Lo, until: r.Hi}
	}
	headRegion := clampRanges(frs, 0, int32(sharedFrames))
	tailRegion := clampRanges(frs, int32(sharedFrames), int32(totalX))
	headCov := frameCoverage(headRegion)
	tailCov := frameCoverage(tailRegion)

	t.Logf("totalX=%d sharedFrames=%d | overlap merged=%d frames across %d ranges | headCov=%d (%.0f%% of shared) tailCov=%d",
		totalX, sharedFrames, frameCoverage(frs), len(overlap),
		headCov, 100*float64(headCov)/float64(sharedFrames), tailCov)

	// The overlap must cover a substantial part of the shared head.
	if headCov < sharedFrames/2 {
		t.Errorf("overlap covers only %d/%d frames of the shared region (< 50%%) — the twin overlap is not being detected", headCov, sharedFrames)
	}
	// And essentially none of the discriminative tail. The last matching window
	// starts a few frames before the 24s seam and spans 4s (≈31 frames), so the
	// merged overlap runs a fraction of one window past the seam (measured: 23
	// frames ≈ 3s). That straddle is inherent to a 4s@1s window and does NOT
	// weaken the guard: it is bounded by one window length, and the FAR half of
	// the tail (the genuinely divergent [~+31, +47) frames) stays clean. A leak
	// larger than one window would mean the tail is being matched on its own
	// (a real bug). Slop = one full window (32 frames).
	const tailSlop = 32
	if tailCov > tailSlop {
		t.Errorf("overlap leaks %d frames into the discriminative tail (> %d slop = one 4s window) — the tail must stay discriminative", tailCov, tailSlop)
	}
}

// clampRanges intersects each range with [lo, hi), dropping empties. Test-only
// helper to measure how much of the overlap lands inside a region.
func clampRanges(rs []frameRange, lo, hi int32) []frameRange {
	var out []frameRange
	for _, r := range rs {
		f, u := r.from, r.until
		if f < lo {
			f = lo
		}
		if u > hi {
			u = hi
		}
		if f < u {
			out = append(out, frameRange{f, u})
		}
	}
	return out
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
