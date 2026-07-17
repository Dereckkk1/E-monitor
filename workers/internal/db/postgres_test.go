package db

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewConnects(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	defer pool.Close()

	var v int
	err = pool.QueryRow(ctx, "SELECT 1").Scan(&v)
	require.NoError(t, err)
	require.Equal(t, 1, v)
}

func TestResolveMaxConns(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int32
		wantErr bool
	}{
		{name: "absent uses default", raw: "", want: 20, wantErr: false},
		{name: "valid in-range", raw: "40", want: 40, wantErr: false},
		{name: "non-numeric falls back to default", raw: "4O", want: 20, wantErr: true},
		{name: "zero falls back to default", raw: "0", want: 20, wantErr: true},
		{name: "negative falls back to default", raw: "-1", want: 20, wantErr: true},
		{name: "1 falls back to default (below MinConns=2)", raw: "1", want: 20, wantErr: true},
		{name: "above range falls back to default", raw: "91", want: 20, wantErr: true},
		{name: "lower boundary 2 is accepted", raw: "2", want: 2, wantErr: false},
		{name: "upper boundary 90 is accepted", raw: "90", want: 90, wantErr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveMaxConns(tc.raw)
			require.Equal(t, tc.want, got)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
