package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestLiveCoverageResult_Totals(t *testing.T) {
	e1, e2 := uuid.New(), uuid.New()
	r := LiveCoverageResult{Cities: []CoverageCity{
		{StationID: e1, IBGECode: 1}, {StationID: e1, IBGECode: 2}, {StationID: e1, IBGECode: 3},
		{StationID: e2, IBGECode: 2}, // a mesma cidade alcançada por outra emissora
	}}
	require.Equal(t, CoverageTotals{DistinctCities: 3, StationCityPairs: 4}, r.Totals())
	require.Equal(t, CoverageTotals{}, LiveCoverageResult{}.Totals())
}
