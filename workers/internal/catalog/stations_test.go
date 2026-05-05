package catalog

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"radiocheck/internal/db"
)

func newTestPool(t *testing.T) (context.Context, *Stations) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM stations`)
		pool.Close()
	})
	return ctx, NewStations(pool)
}

func strPtr(s string) *string { return &s }
func f64Ptr(f float64) *float64 { return &f }

func TestStations_CreateListGet(t *testing.T) {
	ctx, repo := newTestPool(t)

	created, err := repo.Create(ctx, CreateStationInput{
		Name:         "Test FM",
		Band:         "FM",
		FrequencyMHz: f64Ptr(101.5),
		City:         strPtr("São Paulo"),
		State:        strPtr("SP"),
		StreamURL:    "http://example.com/stream",
	})
	require.NoError(t, err)
	require.Equal(t, "Test FM", created.Name)
	require.Equal(t, "paused", created.MonitoringStatus)

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	fetched, err := repo.Get(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, fetched.ID)

	require.NoError(t, repo.UpdateMonitoringStatus(ctx, created.ID, "active"))
	fetched, _ = repo.Get(ctx, created.ID)
	require.Equal(t, "active", fetched.MonitoringStatus)
}
