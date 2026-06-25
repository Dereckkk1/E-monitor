package supervisor

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/ingestor"
)

// TestTotalFrames covers the helper that the supervisor uses to size the
// per-commercial state-machine frame budget. The constants are sampleRate=16000
// and hopSize=2048, so a 30s commercial yields floor(30 * 16000 / 2048) = 234.
func TestTotalFrames(t *testing.T) {
	cases := []struct {
		dur  float64
		want int
	}{
		{0, 0},
		{1, 7},      // floor(1 * 16000 / 2048) = 7
		{30, 234},   // floor(30 * 16000 / 2048) = 234
		{60, 468},   // floor(60 * 16000 / 2048) = 468
		{15.5, 121}, // floor(15.5 * 16000 / 2048) = 121
	}
	for _, c := range cases {
		if got := totalFrames(c.dur); got != c.want {
			t.Errorf("totalFrames(%v) = %d, want %d", c.dur, got, c.want)
		}
	}
}

// TestComputePreventiveRestartDelay verifies the helper returns a positive
// duration that always lands within ~26 hours from now (next 3-5 AM window
// + 24h tail). The function is randomised so we sample a number of times.
func TestComputePreventiveRestartDelay(t *testing.T) {
	for i := 0; i < 50; i++ {
		d := computePreventiveRestartDelay()
		if d <= 0 {
			t.Fatalf("preventive restart delay must be positive, got %v", d)
		}
		// Maximum: tomorrow at 5 AM, from any current time → ~29h worst case
		// (current time 5:00:01 → next 3:00–5:00 window is ~22h-24h away,
		//  plus the 0–2h jitter). Use 30h as a comfortable upper bound.
		if d > 30*time.Hour {
			t.Fatalf("preventive restart delay too large: %v", d)
		}
	}
}

// TestWorkerStatuses_Snapshot verifies the read-only accessor returns a
// best-effort snapshot of the workers map. We construct a Supervisor by hand
// and seed entries directly to avoid depending on DB / NATS.
func TestWorkerStatuses_Snapshot(t *testing.T) {
	s := &Supervisor{
		workers:            make(map[uuid.UUID]*workerEntry),
		lastStallRestart:   make(map[uuid.UUID]time.Time),
		stallRestartCounts: make(map[uuid.UUID]uint32),
	}

	if got := s.WorkerStatuses(); len(got) != 0 {
		t.Fatalf("empty supervisor: WorkerStatuses len=%d, want 0", len(got))
	}

	stationActive := uuid.New()
	stationStallRisk := uuid.New()
	wActive := ingestor.NewWorker(ingestor.WorkerConfig{StationID: stationActive}, nil, nil, zap.NewNop())
	wActive.UpdateLastPCMAt(time.Now())
	wStall := ingestor.NewWorker(ingestor.WorkerConfig{StationID: stationStallRisk}, nil, nil, zap.NewNop())
	wStall.UpdateLastPCMAt(time.Now().Add(-2 * time.Minute)) // > 30s → StallRisk

	s.workers[stationActive] = &workerEntry{worker: wActive}
	s.workers[stationStallRisk] = &workerEntry{worker: wStall}
	// Entry without a worker (nil) — must be skipped, not panic.
	s.workers[uuid.New()] = &workerEntry{worker: nil}

	got := s.WorkerStatuses()
	if len(got) != 2 {
		t.Fatalf("WorkerStatuses len=%d, want 2 (nil-worker entry must be skipped)", len(got))
	}
	for _, st := range got {
		if !st.Active {
			t.Errorf("status %s: Active=false", st.StationID)
		}
		if st.StationID == stationActive.String() && st.StallRisk {
			t.Errorf("active worker should not be flagged StallRisk")
		}
		if st.StationID == stationStallRisk.String() && !st.StallRisk {
			t.Errorf("worker with old PCM should be flagged StallRisk")
		}
	}
}

