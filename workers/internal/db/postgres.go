package db

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

func New(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}
	// DB_MAX_CONNS: teto do pool compartilhado (API + reqmetrics + webhook +
	// jobs). Default 20 (comportamento histórico); prod usa 40 — dimensionado
	// contra max_connections=100 do Postgres, deixando folga p/ psql/backup.
	maxConns := int32(20)
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 90 {
			maxConns = int32(n)
		}
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 2

	// OpenTelemetry instrumentation (§15.3). Each query becomes a span named
	// "pgx.query.<sql>" with attributes db.system=postgresql, db.statement,
	// and the connect URL stripped of credentials. The tracer is a no-op when
	// the global TracerProvider is not configured (see internal/observability).
	cfg.ConnConfig.Tracer = otelpgx.NewTracer(
		otelpgx.WithTrimSQLInSpanName(),
	)

	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connCtx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}
	if err := pool.Ping(connCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}
