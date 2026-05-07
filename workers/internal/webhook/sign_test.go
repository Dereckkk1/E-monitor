package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// TestSignBodyMatchesSpec verifies the HMAC-SHA256 signature uses the secret
// as the key and the raw body as the message, returning lowercase hex —
// exactly what receivers will recompute to verify authenticity.
func TestSignBodyMatchesSpec(t *testing.T) {
	secret := "this-is-a-very-strong-secret"
	body := []byte(`{"event":"detection.confirmed","data":{"x":1}}`)

	got := signBody(secret, body)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))

	if got != want {
		t.Fatalf("signBody mismatch:\n got %s\nwant %s", got, want)
	}
	if got != strings.ToLower(got) {
		t.Fatalf("signBody must be lowercase hex, got %q", got)
	}
}

// TestSubscribedTo covers the event-filter helper used by the outbox.
func TestSubscribedTo(t *testing.T) {
	cases := []struct {
		name   string
		filter []string
		event  string
		want   bool
	}{
		{"empty subscribes to all", nil, "detection.confirmed", true},
		{"explicit match", []string{"detection.confirmed"}, "detection.confirmed", true},
		{"wildcard", []string{"*"}, "anything", true},
		{"no match", []string{"detection.confirmed"}, "webhook.test", false},
		{"multi list match", []string{"a", "b", "webhook.test"}, "webhook.test", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := subscribedTo(c.filter, c.event); got != c.want {
				t.Fatalf("subscribedTo(%v,%q)=%v want %v", c.filter, c.event, got, c.want)
			}
		})
	}
}
