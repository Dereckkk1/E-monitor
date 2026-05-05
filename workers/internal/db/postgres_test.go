package db

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewConnects(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := New(ctx, url)
	require.NoError(t, err)
	defer pool.Close()

	var v int
	err = pool.QueryRow(ctx, "SELECT 1").Scan(&v)
	require.NoError(t, err)
	require.Equal(t, 1, v)
}
