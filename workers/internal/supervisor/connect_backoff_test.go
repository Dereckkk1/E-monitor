package supervisor

import (
	"testing"
	"time"
)

// TestBackoffFor pins the exponential connect-backoff schedule the stall
// watchdog applies to a station that has NEVER connected (flavor B stall —
// LastPCMAt zero past the startup grace). The schedule must grow and cap so a
// station whose IP got firewall-banned (silent TCP timeout) is retried with
// widening gaps instead of respawned every ~2min forever — the behaviour that
// built abuse reputation across livespanel/streamingdevideo and got the egress
// IP 34.39.163.110 silently dropped (incidente jun/2026).
func TestBackoffFor(t *testing.T) {
	cases := []struct {
		failures uint32
		want     time.Duration
	}{
		{0, 0}, // no failures yet -> no backoff (immediate restart, like today)
		{1, 1 * time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{4, 8 * time.Minute},
		{5, 16 * time.Minute},
		{6, 30 * time.Minute}, // cap reached
		{7, 30 * time.Minute},
		{100, 30 * time.Minute},
	}
	for _, c := range cases {
		if got := backoffFor(c.failures); got != c.want {
			t.Errorf("backoffFor(%d) = %v, want %v", c.failures, got, c.want)
		}
	}
}

// TestBackoffFor_MonotonicAndCapped is a property check independent of the
// exact schedule: never decreasing, never above the cap, never negative.
func TestBackoffFor_MonotonicAndCapped(t *testing.T) {
	prev := time.Duration(-1)
	for n := uint32(0); n <= 64; n++ {
		got := backoffFor(n)
		if got < 0 {
			t.Fatalf("backoffFor(%d) = %v is negative", n, got)
		}
		if got < prev {
			t.Fatalf("backoffFor not monotonic: backoffFor(%d)=%v < previous %v", n, got, prev)
		}
		if got > connectBackoffMax {
			t.Fatalf("backoffFor(%d)=%v exceeds cap %v", n, got, connectBackoffMax)
		}
		prev = got
	}
}

// TestConnectBackoff_Constants pins the tuning so anybody changing it has to
// read the rationale (mirrors TestStallStartupGrace_Constant). Base 1m / cap
// 30m gives a duty cycle of ~2min hammer per ~32min once a station is fully
// backed off — a ~16x cut versus the old respawn-every-2min loop.
func TestConnectBackoff_Constants(t *testing.T) {
	if connectBackoffBase != 1*time.Minute {
		t.Errorf("connectBackoffBase = %v, want 1m", connectBackoffBase)
	}
	if connectBackoffMax != 30*time.Minute {
		t.Errorf("connectBackoffMax = %v, want 30m", connectBackoffMax)
	}
}

// TestJitterDelay_Bounds verifies the ±20% jitter stays within [0.8d, 1.2d)
// for positive durations (so a fleet of co-blocked stations doesn't retry in
// lockstep) and is a no-op for non-positive inputs.
func TestJitterDelay_Bounds(t *testing.T) {
	const d = 10 * time.Minute
	lo := time.Duration(float64(d) * 0.8)
	hi := time.Duration(float64(d) * 1.2)
	for i := 0; i < 1000; i++ {
		got := jitterDelay(d)
		if got < lo || got >= hi {
			t.Fatalf("jitterDelay(%v) = %v, want within [%v, %v)", d, got, lo, hi)
		}
	}
	if got := jitterDelay(0); got != 0 {
		t.Errorf("jitterDelay(0) = %v, want 0", got)
	}
	if got := jitterDelay(-5 * time.Second); got != -5*time.Second {
		t.Errorf("jitterDelay(negative) = %v, want no-op", got)
	}
}
