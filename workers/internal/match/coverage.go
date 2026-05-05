package match

// CoverageWindow tracks which time-frame offsets have been "seen" for a
// commercial during the detecting phase.
// It estimates temporal coverage: what fraction of the commercial's expected
// frames have produced a match in any window.
type CoverageWindow struct {
	seen        map[int]struct{} // set of OffsetFrames seen
	totalFrames int              // expected total frames for the commercial
}

// NewCoverageWindow creates a new CoverageWindow for a commercial with the
// given total number of expected frames.
func NewCoverageWindow(totalFrames int) *CoverageWindow {
	return &CoverageWindow{
		seen:        make(map[int]struct{}),
		totalFrames: totalFrames,
	}
}

// Add records an offset frame as seen.
func (c *CoverageWindow) Add(offsetFrames int) {
	c.seen[offsetFrames] = struct{}{}
}

// Coverage returns the fraction of unique offsets seen vs totalFrames.
// Returns 0 if totalFrames == 0.
func (c *CoverageWindow) Coverage() float64 {
	if c.totalFrames == 0 {
		return 0
	}
	return float64(len(c.seen)) / float64(c.totalFrames)
}

// Reset clears all seen offsets.
func (c *CoverageWindow) Reset() {
	c.seen = make(map[int]struct{})
}
