package projrecon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
)

type fakeRec struct {
	drifts    []catalog.ProjectionDrift
	healed    int64
	countErr  error
	healCalls int
	lastSince time.Time
}

func (f *fakeRec) CountProjectionDrift(_ context.Context, since time.Time) ([]catalog.ProjectionDrift, error) {
	f.lastSince = since
	return f.drifts, f.countErr
}
func (f *fakeRec) HealProjectionDrift(context.Context, time.Time) (int64, error) {
	f.healCalls++
	return f.healed, nil
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

	// Com drift → cura e reporta.
	rec.drifts = []catalog.ProjectionDrift{{CampaignID: uuid.New(), From: "orphan", To: "in_slot", N: 10}}
	rec.healed = 10
	found, healed, err = s.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(10), found)
	require.Equal(t, int64(10), healed)
	require.Equal(t, 1, rec.healCalls)

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
