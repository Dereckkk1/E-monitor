package match

import (
	"time"

	"go.uber.org/zap"
)

// State represents the detection phase.
type State int

const (
	StateIdle      State = iota
	StateDetecting State = iota
	// StateUncertain is entered from Detecting when coverage is in an
	// ambiguous zone (borderline coverage, or high score with low coverage).
	// It awaits neural verification via ResolveNeural() to either confirm or
	// drop the detection. Tick() expires the state if no resolution arrives.
	StateUncertain State = iota
	StateCooldown  State = iota
)

// ConfirmedDetection is emitted when the state machine confirms a detection.
type ConfirmedDetection struct {
	CommercialShortID int32
	StationID         string // passed in at construction
	DetectedAt        time.Time
	FirstMatchAt      time.Time // when the detecting phase started (≈ commercial start)
	OffsetFrames      int
	Confidence        float64 // coverage at confirmation time
}

// StateMachine tracks detection state for one commercial on one station.
type StateMachine struct {
	stationID         string
	commercialShortID int32
	state             State
	coverage          *CoverageWindow
	firstMatchAt      time.Time
	log               *zap.Logger

	// StateDetecting tracking
	detectingWindows int

	// StateUncertain tracking (fase2 neural verification)
	uncertainWindows      int
	uncertainOffsetFrames int

	// Configuration
	minScore            int           // minimum MatchResult.Score to count as a hit
	minTemporalCoverage float64       // minimum Coverage() (time elapsed / commercial duration) to confirm
	confirmTimeout      time.Duration // max time in Detecting before reset (no confirm)
	cooldownDuration    time.Duration
	cooldownUntil       time.Time
}

// NewStateMachine creates a new StateMachine for tracking one commercial on one station.
// minTemporalCoverage is the fraction of the commercial's duration that must
// elapse between the first and most-recent sustained match before a detection
// is confirmed. This is the primary false-positive defense: random audio cannot
// sustain delta-aligned hits over a meaningful fraction of a commercial.
func NewStateMachine(
	stationID string,
	commercialShortID int32,
	totalFrames int,
	minScore int,
	minTemporalCoverage float64,
	confirmTimeout time.Duration,
	cooldownDuration time.Duration,
	log *zap.Logger,
) *StateMachine {
	return &StateMachine{
		stationID:           stationID,
		commercialShortID:   commercialShortID,
		state:               StateIdle,
		coverage:            NewCoverageWindow(totalFrames),
		minScore:            minScore,
		minTemporalCoverage: minTemporalCoverage,
		confirmTimeout:      confirmTimeout,
		cooldownDuration:    cooldownDuration,
		log:                 log,
	}
}

// Update processes one MatchResult for this commercial.
// Returns a *ConfirmedDetection if the state just transitioned to Confirmed,
// or nil otherwise.
// After confirmation, resets to Idle automatically.
func (sm *StateMachine) Update(result MatchResult, now time.Time) *ConfirmedDetection {
	// The state machine credits coverage strictly from UniqueScore — hits that
	// came from hashes flagged as shared with another commercial don't count.
	// This is the false-positive defense for cases like the AMB30/JINGLE
	// pair: when commercial A plays and its end-sting matches commercial B,
	// B accumulates Score from the shared hashes only. UniqueScore stays at 0
	// and B never advances out of Idle. Total Score is left for the engine's
	// own minScoreCoverage filter and for diagnostics.
	switch sm.state {
	case StateIdle:
		if result.UniqueScore >= sm.minScore {
			sm.state = StateDetecting
			sm.firstMatchAt = now
			sm.detectingWindows = 0
			sm.coverage.Add(result.OffsetFrames, now)
			sm.log.Info("detecting started",
				zap.String("stationID", sm.stationID),
				zap.Int32("commercialShortID", sm.commercialShortID),
				zap.Int("score", result.Score),
				zap.Int("uniqueScore", result.UniqueScore),
				zap.Int("offsetFrames", result.OffsetFrames),
			)
		}

	case StateDetecting:
		sm.detectingWindows++
		if result.UniqueScore >= sm.minScore {
			sm.coverage.Add(result.OffsetFrames, now)
			if sm.coverage.Coverage() >= sm.minTemporalCoverage {
				confidence := sm.coverage.Coverage()
				detection := &ConfirmedDetection{
					CommercialShortID: sm.commercialShortID,
					StationID:         sm.stationID,
					DetectedAt:        now,
					FirstMatchAt:      sm.firstMatchAt,
					OffsetFrames:      result.OffsetFrames,
					Confidence:        confidence,
				}
				sm.log.Info("detection confirmed",
					zap.String("stationID", sm.stationID),
					zap.Int32("commercialShortID", sm.commercialShortID),
					zap.Float64("confidence", confidence),
				)
				sm.coverage.Reset()
				sm.detectingWindows = 0
				sm.state = StateCooldown
				sm.cooldownUntil = now.Add(sm.cooldownDuration)
				return detection
			}

			// Transition to StateUncertain if coverage is in the ambiguous zone.
			// Two paths: (1) borderline coverage ≥0.4 but below threshold, or
			// (2) high score (3× threshold) with low coverage ≥0.2. The latter catches
			// noise-disrupted matches where the neural verifier can disambiguate.
			// Requires at least 3 windows processed.
			cov := sm.coverage.Coverage()
			highScore := result.UniqueScore >= 3*sm.minScore
			covPath := cov >= 0.4 && cov < sm.minTemporalCoverage
			scorePath := highScore && cov >= 0.2 && cov < sm.minTemporalCoverage
			if sm.detectingWindows >= 3 && (covPath || scorePath) {
				sm.state = StateUncertain
				sm.uncertainWindows = 0
				sm.uncertainOffsetFrames = result.OffsetFrames
				sm.log.Info("detection uncertain, awaiting neural resolution",
					zap.String("stationID", sm.stationID),
					zap.Int32("commercialShortID", sm.commercialShortID),
					zap.Float64("coverage", cov),
					zap.Int("score", result.Score),
				)
				return nil
			}
		}

	case StateUncertain:
		// Expiry is handled by Tick(); Update() only processes neural resolution
		// via ResolveNeural(). Discard any non-neural match results here.
		return nil

	case StateCooldown:
		// Matches during cooldown are discarded to prevent duplicate detections.
	}

	return nil
}

