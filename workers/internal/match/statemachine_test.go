package match

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestStateMachine(totalFrames int, minScore int, minCoverage float64) *StateMachine {
	log, _ := zap.NewDevelopment()
	return NewStateMachine(
		"station-test",
		int32(42),
		totalFrames,
		minScore,
		minCoverage,
		5*time.Second,        // confirmTimeout
		100*time.Millisecond, // cooldownDuration curto para testes
		log,
	)
}

// TestStateMachine_ConfirmsAfterCoverage feeds enough MatchResults with varied
// OffsetFrames until coverage >= minCoverage, expecting a ConfirmedDetection.
func TestStateMachine_ConfirmsAfterCoverage(t *testing.T) {
	const (
		totalFrames = 10
		minScore    = 5
		minCoverage = 0.8
	)

	sm := newTestStateMachine(totalFrames, minScore, minCoverage)
	now := time.Now()

	var confirmed *ConfirmedDetection

	// Send 10 results with offsets 0..9 (score=10, well above minScore=5).
	// After 8 unique offsets, coverage = 0.8 >= minCoverage → confirmation.
	for i := 0; i < totalFrames; i++ {
		result := MatchResult{
			CommercialShortID: int32(42),
			Score:             10,
			OffsetFrames:      i,
		}
		cd := sm.Update(result, now.Add(time.Duration(i)*time.Millisecond))
		if cd != nil {
			confirmed = cd
			break
		}
	}

	require.NotNil(t, confirmed, "should have received a ConfirmedDetection")
	assert.Equal(t, int32(42), confirmed.CommercialShortID)
	assert.Equal(t, "station-test", confirmed.StationID)
	assert.GreaterOrEqual(t, confirmed.Confidence, minCoverage,
		"confidence should be >= minCoverage at confirmation")

	// After confirmation, state should be in Cooldown
	assert.Equal(t, StateCooldown, sm.State(), "após confirmação deve entrar em Cooldown")

	// Avançar além do cooldown (100ms).
	sm.Tick(time.Now().Add(200 * time.Millisecond))
	assert.Equal(t, StateIdle, sm.State(), "após cooldown expirar deve voltar a Idle")
}

// TestStateMachine_TimeoutResetsToIdle feeds one hit to enter Detecting, then
// calls Tick with a time far in the future. State should return to Idle with
// no confirmation.
func TestStateMachine_TimeoutResetsToIdle(t *testing.T) {
	sm := newTestStateMachine(10, 5, 0.8)
	now := time.Now()

	// One hit to enter Detecting state
	result := MatchResult{
		CommercialShortID: int32(42),
		Score:             10,
		OffsetFrames:      0,
	}
	cd := sm.Update(result, now)
	assert.Nil(t, cd, "single hit should not produce a confirmation")
	assert.Equal(t, StateDetecting, sm.State(), "should be in Detecting after first hit")

	// Tick with a time beyond the confirmTimeout (5s)
	future := now.Add(10 * time.Second)
	sm.Tick(future)

	assert.Equal(t, StateIdle, sm.State(), "state should reset to Idle after timeout")
}

// TestStateMachine_IdleWithLowScore verifies that a low-score result in Idle
// state does not cause a transition to Detecting.
func TestStateMachine_IdleWithLowScore(t *testing.T) {
	sm := newTestStateMachine(10, 5, 0.8)
	now := time.Now()

	// Low score result (below minScore=5)
	result := MatchResult{
		CommercialShortID: int32(42),
		Score:             2,
		OffsetFrames:      0,
	}
	cd := sm.Update(result, now)

	assert.Nil(t, cd, "low-score result should not produce a confirmation")
	assert.Equal(t, StateIdle, sm.State(), "state should remain Idle with low-score result")
}

// TestStateMachine_IgnoresMatchDuringCooldown verifies that high-score matches
// arriving while in StateCooldown are silently discarded.
func TestStateMachine_IgnoresMatchDuringCooldown(t *testing.T) {
	const (
		totalFrames = 10
		minScore    = 5
		minCoverage = 0.8
	)

	sm := newTestStateMachine(totalFrames, minScore, minCoverage)
	now := time.Now()

	// Drive to Confirmed by sending enough hits.
	for i := 0; i < totalFrames; i++ {
		result := MatchResult{
			CommercialShortID: int32(42),
			Score:             10,
			OffsetFrames:      i,
		}
		if cd := sm.Update(result, now.Add(time.Duration(i)*time.Millisecond)); cd != nil {
			break
		}
	}
	require.Equal(t, StateCooldown, sm.State(), "should be in Cooldown after confirmation")

	// Send another high-score match during cooldown — must be discarded.
	result := MatchResult{
		CommercialShortID: int32(42),
		Score:             10,
		OffsetFrames:      5,
	}
	cd := sm.Update(result, now.Add(50*time.Millisecond))
	assert.Nil(t, cd, "match during cooldown must not produce a detection")
	assert.Equal(t, StateCooldown, sm.State(), "state must remain Cooldown")
}
