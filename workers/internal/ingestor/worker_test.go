package ingestor

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestNewMatchThreshold verifies the helper builds an *atomic.Int32 with the
// requested value pre-loaded.
func TestNewMatchThreshold(t *testing.T) {
	a := NewMatchThreshold(7)
	if a == nil {
		t.Fatal("NewMatchThreshold returned nil")
	}
	if got := a.Load(); got != 7 {
		t.Fatalf("Load = %d, want 7", got)
	}
}

// TestNewWorker_DefaultThreshold verifies that a nil MatchThreshold in the
// config is replaced with NewMatchThreshold(5) — protects callers that haven't
// migrated to the dynamic plumbing (mirrors the supervisor's contract).
func TestNewWorker_DefaultThreshold(t *testing.T) {
	w := NewWorker(WorkerConfig{}, nil, nil, nil)
	if got := w.Threshold(); got != 5 {
		t.Fatalf("default threshold = %d, want 5", got)
	}

	a := NewMatchThreshold(11)
	w2 := NewWorker(WorkerConfig{MatchThreshold: a}, nil, nil, nil)
	if got := w2.Threshold(); got != 11 {
		t.Fatalf("supplied threshold = %d, want 11", got)
	}
}

// TestSetThreshold_Atomic exercises the swap path used by the supervisor's
// runThresholdRefresher: calling SetThreshold returns the previous value and
// concurrent reads/writes never panic.
func TestSetThreshold_Atomic(t *testing.T) {
	a := NewMatchThreshold(5)
	w := NewWorker(WorkerConfig{MatchThreshold: a}, nil, nil, nil)

	prev := w.SetThreshold(9)
	if prev != 5 {
		t.Fatalf("first SetThreshold returned %d, want 5", prev)
	}
	if cur := w.Threshold(); cur != 9 {
		t.Fatalf("Threshold after Set = %d, want 9", cur)
	}

	prev = w.SetThreshold(9) // same value -> still returns prev
	if prev != 9 {
		t.Fatalf("idempotent SetThreshold returned %d, want 9", prev)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			w.SetThreshold(10 + i%5)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = w.Threshold()
		}
	}()
	wg.Wait()
	w.SetThreshold(13)
	if got := w.Threshold(); got != 13 {
		t.Fatalf("post-concurrency Threshold = %d, want 13", got)
	}
}

// TestSetThreshold_NilSlot verifies the documented behaviour when WorkerConfig
// was built without an atomic: the worker installs one on first SetThreshold
// and reports prev=0.
func TestSetThreshold_NilSlot(t *testing.T) {
	w := &Worker{} // bypass NewWorker so MatchThreshold stays nil
	if got := w.Threshold(); got != 0 {
		t.Fatalf("Threshold with nil slot = %d, want 0", got)
	}
	prev := w.SetThreshold(7)
	if prev != 0 {
		t.Fatalf("SetThreshold prev = %d, want 0 (nil slot path)", prev)
	}
	if got := w.Threshold(); got != 7 {
		t.Fatalf("Threshold after Set = %d, want 7", got)
	}
}

// TestLastPCMAt_DefaultZero verifies that LastPCMAt returns the zero time when
// the worker has not received any audio.
func TestLastPCMAt_DefaultZero(t *testing.T) {
	w := NewWorker(WorkerConfig{}, nil, nil, nil)
	if !w.LastPCMAt().IsZero() {
		t.Fatalf("LastPCMAt should be zero at construction, got %v", w.LastPCMAt())
	}
}

// TestUpdateLastPCMAt_RoundTrips verifies UpdateLastPCMAt + LastPCMAt return
// the expected timestamp.
func TestUpdateLastPCMAt_RoundTrips(t *testing.T) {
	w := NewWorker(WorkerConfig{}, nil, nil, nil)
	now := time.Now()
	w.UpdateLastPCMAt(now)
	if got := w.LastPCMAt(); !got.Equal(now) {
		t.Fatalf("LastPCMAt = %v, want %v", got, now)
	}
	later := now.Add(5 * time.Second)
	w.UpdateLastPCMAt(later)
	if got := w.LastPCMAt(); !got.Equal(later) {
		t.Fatalf("LastPCMAt after second update = %v, want %v", got, later)
	}
}

// TestLastPCMAt_Concurrent ensures concurrent reads/writes to lastPCMAt never
// panic (the supervisor's stall watchdog reads while the PCM goroutine writes).
func TestLastPCMAt_Concurrent(t *testing.T) {
	w := NewWorker(WorkerConfig{}, nil, nil, nil)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			w.UpdateLastPCMAt(time.Now())
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = w.LastPCMAt()
		}
	}()
	wg.Wait()
	if w.LastPCMAt().IsZero() {
		t.Fatal("LastPCMAt should be non-zero after concurrent updates")
	}
}

// TestStreamURL_Accessor verifies that Worker.StreamURL() returns whatever was
// in the WorkerConfig at construction time. The supervisor's reconciler reads
// this to detect when stations.stream_url has drifted from the URL the worker
// is currently feeding into ffmpeg — without it, the worker would loop on a
// stale URL forever after an operator edited the station.
func TestStreamURL_Accessor(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"populated", "https://example.com/stream"},
		{"empty", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := NewWorker(WorkerConfig{StreamURL: c.url}, nil, nil, nil)
			if got := w.StreamURL(); got != c.url {
				t.Fatalf("StreamURL() = %q, want %q", got, c.url)
			}
		})
	}
}

