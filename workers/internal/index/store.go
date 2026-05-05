package index

import (
	"sync/atomic"
)

// Entry is one posting in the hash index.
type Entry struct {
	CommercialShortID int32
	VariantID         uint8 // broadcast simulation variant (0=original, 1=light, 2=medium, 3=heavy)
	RateID            uint8 // time-stretch rate variant
	TimeFrame         int32
}

// Index maps hash values to their entry lists.
// It is read-only after construction and replaced atomically.
type Index map[uint32][]Entry

// Store holds the active index and supports atomic hot-swap.
type Store struct {
	ptr atomic.Pointer[Index]
}

// New returns an empty Store with an initialized (empty) index.
func New() *Store {
	emptyIndex := make(Index)
	s := &Store{}
	s.ptr.Store(&emptyIndex)
	return s
}

// Load returns the current Index. Safe for concurrent reads.
func (s *Store) Load() Index {
	idx := s.ptr.Load()
	return *idx
}

// Swap atomically replaces the current index with idx.
func (s *Store) Swap(idx Index) {
	s.ptr.Store(&idx)
}

// Lookup returns all entries for the given hash. Returns nil if not found.
func (s *Store) Lookup(hash uint32) []Entry {
	idx := s.Load()
	return idx[hash]
}
