package supervisor

import (
	"math/rand"
	"time"

	"github.com/google/uuid"
)

// Circuit breaker for streams that never connect (flavor B stall).
//
// Background: when a stream's IP is firewall-banned the TCP connect times out
// silently — ffmpeg's internal -reconnect keeps retrying forever, so the
// worker's own backoff loop (worker.go) never iterates; runPCMReader just
// blocks. The only thing that breaks the loop is the stall watchdog, which
// respawns the worker every ~2min and RESETS that backoff. Net effect: ~9 TCP
// connects every 2min, 24/7, per blocked station. That continuous hammering
// from a single static datacenter IP is what built abuse reputation across
// multiple Brazilian streaming panels (livespanel, streamingdevideo) and got
// 34.39.163.110 dropped in jun/2026.
//
// The fix lives here, at the respawn decision: count consecutive
// never-connected restarts per station and make the watchdog wait an
// exponentially growing, capped delay before respawning — during which NO
// ffmpeg runs, so the hammering stops. A successful connect (OnStreamUp)
// resets the counter, so a station that recovers (or gets allow-listed) is
// back to normal cadence immediately.

const (
	// connectBackoffBase is the delay after the first never-connected restart.
	connectBackoffBase = 1 * time.Minute
	// connectBackoffMax caps the delay so a recovered/allow-listed station is
	// retried at least every ~30min (recovery latency ≤ cap + startup grace).
	connectBackoffMax = 30 * time.Minute
)

// backoffFor returns the delay to wait before respawning a worker that has
// failed to connect `failures` consecutive times. Exponential from
// connectBackoffBase, doubling each failure, capped at connectBackoffMax.
// failures==0 means "no failure recorded yet" and returns 0 (restart now),
// preserving today's immediate-restart behaviour for the first stall.
func backoffFor(failures uint32) time.Duration {
	if failures == 0 {
		return 0
	}
	d := connectBackoffBase
	for i := uint32(1); i < failures; i++ {
		d *= 2
		if d >= connectBackoffMax {
			return connectBackoffMax
		}
	}
	return d
}

// jitterDelay adds ±20% random spread to d so a fleet of stations co-blocked
// by the same panel doesn't retry in lockstep (and looks less bot-like).
// Non-positive durations pass through unchanged.
func jitterDelay(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	factor := 0.8 + rand.Float64()*0.4 // [0.8, 1.2)
	return time.Duration(float64(d) * factor)
}

// BackoffStations returns the stations currently in connect-backoff — those
// with a parked placeholder (a workerEntry whose worker is nil, left by the
// stall watchdog while it waits out the exponential delay) — mapped to their
// consecutive failure count.
//
// The system-health handler uses this to label a backed-off station "stream
// inalcançável / backing off (warning)" instead of "worker drift (critical)":
// a backed-off station has NO registered worker by design (we killed it to stop
// hammering a blocked IP), not because the reconciler failed to start one.
func (s *Supervisor) BackoffStations() map[uuid.UUID]uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[uuid.UUID]uint32)
	for id, e := range s.workers {
		if e != nil && e.worker == nil {
			out[id] = s.connectFailures[id]
		}
	}
	return out
}
