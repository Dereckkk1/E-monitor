package neural_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"radiocheck/internal/neural"
)

func TestCosineSimilarity_Identical(t *testing.T) {
	a := []float64{1, 0, 0}
	assert.InDelta(t, 1.0, neural.CosineSimilarity(a, a), 0.001)
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float64{1, 0}
	b := []float64{0, 1}
	assert.InDelta(t, 0.0, neural.CosineSimilarity(a, b), 0.001)
}

func TestCosineSimilarity_LengthMismatch(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{1, 2}
	assert.Equal(t, 0.0, neural.CosineSimilarity(a, b))
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	a := []float64{0, 0, 0}
	b := []float64{1, 2, 3}
	assert.Equal(t, 0.0, neural.CosineSimilarity(a, b))
}
