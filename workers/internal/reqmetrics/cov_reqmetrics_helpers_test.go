package reqmetrics

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"radiocheck/internal/db"
)

// covZNewPool abre um pool contra TEST_DATABASE_URL; pula o teste se a env
// não estiver definida (mesmo contrato dos demais pacotes).
func covZNewPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return ctx, pool
}

// covZCountMetrics conta linhas de system_metrics de uma rota.
func covZCountMetrics(t *testing.T, ctx context.Context, pool *pgxpool.Pool, route string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM system_metrics WHERE route = $1`, route).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
