package match

import "time"

// Frames per second of analysis (sampleRate / hopSize at 16kHz / 2048 = 7.8125).
// Each window covers ~31 frames (4s window at 7.8125 frames/s).
const (
	framesPerSecond = 16000.0 / 2048.0
	framesPerWindow = 32 // 4-second window rounded up
)

// CoverageWindow estimates how much of a commercial's duration has been observed
// during the detecting phase, by tracking the time elapsed between the first
// and most-recent successful match.
//
// Rationale: when a commercial plays continuously, the histogram peak (offset)
// stays roughly constant — what changes is the input position. Tracking unique
// offsets undercounts coverage. Tracking time-since-first-match gives an
// honest estimate: each new match means another window of the master was just
// observed, so the visible portion of the commercial grew by ~one window.
type CoverageWindow struct {
	totalFrames int
	first       time.Time
	last        time.Time
	count       int
}

// NewCoverageWindow creates a coverage window for a commercial with the given
// total number of expected frames.
func NewCoverageWindow(totalFrames int) *CoverageWindow {
	return &CoverageWindow{totalFrames: totalFrames}
}

// Add records a successful match at the given wall-clock time.
// The offset is accepted for API compatibility but not used by the time-based
// estimator.
func (c *CoverageWindow) Add(_ int, now time.Time) {
	if c.count == 0 {
		c.first = now
	}
	c.last = now
	c.count++
}

// Coverage returns the estimated fraction of the commercial that has been
// observed playing through. Bounded to [0, 1].
func (c *CoverageWindow) Coverage() float64 {
	if c.count == 0 || c.totalFrames == 0 {
		return 0
	}
	elapsed := c.last.Sub(c.first).Seconds()
	frames := int(elapsed*framesPerSecond) + framesPerWindow
	if frames > c.totalFrames {
		frames = c.totalFrames
	}
	return float64(frames) / float64(c.totalFrames)
}

// Reset clears the coverage window.
func (c *CoverageWindow) Reset() {
	c.first = time.Time{}
	c.last = time.Time{}
	c.count = 0
}
