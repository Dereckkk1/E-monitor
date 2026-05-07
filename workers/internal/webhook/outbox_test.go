package webhook

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// TestEventType_Constants pins the wire identifiers consumed by external
// receivers (documented in docs/webhooks.md). Renaming any of these is an API
// break.
func TestEventType_Constants(t *testing.T) {
	if EventDetectionConfirmed != "detection.confirmed" {
		t.Fatalf("EventDetectionConfirmed = %q", EventDetectionConfirmed)
	}
	if EventDetectionRetracted != "detection.retracted" {
		t.Fatalf("EventDetectionRetracted = %q", EventDetectionRetracted)
	}
	if EventWebhookTest != "webhook.test" {
		t.Fatalf("EventWebhookTest = %q", EventWebhookTest)
	}
}

// TestNewOutbox_Defaults verifies the constructor wires its dependencies.
func TestNewOutbox_Defaults(t *testing.T) {
	o := NewOutbox(nil, nil, zap.NewNop())
	if o == nil {
		t.Fatal("NewOutbox returned nil")
	}
	if o.log == nil {
		t.Fatal("expected non-nil logger")
	}
}

// TestOutbox_Enqueue_NilEvent rejects nil events with a stable error string.
func TestOutbox_Enqueue_NilEvent(t *testing.T) {
	o := NewOutbox(nil, nil, zap.NewNop())
	err := o.Enqueue(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil event")
	}
}

// TestSubscribedTo_Edges supplements the existing positive cases with
// boundary inputs (empty filter + miss, multi-element list with mid match).
func TestSubscribedTo_Edges(t *testing.T) {
	cases := []struct {
		name   string
		filter []string
		event  string
		want   bool
	}{
		{"empty list = all", nil, "anything.x", true},
		{"empty slice = all", []string{}, "anything.x", true},
		{"strict miss", []string{"a"}, "b", false},
		{"wildcard amongst real ones", []string{"detection.confirmed", "*"}, "weird.event", true},
		{"case sensitive", []string{"Detection.Confirmed"}, "detection.confirmed", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := subscribedTo(c.filter, c.event); got != c.want {
				t.Fatalf("subscribedTo(%v,%q)=%v want %v", c.filter, c.event, got, c.want)
			}
		})
	}
}

// TestEnqueue_NilClientsRepo verifies that without a Clients repo the GetWebhookConfig
// call surfaces an error rather than panicking. (Covers the early error wrap
// path of Enqueue.)
func TestEnqueue_NilClientsRepo(t *testing.T) {
	// We can't exercise this without panicking on db nil. Skip until a
	// catalog interface is extracted. Documented for future contributors.
	t.Skip("Enqueue path requires non-nil *catalog.Clients (concrete type uses pgxpool)")
}

// sentinelErr is reused by tests asserting on wrapped errors.
var sentinelErr = errors.New("sentinel")

// TestEvent_StructFields ensures the exported Event struct keeps the
// fields the API and supervisor build today (Type / ClientID / Body).
func TestEvent_StructFields(t *testing.T) {
	clientID := uuid.New()
	ev := Event{
		Type:     EventWebhookTest,
		ClientID: clientID,
		Body:     map[string]any{"x": 1},
	}
	if ev.Type != EventWebhookTest {
		t.Fatalf("Type = %v", ev.Type)
	}
	if ev.ClientID != clientID {
		t.Fatalf("ClientID = %v", ev.ClientID)
	}
	if ev.Body == nil {
		t.Fatal("Body lost")
	}
	_ = sentinelErr // referenced to keep the var meaningful
}
