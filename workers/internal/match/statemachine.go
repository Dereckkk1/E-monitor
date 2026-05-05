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
	StateCooldown  State = iota
)

// ConfirmedDetection is emitted when the state machine confirms a detection.
type ConfirmedDetection struct {
	CommercialShortID int32
	StationID         string // passed in at construction
	DetectedAt        time.Time
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

	// Configuration
	minScore         int           // minimum MatchResult.Score to count as a hit
	minCoverage      float64       // minimum Coverage() to confirm
	confirmTimeout   time.Duration // max time in Detecting before reset (no confirm)
	cooldownDuration time.Duration
	cooldownUntil    time.Time
}

// NewStateMachine creates a new StateMachine for tracking one commercial on one station.
func NewStateMachine(
	stationID string,
	commercialShortID int32,
	totalFrames int,
	minScore int,
	minCoverage float64,
	confirmTimeout time.Duration,
	cooldownDuration time.Duration,
	log *zap.Logger,
) *StateMachine {
	return &StateMachine{
		stationID:         stationID,
		commercialShortID: commercialShortID,
		state:             StateIdle,
		coverage:          NewCoverageWindow(totalFrames),
		minScore:          minScore,
		minCoverage:       minCoverage,
		confirmTimeout:    confirmTimeout,
		cooldownDuration:  cooldownDuration,
		log:               log,
	}
}

// Update processes one MatchResult for this commercial.
// Returns a *ConfirmedDetection if the state just transitioned to Confirmed,
// or nil otherwise.
// After confirmation, resets to Idle automatically.
func (sm *StateMachine) Update(result MatchResult, now time.Time) *ConfirmedDetection {
	switch sm.state {
	case StateIdle:
		if result.Score >= sm.minScore {
			sm.state = StateDetecting
			sm.firstMatchAt = now
			sm.coverage.Add(result.OffsetFrames)
			sm.log.Info("detecting started",
				zap.String("stationID", sm.stationID),
				zap.Int32("commercialShortID", sm.commercialShortID),
				zap.Int("score", result.Score),
				zap.Int("offsetFrames", result.OffsetFrames),
			)
		}

	case StateDetecting:
		if result.Score >= sm.minScore {
			sm.coverage.Add(result.OffsetFrames)
			if sm.coverage.Coverage() >= sm.minCoverage {
				confidence := sm.coverage.Coverage()
				detection := &ConfirmedDetection{
					CommercialShortID: sm.commercialShortID,
					StationID:         sm.stationID,
					DetectedAt:        now,
					OffsetFrames:      result.OffsetFrames,
					Confidence:        confidence,
				}
				sm.log.Info("detection confirmed",
					zap.String("stationID", sm.stationID),
					zap.Int32("commercialShortID", sm.commercialShortID),
					zap.Float64("confidence", confidence),
				)
				sm.coverage.Reset()
				sm.state = StateCooldown
				sm.cooldownUntil = now.Add(sm.cooldownDuration)
				return detection
			}
		}
	}

	return nil
}

// Tick checks if the detecting phase has timed out, or if cooldown has expired.
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
