package supervisor

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestContarAtivas_SoMonitoradasComWorkerAtivo(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	st := []WorkerStatus{
		{StationID: a.String(), Active: true},
		{StationID: b.String(), Active: false},
		{StationID: uuid.NewString(), Active: true}, // worker de emissora fora do escopo
	}
	require.Equal(t, 1, ContarAtivas(st, []uuid.UUID{a, b, c}))
	require.Equal(t, 0, ContarAtivas(nil, []uuid.UUID{a}))
}
