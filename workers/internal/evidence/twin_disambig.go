package evidence

type twinVerdict int

const (
	verdictAmbiguous   twinVerdict = iota
	verdictKeep                    // current attribution (self) is right
	verdictReattribute             // the twin is who actually played
)

// chooseTwinByDiscriminative decides between the attributed material (self) and a
// twin by the clip's coverage OF EACH ONE'S DISCRIMINATIVE REGION (the frames
// that separate the twins). Only called when full-coverage tied AND durations are
// ~equal (the spec's gate — the one case coverage/duration can't resolve).
//   - if the higher discriminative coverage < floor -> ambiguous (the signature
//     didn't survive, or the pair has no separating region: disc frames=0 -> 0/0
//     -> 0 < floor).
//   - if one beats the other by at least `margin` -> that one wins.
//   - near-tie above the floor -> ambiguous (never guess).
func chooseTwinByDiscriminative(discSelf, discTwin, floor, margin float64) twinVerdict {
	hi, hiIsTwin := discSelf, false
	if discTwin > discSelf {
		hi, hiIsTwin = discTwin, true
	}
	if hi < floor {
		return verdictAmbiguous
	}
	lo := discTwin
	if hiIsTwin {
		lo = discSelf
	}
	// Near-tie when the winner doesn't clear the loser by at least `margin`.
	// Uses a small epsilon so that winning by *exactly* the margin resolves
	// (float64 makes e.g. 0.20*1.5 == 0.30000000000000004 > 0.30, which would
	// otherwise misfire the tie guard at the exact boundary).
	const eps = 1e-9
	if lo > 0 && hi < lo*margin-eps {
		return verdictAmbiguous // near-tie — not confident enough
	}
	if hiIsTwin {
		return verdictReattribute
	}
	return verdictKeep
}
