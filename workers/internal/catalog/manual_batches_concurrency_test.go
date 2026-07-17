package catalog

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// TestDetections_CreateManualBatch_ConcurrentDoesNotDeadlock is a permanent
// regression test for the P0 deadlock fixed in commit ce0cc14
// ("fix(manual-batch): categorize roda na tx, nao no pool").
//
// CreateManualBatch opens a transaction (tx.Begin) and, per entry, calls
// categorize. If categorize is called with the shared pool (d.pool) instead
// of the open tx, each request needs TWO simultaneous connections: the one
// held by the tx, plus a second one for categorize's own queries. With the
// pool at its ceiling, N concurrent batches each parked mid-tx exhaust every
// connection while still waiting on a free one for categorize — a circular
// wait that only resolves when the request context times out. Nothing in the
// compiler prevents this from coming back: categorize's signature accepts
// either a *pgxpool.Pool or a pgx.Tx (see pgxQuerier in detections.go), so a
// future call site that passes d.pool from inside a transaction would
// silently reintroduce the deadlock. This test is the only thing that catches
// that regression.
//
// Proof from code review (2026-07-17), reproduced with a disposable Postgres
// and a dedicated MaxConns=2 pool + 3 concurrent CreateManualBatch calls:
//   - BASE (categorize using d.pool inside the tx): deadlock, all 3 calls
//     timed out at 12s (context deadline exceeded).
//   - HEAD (categorize using the tx, this fix): all 3 calls completed in
//     ~179ms.
//
// This test mirrors that setup: MaxConns=2 (not the shared/default pool —
// the whole point is to force contention down to the minimum that exhibits
// the bug) and 3 concurrent CreateManualBatch calls against the same
// campaign/material/station, bounded by a generous 10s timeout (correct code
// finishes in ~180ms, so 10s leaves enormous headroom without flaking).
func TestDetections_CreateManualBatch_ConcurrentDoesNotDeadlock(t *testing.T) {
	// seedAirtimeFixture calls newTestDB internally (testhelpers_test.go),
	// which skips cleanly via t.Skip when TEST_DATABASE_URL is unset — the
	// same mechanism every other integration test in this package relies on.
	ctx, seedPool, campID, matID, statID := seedAirtimeFixture(t, "ConcurrentBatch")
	userID := insertSuggestionTestUser(t, ctx, seedPool)
	url := os.Getenv("TEST_DATABASE_URL") // guaranteed non-empty at this point

	// Dedicated pool with MaxConns=2 — deliberately NOT the shared helper
	// pool. The bug only reproduces once the pool is small enough that two
	// simultaneous transactions can exhaust it.
	cfg, err := pgxpool.ParseConfig(url)
	require.NoError(t, err)
	cfg.MaxConns = 2
	cfg.MinConns = 2
	dedicated, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(dedicated.Close)

	dets := NewDetections(dedicated)

	const concurrency = 3
	errs := make([]error, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := dets.CreateManualBatch(ctx, CreateManualBatchInput{
				CampaignID: campID,
				StationID:  statID,
				ManualBy:   userID,
				Entries: []ManualBatchEntry{
					{CommercialID: matID, DetectedAt: time.Now().Add(time.Duration(i) * time.Minute)},
				},
			})
			errs[i] = err
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Fell through — all goroutines returned within the timeout.
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout: %d concurrent CreateManualBatch calls did not complete within 10s — "+
			"this is the deadlock regression fixed in ce0cc14 (categorize must use the caller's tx, "+
			"not d.pool, when called from inside a transaction)", concurrency)
	}

	// No extra cleanup needed: seedAirtimeFixture already registers a
	// t.Cleanup that deletes detections by campaign_id.

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: CreateManualBatch: %v", i, err)
		}
	}
}
