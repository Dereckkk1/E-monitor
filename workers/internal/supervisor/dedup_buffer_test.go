package supervisor

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"radiocheck/internal/match"
)

func mkEntry(stationID uuid.UUID, clientID uuid.UUID, shortID int32, dur int, broadcastStart time.Time, insertedAt time.Time) DedupEntry {
	return DedupEntry{
		Detection: match.ConfirmedDetection{
			CommercialShortID: shortID,
			StationID:         stationID.String(),
			DetectedAt:        broadcastStart.Add(time.Duration(dur) * time.Second / 10), // ~confirmation time
			FirstMatchAt:      broadcastStart.Add(-2 * time.Second),
			OffsetFrames:      0,
			Confidence:        0.9,
		},
		ClientID:        clientID,
		DurationSeconds: dur,
		BroadcastStart:  broadcastStart,
		InsertedAt:      insertedAt,
	}
}

func TestDedupBuffer_GCEvictsOldEntries(t *testing.T) {
	buf := NewDedupBuffer(60 * time.Second)
	station := uuid.New()
	client := uuid.New()
	now := time.Now()

	buf.Add(mkEntry(station, client, 1, 30, now.Add(-90*time.Second), now.Add(-90*time.Second)))
	buf.Add(mkEntry(station, client, 2, 60, now.Add(-10*time.Second), now.Add(-10*time.Second)))

	if got := buf.Len(); got != 2 {
		t.Fatalf("expected 2 entries before GC, got %d", got)
	}

	buf.GC(now.Add(-60 * time.Second))

	if got := buf.Len(); got != 1 {
		t.Fatalf("expected 1 entry after GC, got %d", got)
	}
}

func TestDedupBuffer_FindWithinAndOutsideWindow(t *testing.T) {
	buf := NewDedupBuffer(60 * time.Second)
	station := uuid.New()
	otherStation := uuid.New()
	client := uuid.New()
	otherClient := uuid.New()
	now := time.Now()

	// Entry: 60s commercial starting at now → broadcast window [now, now+60s].
	buf.Add(mkEntry(station, client, 1, 60, now, now))

	// Overlapping candidate: starts 3s in with 30s duration → [now+3s, now+33s] → hit.
	if got := buf.Find(station, client, now.Add(3*time.Second), 30); got == nil {
		t.Fatalf("expected hit for overlapping candidate")
	}

	// Non-overlapping candidate: starts after entry ends → [now+65s, now+95s] → miss.
	if got := buf.Find(station, client, now.Add(65*time.Second), 30); got != nil {
		t.Fatalf("expected miss for non-overlapping candidate, got %+v", got)
	}

	// Different client → miss.
	if got := buf.Find(station, otherClient, now, 30); got != nil {
		t.Fatalf("expected miss across clients, got %+v", got)
	}

	// Different station → miss.
	if got := buf.Find(otherStation, client, now, 30); got != nil {
		t.Fatalf("expected miss across stations, got %+v", got)
	}
}

// TestDedupBuffer_MisalignedCutOverlap reproduces the Itapoá/Rôgga incident:
// the 30s cut is taken from ~20s into the 60s broadcast (not the first 30s).
// With the old DetectedAt-proximity approach these two confirmations — 20s apart
// — would be outside the 5s window and no dedup would fire. Interval overlap
// catches them correctly.
func TestDedupBuffer_MisalignedCutOverlap(t *testing.T) {
	buf := NewDedupBuffer(60 * time.Second)
	station := uuid.New()
	client := uuid.New()
	broadcastStart := time.Now()

	// 60s commercial starts at broadcastStart → window [T, T+60s].
	buf.Add(mkEntry(station, client, 1, 60, broadcastStart, broadcastStart))

	// 30s cut, starting 20s into the 60s broadcast → window [T+20s, T+50s].
	// This is 20s after the 60s DetectedAt — outside the old 5s Δ window.
	// Interval overlap: (T+20s) < (T+60s) && T < (T+20s+30s) → true.
	conflict := buf.Find(station, client, broadcastStart.Add(20*time.Second), 30)
	if conflict == nil {
		t.Fatalf("expected overlap hit for misaligned 30s cut; dedup would have been missed with old DetectedAt-proximity logic")
	}

	// Dedup action: existing entry is 60s, candidate is 30s → suppress.
	if got := evaluateDedup(30, 2, conflict); got != DedupActionSuppress {
		t.Fatalf("expected suppress (shorter cut loses), got %v", got)
	}
}

func TestDedupBuffer_AddAndReplace(t *testing.T) {
	buf := NewDedupBuffer(60 * time.Second)
	station := uuid.New()
	client := uuid.New()
	now := time.Now()

	original := mkEntry(station, client, 30, 30, now, now)
	buf.Add(original)
	if got := buf.Len(); got != 1 {
		t.Fatalf("expected 1 entry after Add, got %d", got)
	}

	replacement := mkEntry(station, client, 60, 60, now.Add(2*time.Second), now.Add(2*time.Second))
	if !buf.Replace(&original, replacement) {
		t.Fatalf("Replace should have hit existing entry")
	}
	if got := buf.Len(); got != 1 {
		t.Fatalf("expected 1 entry after Replace, got %d", got)
	}

	// Confirm the replaced entry is the only thing findable in the window.
	// Entry: broadcastStart=now+2s, dur=60 → [now+2s, now+62s]. Candidate [now+2s, now+32s] overlaps.
	hit := buf.Find(station, client, now.Add(2*time.Second), 30)
	if hit == nil {
		t.Fatalf("expected to find replaced entry")
	}
	if hit.Detection.CommercialShortID != 60 {
		t.Fatalf("expected replaced entry short id 60, got %d", hit.Detection.CommercialShortID)
	}
}

func TestDedupBuffer_ReplaceFallsBackToAdd(t *testing.T) {
	buf := NewDedupBuffer(60 * time.Second)
	station := uuid.New()
	client := uuid.New()
	now := time.Now()

	missing := mkEntry(station, client, 99, 30, now, now)
	replacement := mkEntry(station, client, 30, 30, now, now)
	if buf.Replace(&missing, replacement) {
		t.Fatalf("Replace should have returned false for missing entry")
	}
	if got := buf.Len(); got != 1 {
		t.Fatalf("expected entry to be appended, got %d", got)
	}
}

func TestDedupBuffer_Concurrent(t *testing.T) {
	buf := NewDedupBuffer(60 * time.Second)
	station := uuid.New()
	client := uuid.New()
	now := time.Now()

	const goroutines = 16
	const perG = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(idx int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				buf.Add(mkEntry(station, client, int32(idx*perG+i), 30,
					now.Add(time.Duration(i)*time.Millisecond),
					now.Add(time.Duration(i)*time.Millisecond)))
				_ = buf.Find(station, client, now, 30)
			}
		}(g)
	}
	wg.Wait()

	if got := buf.Len(); got != goroutines*perG {
		t.Fatalf("expected %d entries, got %d", goroutines*perG, got)
	}

	// GC everything; concurrent code shouldn't have corrupted slice state.
	buf.GC(now.Add(time.Hour))
	if got := buf.Len(); got != 0 {
		t.Fatalf("expected GC to drop all entries, got %d", got)
	}
}
