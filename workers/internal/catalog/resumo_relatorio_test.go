package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTotaisDoResumo_BaseInSlotMaisBonusEArredondamento(t *testing.T) {
	pmm := 1000.4
	alvo := 300
	por := []StationAggregateRow{
		{StationPMM: &pmm, StationPMMTarget: &alvo, Count: 3, ImpactCount: 2},
		{Count: 1, ImpactCount: 1}, // sem PMM: fora dos impactos
	}
	tot := TotaisDoResumo(&MaterialAggregateResult{TotalDetections: 4, DistinctMaterials: 1}, por)
	require.Equal(t, ResumoTotais{Detections: 4, DistinctMaterials: 1, DistinctStations: 2,
		Impactos: 2001, ImpactosTarget: 600, StationsWithTarget: 1}, tot)
	require.EqualValues(t, 2001, *ImpactosDaEmissora(por[0]))
	require.Nil(t, ImpactosDaEmissora(por[1]), "sem PMM = não se aplica, não zero")
}
