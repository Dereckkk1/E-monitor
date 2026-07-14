package projrecon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/metrics"
)

type fakeRec struct {
	drifts    []catalog.ProjectionDrift
	healed    int64
	countErr  error
	healErr   error
	healCalls int
	lastSince time.Time
}

func (f *fakeRec) CountProjectionDrift(_ context.Context, since time.Time) ([]catalog.ProjectionDrift, error) {
	f.lastSince = since
	return f.drifts, f.countErr
}
func (f *fakeRec) HealProjectionDrift(context.Context, time.Time) (int64, error) {
	f.healCalls++
	return f.healed, f.healErr
}

func TestRunOnce_HealsOnlyWhenDriftFound(t *testing.T) {
	rec := &fakeRec{}
	s := New(rec, nil)

	// Sem drift → não chama Heal.
	found, healed, err := s.RunOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, found)
	require.Zero(t, healed)
	require.Zero(t, rec.healCalls)

	// Métrica: após um ciclo limpo o gauge do último run zera.
	require.Equal(t, 0.0, testutil.ToFloat64(metrics.ProjectionDriftLastRun))

	// Com drift → cura e reporta.
	rec.drifts = []catalog.ProjectionDrift{{CampaignID: uuid.New(), From: "orphan", To: "in_slot", N: 10}}
	rec.healed = 10
	healedBefore := testutil.ToFloat64(metrics.ProjectionDriftHealed.WithLabelValues("orphan", "in_slot"))
	found, healed, err = s.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(10), found)
	require.Equal(t, int64(10), healed)
	require.Equal(t, 1, rec.healCalls)

	// Métrica: contador da transição curada subiu pelo N do drift (delta —
	// o registry é process-global e compartilhado entre os testes).
	healedAfter := testutil.ToFloat64(metrics.ProjectionDriftHealed.WithLabelValues("orphan", "in_slot"))
	require.Equal(t, healedBefore+10, healedAfter)

	// Lookback aplicado: since ≈ now-Lookback.
	require.WithinDuration(t, time.Now().Add(-s.Lookback), rec.lastSince, time.Minute)
}

func TestRunOnce_CountErrorPropagates(t *testing.T) {
	rec := &fakeRec{countErr: errors.New("boom")}
	s := New(rec, nil)
	_, _, err := s.RunOnce(context.Background())
	require.Error(t, err)
	require.Zero(t, rec.healCalls)
}

func TestRunOnce_HealErrorPropagates(t *testing.T) {
	rec := &fakeRec{
		drifts:  []catalog.ProjectionDrift{{CampaignID: uuid.New(), From: "in_slot", To: "orphan", N: 7}},
		healErr: errors.New("heal boom"),
	}
	s := New(rec, nil)

	// Heal falha → propaga erro, healed==0, e NÃO conta a transição curada.
	healedBefore := testutil.ToFloat64(metrics.ProjectionDriftHealed.WithLabelValues("in_slot", "orphan"))
	found, healed, err := s.RunOnce(context.Background())
	require.Error(t, err)
	require.Equal(t, int64(7), found)
	require.Zero(t, healed)
	require.Equal(t, 1, rec.healCalls)

	healedAfter := testutil.ToFloat64(metrics.ProjectionDriftHealed.WithLabelValues("in_slot", "orphan"))
	require.Equal(t, healedBefore, healedAfter)
}
