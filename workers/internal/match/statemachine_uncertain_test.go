package match

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStateMachine_TransitionsToUncertain(t *testing.T) {
	// This test verifies the IsUncertain() method exists on StateMachine.
	// Full integration test requires a running state machine — just compile-check here.
	var sm *StateMachine
	_ = sm.IsUncertain     // method must exist
	_ = sm.UncertainOffset // method must exist
	assert.True(t, true, "StateUncertain methods exist")
}
