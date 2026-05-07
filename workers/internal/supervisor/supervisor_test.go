package supervisor

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/ingestor"
)

// TestRefreshThreshold_NoWorker verifies that calling RefreshThreshold for a
// station with no running worker returns a sentinel error rather than
// panicking on a nil entry.
func TestRefreshThreshold_NoWorker(t *testing.T) {
	s := &Supervisor{
		workers: make(map[uuid.UUID]*workerEntry),
	}
	err := s.RefreshThreshold(uuid.New())
	assert.Error(t, err, "should refuse refresh when no worker is registered")
}

// TestRefreshThreshold_SignalsWorker verifies that RefreshThreshold writes to
// the entry's refreshNow channel when a worker is registered, and that the
// signal coalesces (cap-1 buffer) when called twice in a row.
func TestRefreshThreshold_SignalsWorker(t *testing.T) {
	id := uuid.New()
	entry := &workerEntry{
		refreshNow: make(chan struct{}, 1),
	}
	s := &Supervisor{
		workers: map[uuid.UUID]*workerEntry{id: entry},
	}

	require.NoError(t, s.RefreshThreshold(id))
	// Second call must not block (coalesces into the still-pending one).
	require.NoError(t, s.RefreshThreshold(id))

	select {
	case <-entry.refreshNow:
	default:
		t.Fatal("refresh signal was never written to refreshNow")
	}
	select {
	case <-entry.refreshNow:
		t.Fatal("second refresh call should have coalesced; got two signals")
	default:
	}
}

// TestWorker_SetThreshold_Atomic exercises the path the supervisor's refresher
// uses: build a Worker around an *atomic.Int32, swap the value, and confirm
// concurrent reads via Threshold() observe the new value. Mirrors the
// supervisor-driven hot-reload from station_thresholds.
func TestWorker_SetThreshold_Atomic(t *testing.T) {
	a := ingestor.NewMatchThreshold(5)
	cfg := ingestor.WorkerConfig{MatchThreshold: a}
	w := ingestor.NewWorker(cfg, nil, nil, nil)

	require.EqualValues(t, 5, w.Threshold())

	// Hammer SetThreshold from one goroutine while another goroutine reads,
	// to give -race a chance to fail loudly if we ever drop the atomic.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			w.SetThreshold(7 + i%3)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = w.Threshold()
		}
	}()
	wg.Wait()

	// Final pin so the assertion is deterministic.
	prev := w.SetThreshold(11)
	assert.Greater(t, int(prev), 0)
	assert.EqualValues(t, 11, w.Threshold())
}

// TestNewWorker_DefaultsThreshold verifies that constructing a Worker without
// a MatchThreshold falls back to the documented default (5) — protecting any
// test or call-site that hasn't yet been migrated to the atomic plumbing.
func TestNewWorker_DefaultsThreshold(t *testing.T) {
	w := ingestor.NewWorker(ingestor.WorkerConfig{}, nil, nil, nil)
	assert.EqualValues(t, 5, w.Threshold(), "nil MatchThreshold should default to 5")
}
