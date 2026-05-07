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

// TestSignTimestampedBody verifies the Stripe-style replay-resistant scheme:
// HMAC-SHA256(secret, "<unix>.<body>") in lowercase hex. Receivers documented
// in docs/webhooks.md MUST recompute exactly this string to verify.
func TestSignTimestampedBody(t *testing.T) {
	secret := "this-is-a-very-strong-secret"
	body := []byte(`{"event":"detection.confirmed"}`)
	ts := int64(1715123456)

	got := signTimestampedBody(secret, ts, body)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("1715123456."))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))

	if got != want {
		t.Fatalf("signTimestampedBody mismatch:\n got %s\nwant %s", got, want)
	}
	if got == signBody(secret, body) {
		t.Fatal("timestamped signature must differ from body-only signature")
	}
	if got != strings.ToLower(got) {
		t.Fatalf("must be lowercase hex, got %q", got)
	}
}

// TestSignTimestampedBody_ChangesWithTimestamp confirms that mutating only
// the timestamp produces a different signature — the property a receiver
// relies on to detect replay attempts that re-stamp the request.
func TestSignTimestampedBody_ChangesWithTimestamp(t *testing.T) {
	secret := "k"
	body := []byte("x")
	a := signTimestampedBody(secret, 1, body)
	b := signTimestampedBody(secret, 2, body)
	if a == b {
		t.Fatal("signature must depend on timestamp")
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