// TestRun_RespectsCancelledContext verifies that the outer Run loop returns
// promptly when the context is cancelled BEFORE entering the ffmpeg startup
// path. This is the only branch we can exercise without ffmpeg installed.
func TestRun_RespectsCancelledContext(t *testing.T) {
	w := NewWorker(WorkerConfig{
		StationID: uuid.New(),
		StreamURL: "http://invalid.example.invalid/stream",
	}, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE Run starts the ffmpeg path

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after pre-cancelled ctx")
	}
}

// TestSleep_ReturnsOnContextCancel verifies the helper exits early when ctx
// is cancelled.
func TestSleep_ReturnsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	sleep(ctx, 5*time.Second)
	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Fatalf("sleep(ctx, 5s) with cancel did not return early (took %s)", elapsed)
	}
}

// TestSleep_BlocksFullDuration verifies that without cancellation sleep
// blocks for at least the requested duration.
func TestSleep_BlocksFullDuration(t *testing.T) {
	ctx := context.Background()
	d := 25 * time.Millisecond
	start := time.Now()
	sleep(ctx, d)
	if elapsed := time.Since(start); elapsed < d {
		t.Fatalf("sleep returned in %s, want at least %s", elapsed, d)
	}
}

// TestJitter_BoundedByPlusMinus20Percent samples jitter many times and ensures
// every sample lies in [0.8d, 1.2d).
func TestJitter_BoundedByPlusMinus20Percent(t *testing.T) {
	d := 1000 * time.Millisecond
	lo := time.Duration(float64(d) * 0.8)
	hi := time.Duration(float64(d) * 1.2)
	for i := 0; i < 200; i++ {
		got := jitter(d)
		if got < lo || got >= hi {
			t.Fatalf("jitter(%v) = %v, want in [%v, %v)", d, got, lo, hi)
		}
	}
}

// TestMin_Helper covers the durable min(a,b) helper used in the reconnect
// backoff path (`backoff = min(backoff*2, maxBackoff)`).
func TestMin_Helper(t *testing.T) {
	if got := min(2*time.Second, 5*time.Second); got != 2*time.Second {
		t.Fatalf("min(2s,5s) = %v, want 2s", got)
	}
	if got := min(60*time.Second, 30*time.Second); got != 30*time.Second {
		t.Fatalf("min(60s,30s) = %v, want 30s", got)
	}
	if got := min(time.Second, time.Second); got != time.Second {
		t.Fatalf("min(=) = %v, want 1s", got)
	}
}

// TestDetectionEvent_JSONRoundTrip protects the on-the-wire shape of the
// payload published to detections.pending. Downstream consumers (supervisor
// disambiguation, evidence service, webhook deliverer) all unmarshal this
// exact struct — any rename here is an API break.
func TestDetectionEvent_JSONRoundTrip(t *testing.T) {
	src := DetectionEvent{
		StationID:           uuid.New().String(),
		CommercialShortID:   42,
		DetectedAt:          "2026-05-07T10:00:00Z",
		OffsetFrames:        12,
		Confidence:          0.92,
		EvidenceWindowStart: "2026-05-07T09:59:00Z",
		EvidenceWindowEnd:   "2026-05-07T10:01:00Z",
		HashCount:           37,
		TemporalCoverage:    0.92,
		MatchStartOffsetMs:  256,
		MatchEndOffsetMs:    30464,
		VariantUsed:         0,
		RateUsed:            0,
	}
	b, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Field-name pinning — receivers in supervisor/evidence/webhook depend on these.
	for _, field := range []string{
		`"station_id"`,
		`"commercial_short_id"`,
		`"detected_at"`,
		`"offset_frames"`,
		`"confidence"`,
		`"evidence_window_start"`,
		`"evidence_window_end"`,
		`"hash_count"`,
		`"temporal_coverage"`,
		`"match_start_offset_ms"`,
		`"match_end_offset_ms"`,
		`"variant_used"`,
		`"rate_used"`,
	} {
		if !contains(b, []byte(field)) {
			t.Fatalf("expected JSON to contain %s; got %s", field, string(b))
		}
	}
	var dst DetectionEvent
	if err := json.Unmarshal(b, &dst); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if dst != src {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", dst, src)
	}
}

func contains(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// TestWorkerConfig_AtomicReuse verifies the supervisor's invariant: the same
// *atomic.Int32 passed to MatchThreshold is observed by w.Threshold() — i.e.
// updates from the refresher goroutine are visible without restarting the
// worker. (`runThresholdRefresher` exposes this contract.)
func TestWorkerConfig_AtomicReuse(t *testing.T) {
	a := NewMatchThreshold(3)
	w := NewWorker(WorkerConfig{MatchThreshold: a}, nil, nil, nil)

	// External actor swaps the underlying atomic — emulates what the
	// supervisor's runThresholdRefresher does on a station_thresholds change.
	a.Store(20)
	if got := w.Threshold(); got != 20 {
		t.Fatalf("Threshold did not pick up external atomic update: got %d, want 20", got)
	}

	// Worker's Set still goes through the same atomic.
	w.SetThreshold(7)
	if got := a.Load(); got != 7 {
		t.Fatalf("external atomic should observe Set: got %d, want 7", got)
	}
}
