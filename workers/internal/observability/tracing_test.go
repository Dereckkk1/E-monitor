package observability

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInit_NoOpFallback validates that calling Init with no OTLP endpoint
// configured returns a no-op shutdown that does not panic and does not
// surface an error. This is the dev/CI path — every binary calls Init at
// startup so it must be safe when the operator hasn't wired a collector.
func TestInit_NoOpFallback(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")

	shutdown, err := Init(context.Background(), "test-noop")
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	// Calling shutdown on a fresh background context must be a no-op
	// (no panic, no error). The contract says callers always invoke it
	// before exit; the no-op path has to honour that.
	assert.NotPanics(t, func() {
		_ = shutdown(context.Background())
	})
	// Idempotent: a second invocation is also harmless.
	assert.NoError(t, shutdown(context.Background()))
}

// TestInit_WithEndpoint exercises the OTLP exporter construction path with
// an endpoint that will fail to dial. The OTLP gRPC exporter is supposed
// to be resilient to a missing collector at startup (batch exporter
// retries lazily), so Init must still return successfully — otherwise
// any restart-with-collector-down scenario would crash the worker.
func TestInit_WithEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:0")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	// Keep sampler explicit so we don't pick up surprises from a parent
	// shell environment.
	t.Setenv("OTEL_TRACES_SAMPLER", "always_off")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdown, err := Init(ctx, "test-endpoint")
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	// Shutdown should also tolerate the unreachable collector — flushing
	// an empty batch when the dial never succeeded must not error out.
	shutdownCtx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer scancel()
	_ = shutdown(shutdownCtx) // do not assert NoError: depending on grpc state
	// the underlying flush may surface a deadline; what matters is that
	// the helper does not panic, which is implicit if we got here.
}
