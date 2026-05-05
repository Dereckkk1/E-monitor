package ringbuffer

import (
	"sync"
)

// PCMRing is a thread-safe ring buffer of float32 PCM samples with a fixed
// capacity in samples. It is used by the match engine to hold recent PCM
// audio for analysis windows.
type PCMRing struct {
	mu       sync.Mutex
	samples  []float32
	capacity int
	head     int
	size     int
}

// NewPCMRing creates a new PCMRing with the given sample capacity.
func NewPCMRing(capacity int) *PCMRing {
	return &PCMRing{
		samples:  make([]float32, capacity),
		capacity: capacity,
	}
}

// Write appends samples into the ring buffer, overwriting the oldest samples
// when the buffer is full.
func (r *PCMRing) Write(s []float32) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, v := range s {
		r.samples[r.head] = v
		r.head = (r.head + 1) % r.capacity
		if r.size < r.capacity {
			r.size++
		}
	}
}

// ReadLast returns the last min(n, r.size) samples in chronological order
// (oldest to newest).
func (r *PCMRing) ReadLast(n int) []float32 {
	r.mu.Lock()
	defer r.mu.Unlock()

	if n > r.size {
		n = r.size
	}
	if n == 0 {
		return []float32{}
	}

	out := make([]float32, n)
	// The oldest of the last n samples sits at (head - size + size - n) = head - n
	// expressed circularly.
	start := ((r.head - n) % r.capacity + r.capacity) % r.capacity
	for i := 0; i < n; i++ {
		out[i] = r.samples[(start+i)%r.capacity]
	}
	return out
}

// Size returns the current number of samples stored in the buffer.
func (r *PCMRing) Size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size
}
