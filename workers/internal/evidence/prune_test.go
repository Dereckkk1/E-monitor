package evidence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// call records one boundary invocation so tests can assert order + arguments.
type call struct {
	kind string // "delete" | "expire"
	key  string
	id   uuid.UUID
}

func cand(key string, size int64) pruneCandidate {
	return pruneCandidate{ID: uuid.New(), DetectedAt: time.Unix(0, 0).UTC(), EvidenceKey: key, SizeBytes: size}
}

// TestRunPrune_DeletesObjectThenExpiresRow is the happy path: each candidate's
// object is deleted FIRST, then its row is marked expired. Order matters —
// deleting before expiring means a crash in between leaves an orphaned
// 'available' row (re-pruned next run) rather than an 'expired' row still
// pointing at a live object that never gets reclaimed.
func TestRunPrune_DeletesObjectThenExpiresRow(t *testing.T) {
	var calls []call
	del := func(_ context.Context, key string) error {
		calls = append(calls, call{kind: "delete", key: key})
		return nil
	}
	expire := func(_ context.Context, id uuid.UUID, _ time.Time) error {
		calls = append(calls, call{kind: "expire", id: id})
		return nil
	}

	c := cand("evidences/a.m4a", 2048)
	pruned, reclaimed, errs := runPrune(context.Background(), []pruneCandidate{c}, false, del, expire, zap.NewNop())

	if pruned != 1 || reclaimed != 2048 || errs != 0 {
		t.Fatalf("pruned=%d reclaimed=%d errs=%d; want 1/2048/0", pruned, reclaimed, errs)
	}
	if len(calls) != 2 || calls[0].kind != "delete" || calls[1].kind != "expire" {
		t.Fatalf("expected delete then expire, got %+v", calls)
	}
	if calls[0].key != "evidences/a.m4a" {
		t.Fatalf("deleted wrong key: %q", calls[0].key)
	}
	if calls[1].id != c.ID {
		t.Fatalf("expired wrong id: %v want %v", calls[1].id, c.ID)
	}
}

// TestRunPrune_DeleteFailureLeavesRowIntact verifies that when the object
// delete fails, the row is NOT marked expired (so a retry re-attempts the
// whole move) and the failure is counted. Losing the DB pointer to an object
// we failed to delete would strand the bytes on disk forever — the opposite
// of what a prune is for.
func TestRunPrune_DeleteFailureLeavesRowIntact(t *testing.T) {
	var expireCalled bool
	del := func(_ context.Context, _ string) error { return errors.New("minio down") }
	expire := func(_ context.Context, _ uuid.UUID, _ time.Time) error {
		expireCalled = true
		return nil
	}

	pruned, reclaimed, errs := runPrune(context.Background(),
		[]pruneCandidate{cand("evidences/a.m4a", 4096)}, false, del, expire, zap.NewNop())

	if expireCalled {
		t.Fatal("row must NOT be expired when the object delete failed")
	}
	if pruned != 0 || reclaimed != 0 || errs != 1 {
		t.Fatalf("pruned=%d reclaimed=%d errs=%d; want 0/0/1", pruned, reclaimed, errs)
	}
}

// TestRunPrune_ExpireFailureCounted verifies a failed row-update is counted as
// an error and not tallied as pruned (the next run re-selects the row — the
// object delete is idempotent).
func TestRunPrune_ExpireFailureCounted(t *testing.T) {
	del := func(_ context.Context, _ string) error { return nil }
	expire := func(_ context.Context, _ uuid.UUID, _ time.Time) error { return errors.New("db down") }

	pruned, reclaimed, errs := runPrune(context.Background(),
		[]pruneCandidate{cand("evidences/a.m4a", 4096)}, false, del, expire, zap.NewNop())

	if pruned != 0 || reclaimed != 0 || errs != 1 {
		t.Fatalf("pruned=%d reclaimed=%d errs=%d; want 0/0/1", pruned, reclaimed, errs)
	}
}

// TestRunPrune_DryRunNoSideEffects verifies that dry-run accounts what WOULD be
// pruned (count + reclaimable bytes) without deleting anything. This is the
// safety valve for the first production rollout.
func TestRunPrune_DryRunNoSideEffects(t *testing.T) {
	var touched bool
	del := func(_ context.Context, _ string) error { touched = true; return nil }
	expire := func(_ context.Context, _ uuid.UUID, _ time.Time) error { touched = true; return nil }

	cands := []pruneCandidate{cand("a.m4a", 1000), cand("b.m4a", 2000)}
	pruned, reclaimed, errs := runPrune(context.Background(), cands, true, del, expire, zap.NewNop())

	if touched {
		t.Fatal("dry-run must not delete or expire anything")
	}
	if pruned != 2 || reclaimed != 3000 || errs != 0 {
		t.Fatalf("pruned=%d reclaimed=%d errs=%d; want 2/3000/0", pruned, reclaimed, errs)
	}
}
