package catalog

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"radiocheck/internal/db"
)

// newTestDB returns a raw pool connected to TEST_DATABASE_URL.
// It skips the test if the env var is not set and closes the pool on cleanup.
//
// Use this in repo tests where you need a generic pool to instantiate your
// repo (e.g. catalog.NewMaterialTypes(pool)). For tests that need the Stations
// repo specifically, see newTestPool in stations_test.go.
func newTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	return ctx, pool
}
