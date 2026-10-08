package supervisor

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
)

func set(ids ...uuid.UUID) map[uuid.UUID]struct{} {
	m := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

func sortedIDs(ids []uuid.UUID) []uuid.UUID {
	out := append([]uuid.UUID(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// TestPlanFleetStarts cobre a carência de duas passadas: uma emissora coberta
// por campanha ativa e sem entrada no mapa do supervisor só ganha worker se
// faltava também na passada anterior. Na primeira, pode ser um Start() ou um
// restart do stall watchdog em andamento — subir junto criaria dois
// startStationWorker concorrentes na mesma emissora (ffmpeg órfão).
func TestPlanFleetStarts(t *testing.T) {
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	cases := []struct {
		name        string
		desired     []uuid.UUID
		present     map[uuid.UUID]struct{}
		prevMissing map[uuid.UUID]struct{}
		wantStart   []uuid.UUID
		wantMissing map[uuid.UUID]struct{}
	}{
		{
			name:        "all present → nothing to do",
			desired:     []uuid.UUID{a, b},
			present:     set(a, b),
			prevMissing: set(),
			wantStart:   nil,
			wantMissing: set(),
		},
		{
			name:        "missing for the first time → wait one pass",
			desired:     []uuid.UUID{a, b},
			present:     set(a),
			prevMissing: set(),
			wantStart:   nil,
			wantMissing: set(b),
		},
		{
			name:        "missing on two passes in a row → start",
			desired:     []uuid.UUID{a, b},
			present:     set(a),
			prevMissing: set(b),
			wantStart:   []uuid.UUID{b},
			wantMissing: set(b),
		},
		{
			name:        "came back between passes → forgotten",
			desired:     []uuid.UUID{a, b},
			present:     set(a, b),
			prevMissing: set(b),
			wantStart:   nil,
			wantMissing: set(),
		},
		{
			name:        "left the active set between passes → not started",
			desired:     []uuid.UUID{a},
			present:     set(a),
			prevMissing: set(c),
			wantStart:   nil,
			wantMissing: set(),
		},
		{
			name:        "present but not desired (orphan) → not this reconciler's job",
			desired:     []uuid.UUID{a},
			present:     set(a, d),
			prevMissing: set(),
			wantStart:   nil,
			wantMissing: set(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStart, gotMissing := planFleetStarts(tc.desired, tc.present, tc.prevMissing)
			require.Equal(t, sortedIDs(tc.wantStart), sortedIDs(gotStart))
			require.Equal(t, tc.wantMissing, gotMissing)
		})
	}
}

// TestReconcileFleetOnce: emissora em campanha ativa sem worker (o worker
// falhou ao subir, ou a campanha virou 'ativa' por SQL direto) ganha worker na
// segunda passada; a que já tem worker não é tocada; e a marcação
// monitoring_status é acertada já na primeira.
func TestReconcileFleetOnce(t *testing.T) {
	f := newReconcileFixture(t) // f.stationID já tem entry no mapa

	missing, err := catalog.NewStations(f.pool).Create(f.ctx, catalog.CreateStationInput{
		Name: "fleet-missing-" + uuid.NewString(), Band: "FM", StreamURL: "http://fleet-missing.invalid/s",
	})
	require.NoError(t, err)
	t.Cleanup(func() { f.pool.Exec(f.ctx, `DELETE FROM stations WHERE id=$1`, missing.ID) })
	require.NoError(t, f.s.stations.UpdateMonitoringStatus(f.ctx, missing.ID, "paused"))

	_, err = f.pool.Exec(f.ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status, target_stations)
		VALUES ($1, $2, CURRENT_DATE - 1, CURRENT_DATE + 20, 'ativa', ARRAY[$3::uuid, $4::uuid])`,
		f.clientID, "fleet-"+uuid.NewString(), f.stationID, missing.ID)
	require.NoError(t, err)

	var started []uuid.UUID
	start := func(_ context.Context, id uuid.UUID) error {
		started = append(started, id)
		return nil
	}

	// 1ª passada: só anota a falta; a marcação já é acertada.
	prev := f.s.reconcileFleetOnce(f.ctx, map[uuid.UUID]struct{}{}, start)
	require.Empty(t, started, "first pass must only record the gap")
	require.Contains(t, prev, missing.ID)
	require.NotContains(t, prev, f.stationID, "station with a worker entry is present")
	var ms string
	require.NoError(t, f.pool.QueryRow(f.ctx, `SELECT monitoring_status FROM stations WHERE id=$1`, missing.ID).Scan(&ms))
	require.Equal(t, "active", ms, "covered station is marked active")

	// 2ª passada: ainda faltando → sobe. (Contains, não Equal: o DB de teste
	// compartilhado pode ter campanhas 'ativa' de outros testes.)
	f.s.reconcileFleetOnce(f.ctx, prev, start)
	require.Contains(t, started, missing.ID)
	require.NotContains(t, started, f.stationID, "station with a worker must not be restarted")
}
