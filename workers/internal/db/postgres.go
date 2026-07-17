package db

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

const (
	// defaultMaxConns é o teto histórico do pool, usado quando DB_MAX_CONNS
	// está ausente ou inválido.
	defaultMaxConns = int32(20)
	// minAllowedMaxConns é TAMBÉM o MinConns do pool (ver New) — daí o piso do
	// range aceito: um MaxConns abaixo dele deixaria MinConns > MaxConns.
	// Os dois saem desta constante justamente pra não poderem divergir.
	minAllowedMaxConns = int32(2)
	// maxAllowedMaxConns: teto contra max_connections=100 do Postgres,
	// deixando folga p/ psql manual do operador, backup e migrate.
	maxAllowedMaxConns = int32(90)
)

// resolveMaxConns traduz o valor cru de DB_MAX_CONNS no tamanho de pool
// efetivo. raw == "" (var ausente) é o caso normal e não é erro. Qualquer
// outro valor inválido (não numérico, fora de [2, 90]) devolve o default
// (20) MAIS um erro descritivo — o caller decide logar; boot nunca falha
// por causa desta env var.
func resolveMaxConns(raw string) (int32, error) {
	if raw == "" {
		return defaultMaxConns, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return defaultMaxConns, fmt.Errorf("DB_MAX_CONNS=%q não é um inteiro válido, usando default %d: %w", raw, defaultMaxConns, err)
	}
	if n < int(minAllowedMaxConns) || n > int(maxAllowedMaxConns) {
		return defaultMaxConns, fmt.Errorf("DB_MAX_CONNS=%d fora do range [%d,%d], usando default %d", n, minAllowedMaxConns, maxAllowedMaxConns, defaultMaxConns)
	}
	return int32(n), nil
}

func New(ctx context.Context, url string, logger *zap.Logger) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}
	// DB_MAX_CONNS: teto do pool compartilhado (API + reqmetrics + webhook +
	// jobs). Default 20 (comportamento histórico); prod: 40 (setar no .env
	// da VM) — dimensionado contra max_connections=100 do Postgres.
	maxConns, err := resolveMaxConns(os.Getenv("DB_MAX_CONNS"))
	if err != nil {
		logger.Warn("invalid DB_MAX_CONNS, using default", zap.Error(err))
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = minAllowedMaxConns

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
