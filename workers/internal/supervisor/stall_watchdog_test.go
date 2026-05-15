package supervisor

import (
	"testing"
	"time"
)

// TestIsStalled covers the decision the stall watchdog makes every 30s for
// each running worker. There are two distinct stall flavors and both must
// trigger a restart:
//
//  1. Worker WAS producing PCM, then stopped (the original §8.5 watchdog
//     contract — "no PCM for >60s").
//
//  2. Worker has NEVER produced PCM since it started, and the startup grace
//     period has elapsed. This is the case that bit us in production: a
//     station's stream_url was edited, the supervisor never reloaded, ffmpeg
//     kept retrying the dead URL and `LastPCMAt` stayed zero. The old code
//     bailed out on `last.IsZero()` and the worker stayed zombified forever.
func TestIsStalled(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		last      time.Time
		startedAt time.Time
		want      bool
	}{
		// ── case 1: stale PCM (flavor A) ────────────────────────────────
		{
			name:      "pcm stale by 70s -> stalled",
			last:      now.Add(-70 * time.Second),
			startedAt: now.Add(-10 * time.Minute),
			want:      true,
		},
		{
			name:      "pcm fresh (30s ago) -> not stalled",
			last:      now.Add(-30 * time.Second),
			startedAt: now.Add(-10 * time.Minute),
			want:      false,
		},
		{
			name:      "pcm exactly 60s ago -> not stalled (boundary)",
			last:      now.Add(-60 * time.Second),
			startedAt: now.Add(-10 * time.Minute),
			want:      false,
		},
		// ── case 2: never produced PCM (flavor B) ───────────────────────
		{
			name:      "never started, within startup grace -> not stalled",
			last:      time.Time{}, // zero
			startedAt: now.Add(-30 * time.Second),
			want:      false,
		},
		{
			name:      "never started, just past startup grace -> stalled",
			last:      time.Time{},
			startedAt: now.Add(-(stallStartupGrace + time.Second)),
			want:      true,
		},
		{
			name:      "never started, long past grace -> stalled",
			last:      time.Time{},
			startedAt: now.Add(-15 * time.Minute),
			want:      true,
		},
		// ── case 3: defensive (zero startedAt, should not panic) ────────
		{
			name:      "never started, startedAt zero -> not stalled (worker not registered yet)",
			last:      time.Time{},
			startedAt: time.Time{},
			want:      false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isStalled(c.last, c.startedAt, now); got != c.want {
				t.Fatalf("isStalled(last=%v, startedAt=%v, now=%v) = %v, want %v",
					c.last, c.startedAt, now, got, c.want)
			}
		})
	}
}

// TestStallStartupGrace_Constant pins the startup grace so anybody changing
// it has to read the rationale. 2 minutes is the trade-off: long enough that
// a slow first ffmpeg connect (DNS, TLS handshake, ICY metadata negotiation)
// completes before we declare stall, short enough that a worker stuck on a
// dead URL gets restarted on the second watchdog tick after grace expires.
func TestStallStartupGrace_Constant(t *testing.T) {
	if stallStartupGrace != 2*time.Minute {
		t.Fatalf("stallStartupGrace = %v, want 2m", stallStartupGrace)
	}
}
