package calibration

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPercentile99_EmptyReturnsFloor(t *testing.T) {
	assert.Equal(t, float64(5), percentile99(nil))
}

func TestPercentile99_OneHundredItems(t *testing.T) {
	data := make([]float64, 100)
	for i := range data {
		data[i] = float64(i + 1)
	}
	assert.Equal(t, float64(99), percentile99(data))
}

func TestPercentile99_SingleItem(t *testing.T) {
	assert.Equal(t, float64(42), percentile99([]float64{42}))
}
