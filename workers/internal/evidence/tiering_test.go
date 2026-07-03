package evidence

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/storage"
)

// TestTieringJobDefaults verifies that NewTieringJob populates sensible
// defaults so callers can rely on them without re-checking each field.
func TestTieringJobDefaults(t *testing.T) {
	j := NewTieringJob(nil, nil, nil, nil, zap.NewNop())
	if j.BatchSize != 100 {
		t.Errorf("BatchSize = %d, want 100", j.BatchSize)
	}
	if j.HotMaxAge != 30*24*time.Hour {
		t.Errorf("HotMaxAge = %v, want 30d", j.HotMaxAge)
	}
	if j.ColdMaxAge != 365*24*time.Hour {
		t.Errorf("ColdMaxAge = %v, want 365d", j.ColdMaxAge)
	}
	if j.ArchiveStorageClass != "STANDARD_IA" {
		t.Errorf("ArchiveStorageClass = %q, want STANDARD_IA", j.ArchiveStorageClass)
	}
	if j.Now == nil {
		t.Error("Now is nil; want non-nil function")
	}
}

// TestMoveTierSkipsOnNilBuckets verifies that the job exits cleanly (no
// errors counted) when one of the buckets is nil — i.e. when the operator
// hasn't configured a cold/archive bucket. This is the common dev-env case.
func TestMoveTierSkipsOnNilBuckets(t *testing.T) {
	j := NewTieringJob(nil, nil, nil, nil, zap.NewNop())
	moved, errs := j.moveTier(context.Background(), "hot", "cold", nil, nil, time.Hour, "")
	if moved != 0 {
		t.Errorf("moved = %d, want 0", moved)
	}
	if errs != 0 {
		t.Errorf("errs = %d, want 0", errs)
	}
}

// TestMoveTierSkipsWhenSameBucket verifies that when hot and cold resolve to the
// SAME bucket (no separate cold tier — the prod reality behind the local-retention
// prune), moveTier skips entirely instead of attempting a Get→Put copy that fails
// with "request stream is not seekable" and spams errors on every 30d+ row
// (incidente 2026-07-02). j.DB is nil, so if the skip guard is missing moveTier
// proceeds to j.DB.Query and panics — the test would fail loudly.
func TestMoveTierSkipsWhenSameBucket(t *testing.T) {
	c, err := storage.New(context.Background(), "http://minio:9000", "", "evidence", "us-east-1", "k", "s")
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	j := NewTieringJob(nil, c, c, c, zap.NewNop())
	moved, errs := j.moveTier(context.Background(), "hot", "cold", c, c, time.Hour, "")
	if moved != 0 || errs != 0 {
		t.Fatalf("moved=%d errs=%d; want 0/0 (skipped same-bucket move)", moved, errs)
	}
}

// TestScheduleStopsOnContextCancel verifies that Schedule respects ctx
// cancellation rather than spinning forever — important so the API server
// can shut down cleanly.
func TestScheduleStopsOnContextCancel(t *testing.T) {
	j := NewTieringJob(nil, nil, nil, nil, zap.NewNop())
	// Override Now to never be 03:00 so the loop only watches ctx.
	j.Now = func() time.Time { return time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC) }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		j.Schedule(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatal("Schedule did not return after ctx cancel within 5s")
	}
}
