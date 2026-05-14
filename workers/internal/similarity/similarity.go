// Package similarity scans a freshly-fingerprinted material against the other
// materials of the same client and persists the top similarity match on the
// materials row. Used to warn operators about likely-duplicate uploads
// before they pollute detection reports. See
// docs/superpowers/specs/2026-05-13-material-similarity-warning-design.md.
package similarity

import (
	"sort"

	"github.com/google/uuid"
)

const (
	// WindowSeconds is the analysis window length (matches runtime matcher).
	WindowSeconds = 4
	// HopSeconds is the hop between consecutive analysis windows.
	HopSeconds = 1
	// MinScore is the histogram peak score that qualifies a window as a hit
	// in the cov computation. Aligned with the runtime matcher.
	MinScore = 5
	// WarnThreshold is the score (max(ownCov, otherCov)) at or above which
	// we surface the warning. Calibration: <5% noise, 15-25% sting, 50%+ subset.
	WarnThreshold = 0.15
)

// frameRange is a half-open [from, until) interval on the time_frame axis.
type frameRange struct{ from, until int32 }

// pairScan holds the per-other-material accumulator during a scan: the other
// material's total frame count plus the matched ranges on both sides of the
// pair (own = the material being scanned, other = the candidate match).
type pairScan struct {
	otherTotalFrames int
	ownRanges        []frameRange
	otherRanges      []frameRange
}

// scanReport aggregates a complete scan: own material's total frames and per-
// other-material pair scans. Kept as a named type so pickTopMatch is testable
// without running audio + DB.
type scanReport struct {
	ownTotalFrames int
	perOther       map[uuid.UUID]*pairScan
}

// pickTopMatch returns the (otherID, score) with the highest
// score = max(ownCov, otherCov) across all candidates. Returns (uuid.Nil, 0)
// when the scan is empty or own has zero frames.
//
// Rationale for max: captures asymmetric subset relationships. For a 30s cut
// of a 60s master, ownCov=100% but otherCov=50%. Using max gives 100%, which
// matches operator intuition ("this audio IS that one"). For a sting overlap
// (~20% on both sides), max=20% — still surfaces above the WarnThreshold but
// well below subset territory.
func pickTopMatch(report scanReport) (uuid.UUID, float64) {
	if report.ownTotalFrames == 0 || len(report.perOther) == 0 {
		return uuid.Nil, 0
	}
	var topID uuid.UUID
	var topScore float64
	for otherID, s := range report.perOther {
		ownCov := float64(frameCoverage(s.ownRanges)) / float64(report.ownTotalFrames)
		var otherCov float64
		if s.otherTotalFrames > 0 {
			otherCov = float64(frameCoverage(s.otherRanges)) / float64(s.otherTotalFrames)
		}
		// Clamp at 1.0: window-based hits can extend past the nominal frame
		// total when the last window straddles the end of the material.
		if ownCov > 1.0 {
			ownCov = 1.0
		}
		if otherCov > 1.0 {
			otherCov = 1.0
		}
		score := ownCov
		if otherCov > score {
			score = otherCov
		}
		if score > topScore {
			topScore = score
			topID = otherID
		}
	}
	return topID, topScore
}

// frameCoverage returns the total frame count covered by the union of the
// input ranges (after merging overlaps).
func frameCoverage(rs []frameRange) int {
	if len(rs) == 0 {
		return 0
	}
	merged := mergeRanges(rs)
	total := 0
	for _, r := range merged {
		total += int(r.until - r.from)
	}
	return total
}

// mergeRanges merges overlapping/contiguous ranges into a minimal covering set.
func mergeRanges(rs []frameRange) []frameRange {
	if len(rs) == 0 {
		return nil
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].from < rs[j].from })
	merged := []frameRange{rs[0]}
	for _, r := range rs[1:] {
		last := &merged[len(merged)-1]
		if r.from <= last.until {
			if r.until > last.until {
				last.until = r.until
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}