// Tick checks if the detecting phase has timed out, cooldown has expired,
// or if StateUncertain has lingered too long without neural resolution.
// Call once per window.
func (sm *StateMachine) Tick(now time.Time) {
	switch sm.state {
	case StateDetecting:
		if now.Sub(sm.firstMatchAt) > sm.confirmTimeout {
			sm.log.Info("detection timed out, resetting to idle",
				zap.String("stationID", sm.stationID),
				zap.Int32("commercialShortID", sm.commercialShortID),
			)
			sm.coverage.Reset()
			sm.detectingWindows = 0
			sm.state = StateIdle
		}
	case StateUncertain:
		sm.uncertainWindows++
		if sm.uncertainWindows >= 3 {
			sm.log.Info("uncertain detection expired without neural resolution",
				zap.String("stationID", sm.stationID),
				zap.Int32("commercialShortID", sm.commercialShortID),
			)
			sm.coverage.Reset()
			sm.state = StateIdle
		}
	case StateCooldown:
		if now.After(sm.cooldownUntil) {
			sm.log.Info("cooldown expired, back to idle",
				zap.String("stationID", sm.stationID),
				zap.Int32("commercialShortID", sm.commercialShortID),
			)
			sm.state = StateIdle
		}
	}
}

// State returns the current state.
func (sm *StateMachine) State() State {
	return sm.state
}

// IsUncertain returns true when the state machine is in the StateUncertain state,
// waiting for neural verification to decide the detection.
func (sm *StateMachine) IsUncertain() bool { return sm.state == StateUncertain }

// UncertainOffset returns the OffsetFrames recorded when the machine entered StateUncertain.
func (sm *StateMachine) UncertainOffset() int { return sm.uncertainOffsetFrames }

// ResolveNeural resolves an uncertain detection based on neural cosine similarity score.
// Returns a *ConfirmedDetection if similarity >= 0.85, otherwise resets to Idle.
// If called outside StateUncertain, returns nil without side effects.
func (sm *StateMachine) ResolveNeural(similarity float64, now time.Time) *ConfirmedDetection {
	if sm.state != StateUncertain {
		return nil
	}
	if similarity >= 0.85 {
		confidence := sm.coverage.Coverage()
		detection := &ConfirmedDetection{
			CommercialShortID: sm.commercialShortID,
			StationID:         sm.stationID,
			DetectedAt:        now,
			FirstMatchAt:      sm.firstMatchAt,
			OffsetFrames:      sm.uncertainOffsetFrames,
			Confidence:        confidence,
		}
		sm.log.Info("uncertain detection confirmed via neural",
			zap.String("stationID", sm.stationID),
			zap.Int32("commercialShortID", sm.commercialShortID),
			zap.Float64("similarity", similarity),
			zap.Float64("confidence", confidence),
		)
		sm.coverage.Reset()
		sm.detectingWindows = 0
		sm.uncertainWindows = 0
		sm.state = StateCooldown
		sm.cooldownUntil = now.Add(sm.cooldownDuration)
		return detection
	}
	sm.log.Info("uncertain detection rejected by neural",
		zap.String("stationID", sm.stationID),
		zap.Int32("commercialShortID", sm.commercialShortID),
		zap.Float64("similarity", similarity),
	)
	sm.coverage.Reset()
	sm.detectingWindows = 0
	sm.uncertainWindows = 0
	sm.state = StateIdle
	return nil
}
