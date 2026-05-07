// Package supervisor — version disambiguation buffer (§18.2.2).
//
// DedupBuffer is the in-memory coordination ring used by SubmitDetection to
// suppress duplicate publications when two cuts of the same commercial
// (e.g. VERÃO 30s and VERÃO 60s) confirm within Δ seconds of one another on
// the same station. Persistence is intentionally absent — the buffer is a
// correlation cache, not a system of record. After a supervisor restart the
// buffer is empty; the worst case is one extra duplicate detection per
// outage, which is acceptable (R-C in the plan).
package supervisor

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"radiocheck/internal/match"
)

// DedupEntry is one previously-published confirmation kept in the buffer for
// short-window cross-checks against newer confirmations.
type DedupEntry struct {
	Detection       match.ConfirmedDetection
	ClientID        uuid.UUID
	DurationSeconds int
	InsertedAt      time.Time
}

// DedupBuffer is a small in-memory ring of recent confirmed detections,
// scoped per-process. All public methods are safe for concurrent use.
type DedupBuffer struct {
	mu      sync.Mutex
	entries []DedupEntry
	maxAge  time.Duration
}

// NewDedupBuffer constructs a buffer that retains entries up to maxAge old.
// Entries older than maxAge are removed lazily by GC().
func NewDedupBuffer(maxAge time.Duration) *DedupBuffer {
	return &DedupBuffer{
		entries: make([]DedupEntry, 0, 64),
		maxAge:  maxAge,
	}
}

// MaxAge returns the configured retention window.
func (b *DedupBuffer) MaxAge() time.Duration { return b.maxAge }

// GC removes entries inserted before cutoff. Called by SubmitDetection on
// every entry to keep the buffer bounded.
func (b *DedupBuffer) GC(cutoff time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.entries) == 0 {
		return
	}
	kept := b.entries[:0]
	for _, e := range b.entries {
		if !e.InsertedAt.Before(cutoff) {
			kept = append(kept, e)
		}
	}
	b.entries = kept
}

// Find returns a *copy* of the first entry matching (stationID, clientID)
// whose Detection.DetectedAt is within ±window of t, or nil if no entry
// qualifies. The returned pointer is decoupled from internal storage —
// callers must use Replace/Add to mutate the buffer.
func (b *DedupBuffer) Find(stationID uuid.UUID, clientID uuid.UUID, t time.Time, window time.Duration) *DedupEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.entries {
		e := &b.entries[i]
		if e.ClientID != clientID {
			continue
		}
		entryStation, err := uuid.Parse(e.Detection.StationID)
		if err != nil || entryStation != stationID {
			continue
		}
		diff := t.Sub(e.Detection.DetectedAt)
		if diff < 0 {
			diff = -diff
		}
		if diff <= window {
			out := *e
			return &out
		}
	}
	return nil
}

// Add appends a new entry to the buffer. Caller is responsible for setting
// InsertedAt; if zero, the current time is used.
func (b *DedupBuffer) Add(entry DedupEntry) {
	if entry.InsertedAt.IsZero() {
		entry.InsertedAt = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, entry)
}

// Replace finds the entry matching old's identity (station + client +
// commercial short id + DetectedAt) and replaces it with newEntry. If no
// match is found, newEntry is appended (so callers always end up with the
// new entry in the buffer). Returns true when a replacement happened.
func (b *DedupBuffer) Replace(old *DedupEntry, newEntry DedupEntry) bool {
	if newEntry.InsertedAt.IsZero() {
		newEntry.InsertedAt = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.entries {
		e := &b.entries[i]
		if e.ClientID == old.ClientID &&
			e.Detection.StationID == old.Detection.StationID &&
			e.Detection.CommercialShortID == old.Detection.CommercialShortID &&
			e.Detection.DetectedAt.Equal(old.Detection.DetectedAt) {
			b.entries[i] = newEntry
			return true
		}
	}
	b.entries = append(b.entries, newEntry)
	return false
}

// Len returns the number of entries currently in the buffer (test helper).
func (b *DedupBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}
