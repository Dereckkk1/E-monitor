package supervisor

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"radiocheck/internal/match"
)

func mkEntry(stationID uuid.UUID, clientID uuid.UUID, shortID int32, dur int, detectedAt time.Time, insertedAt time.Time) DedupEntry {
	return DedupEntry{
		Detection: match.ConfirmedDetection{
			CommercialShortID: shortID,
			StationID:         stationID.String(),
			DetectedAt:        detectedAt,
			FirstMatchAt:      detectedAt.Add(-2 * time.Second),
			OffsetFrames:      0,
			Confidence:        0.9,
		},
		ClientID:        clientID,
		DurationSeconds: dur,
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

	buf.Add(mkEntry(station, client, 1, 60, now, now))

	// Within window, same client + station → hit.
	if got := buf.Find(station, client, now.Add(3*time.Second), 5*time.Second); got == nil {
		t.Fatalf("expected hit within window")
	}

	// Outside window → miss.
	if got := buf.Find(station, client, now.Add(10*time.Second), 5*time.Second); got != nil {
		t.Fatalf("expected miss outside window, got %+v", got)
	}

	// Different client → miss.
	if got := buf.Find(station, otherClient, now, 5*time.Second); got != nil {
		t.Fatalf("expected miss across clients, got %+v", got)
	}

	// Different station → miss.
	if got := buf.Find(otherStation, client, now, 5*time.Second); got != nil {
		t.Fatalf("expected miss across stations, got %+v", got)
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
	hit := buf.Find(station, client, now.Add(2*time.Second), 5*time.Second)
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
				_ = buf.Find(station, client, now, 5*time.Second)
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
