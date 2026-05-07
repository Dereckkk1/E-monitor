package index

import (
	"sync"
	"testing"
)

// TestNew_EmptyIndex verifies that a freshly constructed Store returns an
// empty (non-nil) index — Lookup must never panic on a brand-new store.
func TestNew_EmptyIndex(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New returned nil")
	}
	idx := s.Load()
	if idx == nil {
		t.Fatal("Load returned nil index")
	}
	if len(idx) != 0 {
		t.Fatalf("expected empty index, got %d entries", len(idx))
	}
	if got := s.Lookup(0xDEADBEEF); got != nil {
		t.Fatalf("Lookup on empty store should return nil, got %v", got)
	}
}

// TestSwap_ReplacesIndex verifies that Swap atomically replaces the index
// and that subsequent reads see the new entries.
func TestSwap_ReplacesIndex(t *testing.T) {
	s := New()
	idx := Index{
		0x1: []Entry{{CommercialShortID: 10, VariantID: 0, RateID: 0, TimeFrame: 5}},
		0x2: []Entry{{CommercialShortID: 20, VariantID: 1, RateID: 2, TimeFrame: 9}},
	}
	s.Swap(idx)

	if got := s.Lookup(0x1); len(got) != 1 || got[0].CommercialShortID != 10 {
		t.Fatalf("Lookup 0x1: got %v", got)
	}
	if got := s.Lookup(0x2); len(got) != 1 || got[0].VariantID != 1 || got[0].RateID != 2 {
		t.Fatalf("Lookup 0x2: got %v", got)
	}
	if got := s.Lookup(0x3); got != nil {
		t.Fatalf("Lookup of missing key should return nil, got %v", got)
	}

	// Swap again — old entries must vanish.
	s.Swap(make(Index))
	if got := s.Lookup(0x1); got != nil {
		t.Fatalf("after swap to empty, Lookup 0x1 should be nil; got %v", got)
	}
}

// TestSwap_SnapshotIsolation verifies that callers holding a previously
// returned Index keep seeing the old data after Swap — i.e. atomic pointer
// swap doesn't mutate the previous map in place.
func TestSwap_SnapshotIsolation(t *testing.T) {
	s := New()
	first := Index{0xAA: []Entry{{CommercialShortID: 1}}}
	s.Swap(first)
	snap := s.Load()

	second := Index{0xBB: []Entry{{CommercialShortID: 2}}}
	s.Swap(second)

	// snap should still see the first index, not the second.
	if _, ok := snap[0xAA]; !ok {
		t.Fatal("old snapshot should still see 0xAA")
	}
	if _, ok := snap[0xBB]; ok {
		t.Fatal("old snapshot must NOT see 0xBB after Swap")
	}
	if _, ok := s.Load()[0xBB]; !ok {
		t.Fatal("current load should see 0xBB")
	}
}

// TestStore_ConcurrentLookup hammers Lookup and Swap concurrently to give
// -race a chance to flag any unsafe pointer access.
func TestStore_ConcurrentLookup(t *testing.T) {
	s := New()
	idx := Index{0x42: []Entry{{CommercialShortID: 7, TimeFrame: 1}}}
	s.Swap(idx)

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = s.Lookup(0x42)
					_ = s.Lookup(0xFFFF) // miss
				}
			}
		}()
	}

	// Writer goroutine swaps repeatedly, then signals readers to stop.
	for i := 0; i < 200; i++ {
		next := Index{0x42: []Entry{{CommercialShortID: int32(i % 100), TimeFrame: int32(i)}}}
		s.Swap(next)
	}
	close(stop)
	readers.Wait()

	// Sanity: Lookup still returns something for 0x42.
	if got := s.Lookup(0x42); len(got) == 0 {
		t.Fatal("after concurrent run, Lookup should still find 0x42")
	}
}

// TestEntryFields ensures the Entry struct's basic field types behave as
// expected (uint8 wraparound boundaries are documented in loader.go).
func TestEntryFields(t *testing.T) {
	e := Entry{
		CommercialShortID: 32767,
		VariantID:         255,
		RateID:            255,
		TimeFrame:         123456,
	}
	if e.VariantID != 255 || e.RateID != 255 {
		t.Fatalf("unexpected field values: %+v", e)
	}
}
