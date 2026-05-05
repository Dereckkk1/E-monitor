package ringbuffer

import (
	"sync"
	"time"
)

type byteChunk struct {
	data []byte
	at   time.Time
}

// ByteRing is a thread-safe ring buffer of timestamped byte chunks.
// Capacity is expressed in number of chunks, not bytes.
// It is used by the evidence service to hold raw AAC audio from the stream.
type ByteRing struct {
	mu       sync.Mutex
	chunks   []byteChunk
	capacity int
	head     int
	size     int
}

// NewByteRing creates a new ByteRing with the given chunk capacity.
func NewByteRing(capacity int) *ByteRing {
	if capacity <= 0 {
		panic("ringbuffer: capacity must be > 0")
	}
	return &ByteRing{
		chunks:   make([]byteChunk, capacity),
		capacity: capacity,
	}
}

// Write appends a deep copy of data as a new chunk timestamped at t.
// If the buffer is full the oldest chunk is overwritten.
func (r *ByteRing) Write(data []byte, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cp := make([]byte, len(data))
	copy(cp, data)

	r.chunks[r.head] = byteChunk{data: cp, at: at}
	r.head = (r.head + 1) % r.capacity
	if r.size < r.capacity {
		r.size++
	}
}

// Extract returns the concatenation of all chunk data whose timestamp falls
// within [from, to] (inclusive), in chronological order.
func (r *ByteRing) Extract(from, to time.Time) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Determine the index of the oldest chunk.
	// When the buffer is full, the oldest chunk is at r.head.
	// When not full, the oldest chunk is at index 0.
	out := []byte{}
	oldest := 0
	if r.size == r.capacity {
		oldest = r.head
	}

	for i := 0; i < r.size; i++ {
		idx := (oldest + i) % r.capacity
		chunk := r.chunks[idx]
		if !chunk.at.Before(from) && !chunk.at.After(to) {
			out = append(out, chunk.data...)
		}
	}
	return out
}
