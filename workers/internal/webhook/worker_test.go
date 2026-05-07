package webhook

import (
	"net/http"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestNewWorker_Defaults verifies the constructor populates documented defaults
// (poll interval, batch size, HTTP timeout).
func TestNewWorker_Defaults(t *testing.T) {
	w := NewWorker(nil, zap.NewNop())
	if w == nil {
		t.Fatal("NewWorker returned nil")
	}
	if w.pollInterval != defaultPollInterval {
		t.Fatalf("pollInterval = %v, want %v", w.pollInterval, defaultPollInterval)
	}
	if w.batchSize != defaultBatchSize {
		t.Fatalf("batchSize = %d, want %d", w.batchSize, defaultBatchSize)
	}
	if w.httpClient == nil {
		t.Fatal("expected non-nil httpClient (SSRF-safe wrapper)")
	}
	// The default http.Client is built via BuildSafeHTTPClient — verify
	// its timeout is wired.
	if w.httpClient.Timeout != defaultHTTPTimeout {
		t.Fatalf("http timeout = %v, want %v", w.httpClient.Timeout, defaultHTTPTimeout)
	}
}

// TestWithHTTPClient_OverridesDefault verifies the test seam used by tests to
// inject custom *http.Client (e.g. one that ignores SSRF protection so they
// can hit httptest servers).
func TestWithHTTPClient_OverridesDefault(t *testing.T) {
	w := NewWorker(nil, zap.NewNop())
	custom := &http.Client{Timeout: 1 * time.Second}
	got := w.WithHTTPClient(custom)
	if got != w {
		t.Fatal("WithHTTPClient should return the same worker")
	}
	if w.httpClient != custom {
		t.Fatal("httpClient was not replaced")
	}
}

// TestWithPollInterval_OverridesDefault verifies the test seam used to
// shorten the polling cadence in tests.
func TestWithPollInterval_OverridesDefault(t *testing.T) {
	w := NewWorker(nil, zap.NewNop())
	got := w.WithPollInterval(100 * time.Millisecond)
	if got != w {
		t.Fatal("WithPollInterval should return the same worker")
	}
	if w.pollInterval != 100*time.Millisecond {
		t.Fatalf("pollInterval = %v, want 100ms", w.pollInterval)
	}
}

// TestConstants_Pinned protects the well-documented header names and limits
// that receivers depend on. Any rename here is a contract break with already-
// shipped client code.
func TestConstants_Pinned(t *testing.T) {
	if signatureHeaderName != "X-Radiocheck-Signature" {
		t.Fatalf("signatureHeaderName = %q", signatureHeaderName)
	}
	if timestampHeaderName != "X-Radiocheck-Timestamp" {
		t.Fatalf("timestampHeaderName = %q", timestampHeaderName)
	}
	if eventTypeHeaderName != "X-Radiocheck-Event" {
		t.Fatalf("eventTypeHeaderName = %q", eventTypeHeaderName)
	}
	if deliveryIDHeaderName != "X-Radiocheck-Delivery-Id" {
		t.Fatalf("deliveryIDHeaderName = %q", deliveryIDHeaderName)
	}
	if userAgentHeader != "Radiocheck-Webhook/1.0" {
		t.Fatalf("userAgentHeader = %q", userAgentHeader)
	}
	if maxResponseBodyBytes != 4*1024 {
		t.Fatalf("maxResponseBodyBytes = %d, want 4096", maxResponseBodyBytes)
	}
	if maxAttempts != 5 {
		t.Fatalf("maxAttempts = %d, want 5", maxAttempts)
	}
	if defaultPollInterval != 5*time.Second {
		t.Fatalf("defaultPollInterval = %v, want 5s", defaultPollInterval)
	}
	if defaultBatchSize != 10 {
		t.Fatalf("defaultBatchSize = %d, want 10", defaultBatchSize)
	}
	if defaultHTTPTimeout != 10*time.Second {
		t.Fatalf("defaultHTTPTimeout = %v, want 10s", defaultHTTPTimeout)
	}
}

// TestPendingDelivery_Fields ensures the row shape produced by claimBatch
// keeps the fields that deliver() uses. Smoke test only.
func TestPendingDelivery_Fields(t *testing.T) {
	d := pendingDelivery{
		EventType:    "detection.confirmed",
		Payload:      []byte(`{}`),
		AttemptCount: 0,
		URL:          "https://example.com/hook",
		Secret:       "shhh-this-is-secret-enough",
	}
	if d.URL == "" || d.Secret == "" {
		t.Fatal("URL/Secret should be set")
	}
}
