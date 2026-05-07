package webhook

import (
	"testing"
	"time"
)

// TestBackoffSchedule guarantees the spec'd retry curve (1m, 5m, 15m, 1h, 4h)
// hasn't drifted. Receivers depend on this to estimate "when to give up".
func TestBackoffSchedule(t *testing.T) {
	want := []time.Duration{
		1 * time.Minute,
		5 * time.Minute,
		15 * time.Minute,
		1 * time.Hour,
		4 * time.Hour,
	}
	if len(backoffSchedule) != len(want) {
		t.Fatalf("backoffSchedule length: got %d want %d", len(backoffSchedule), len(want))
	}
	for i, d := range want {
		if backoffSchedule[i] != d {
			t.Fatalf("backoffSchedule[%d]: got %s want %s", i, backoffSchedule[i], d)
		}
	}
	if maxAttempts != 5 {
		t.Fatalf("maxAttempts: got %d want 5", maxAttempts)
	}
}
