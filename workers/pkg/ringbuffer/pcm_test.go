package ringbuffer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPCMRing_WriteAndReadLast(t *testing.T) {
	r := NewPCMRing(10)
	r.Write([]float32{1, 2, 3})
	got := r.ReadLast(3)
	assert.Equal(t, []float32{1, 2, 3}, got)
}

func TestPCMRing_OverflowKeepsLastN(t *testing.T) {
	r := NewPCMRing(4)
	r.Write([]float32{1, 2, 3, 4, 5})
	got := r.ReadLast(4)
	assert.Equal(t, []float32{2, 3, 4, 5}, got)
}

func TestPCMRing_ReadLastMoreThanSize(t *testing.T) {
	r := NewPCMRing(10)
	r.Write([]float32{1, 2, 3})
	got := r.ReadLast(10)
	assert.Equal(t, []float32{1, 2, 3}, got)
}
