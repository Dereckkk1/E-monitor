package ringbuffer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestByteRing_WriteAndExtract(t *testing.T) {
	r := NewByteRing(5)

	t0 := time.Now()
	t1 := t0.Add(1 * time.Second)
	t2 := t0.Add(2 * time.Second)

	r.Write([]byte{1, 2}, t0)
	r.Write([]byte{3, 4}, t1)
	r.Write([]byte{5, 6}, t2)

	got := r.Extract(t0, t2)
	assert.Equal(t, []byte{1, 2, 3, 4, 5, 6}, got)
}

func TestByteRing_OverwritesOldest(t *testing.T) {
	r := NewByteRing(2)

	t0 := time.Now()
	t1 := t0.Add(1 * time.Second)
	t2 := t0.Add(2 * time.Second)

	r.Write([]byte{1, 2}, t0) // will be overwritten
	r.Write([]byte{3, 4}, t1)
	r.Write([]byte{5, 6}, t2)

	// Only the last two chunks (t1 and t2) should remain.
	got := r.Extract(t0, t2)
	assert.Equal(t, []byte{3, 4, 5, 6}, got)
}

func TestByteRing_ExtractPartialWindow(t *testing.T) {
	r := NewByteRing(5)
	t0 := time.Now()
	t1 := t0.Add(time.Second)
	t2 := t0.Add(2 * time.Second)
	r.Write([]byte("a"), t0)
	r.Write([]byte("b"), t1)
	r.Write([]byte("c"), t2)
	// Extract only middle chunk
	out := r.Extract(t1, t1)
	assert.Equal(t, []byte("b"), out)
}