// TestBackoffStations_OnlyPlaceholders verifies the accessor used by the
// system-health handler reports ONLY the parked circuit-breaker placeholders
// (worker==nil entries the stall watchdog left while waiting out the backoff),
// mapped to their consecutive failure count — never a real running worker. This
// is what lets the admin "Atenção agora" panel label a backed-off station
// "stream inalcançável (warning)" instead of "drift do reconciler (critical)".
func TestBackoffStations_OnlyPlaceholders(t *testing.T) {
	s := &Supervisor{
		workers:         make(map[uuid.UUID]*workerEntry),
		connectFailures: make(map[uuid.UUID]uint32),
	}

	if got := s.BackoffStations(); len(got) != 0 {
		t.Fatalf("empty supervisor: BackoffStations len=%d, want 0", len(got))
	}

	running := uuid.New()
	backoff := uuid.New()
	wRunning := ingestor.NewWorker(ingestor.WorkerConfig{StationID: running}, nil, nil, zap.NewNop())

	// Real running worker — must NOT appear.
	s.workers[running] = &workerEntry{worker: wRunning}
	// Parked backoff placeholder (worker==nil) with a failure count — must appear.
	s.workers[backoff] = &workerEntry{worker: nil}
	s.connectFailures[backoff] = 3

	got := s.BackoffStations()
	if len(got) != 1 {
		t.Fatalf("BackoffStations len=%d, want 1 (only the nil-worker placeholder)", len(got))
	}
	if fails, ok := got[backoff]; !ok || fails != 3 {
		t.Fatalf("BackoffStations[backoff] = (%d, %v), want (3, true)", fails, ok)
	}
	if _, ok := got[running]; ok {
		t.Fatal("BackoffStations must not include a running worker")
	}
}

// TestHandlePendingDetection_InvalidJSON verifies the unmarshal guard.
func TestHandlePendingDetection_InvalidJSON(t *testing.T) {
	s := &Supervisor{log: zap.NewNop()}
	err := s.handlePendingDetection(nil, []byte("not json"))
	if err == nil {
		t.Fatal("expected unmarshal error for invalid JSON")
	}
}

// TestHandlePendingDetection_InvalidDate verifies the time.Parse guard.
func TestHandlePendingDetection_InvalidDate(t *testing.T) {
	s := &Supervisor{log: zap.NewNop()}
	ev := ingestor.DetectionEvent{
		StationID:         uuid.New().String(),
		CommercialShortID: 7,
		DetectedAt:        "not-a-date",
	}
	raw, _ := json.Marshal(ev)
	err := s.handlePendingDetection(nil, raw)
	if err == nil {
		t.Fatal("expected parse_detected_at error")
	}
}

// TestRetractedEvent_JSONShape pins the wire format so external subscribers
// (audit log, UI) keep working when this struct is touched.
func TestRetractedEvent_JSONShape(t *testing.T) {
	ev := RetractedEvent{
		StationID:         uuid.New().String(),
		CommercialShortID: 17,
		DetectedAt:        time.Now().UTC().Format(time.RFC3339),
		RetractedAt:       time.Now().UTC().Format(time.RFC3339),
		Reason:            "longer_cut_detected",
		Confidence:        0.91,
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, field := range []string{
		`"station_id"`,
		`"commercial_short_id"`,
		`"detected_at"`,
		`"retracted_at"`,
		`"reason"`,
		`"confidence"`,
	} {
		if !contains(b, []byte(field)) {
			t.Fatalf("missing %s in retracted event JSON: %s", field, string(b))
		}
	}
}

// TestDedupActions_Constants pins the enum values in case future contributors
// reorder them — the underlying SubmitDetection switch depends on stability.
func TestDedupActions_Constants(t *testing.T) {
	if DedupActionPublish != 0 {
		t.Fatalf("DedupActionPublish = %d, want 0", DedupActionPublish)
	}
	if DedupActionRetractAndPublish != 1 {
		t.Fatalf("DedupActionRetractAndPublish = %d, want 1", DedupActionRetractAndPublish)
	}
	if DedupActionSuppress != 2 {
		t.Fatalf("DedupActionSuppress = %d, want 2", DedupActionSuppress)
	}
}

// TestDedupBufferRetention_Constant pins the documented retention window.
func TestDedupBufferRetention_Constant(t *testing.T) {
	if dedupBufferRetention != 60*time.Second {
		t.Fatalf("dedupBufferRetention = %v, want 60s", dedupBufferRetention)
	}
}

// TestDefaultThresholdConstant pins the documented fallback threshold (5)
// referenced in §9.4 — calibration job's floor matches this value.
func TestDefaultThresholdConstant(t *testing.T) {
	if defaultMatchThreshold != 5 {
		t.Fatalf("defaultMatchThreshold = %d, want 5", defaultMatchThreshold)
	}
	if thresholdRefreshInterval != 5*time.Minute {
		t.Fatalf("thresholdRefreshInterval = %v, want 5m", thresholdRefreshInterval)
	}
}
