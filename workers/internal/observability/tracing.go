// Package observability wires up OpenTelemetry tracing for the Radiocheck
// workers (§15.3 of the plan).
//
// The package is intentionally minimal: it owns the global TracerProvider and
// nothing else. Helpers for HTTP, Postgres and NATS instrumentation live in
// sibling files (tracing_http.go, tracing_db.go, tracing_nats.go) so callers
// can import only what they need without dragging the OTLP exporter into unit
// tests.
//
// Defaults
//
//   - Endpoint defaults to "localhost:4317" (OTLP gRPC, the Jaeger all-in-one
//     image accepts it on the same port).
//   - Sampling defaults to "parentbased_traceidratio" with ratio 1.0; a
//     production deployment should set OTEL_TRACES_SAMPLER_ARG to a smaller
//     value (e.g. 0.1) once volume picks up.
//   - If neither OTEL_EXPORTER_OTLP_ENDPOINT nor OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
//     is set, the package installs a no-op TracerProvider so library callers
//     work the same way without a collector running.
//
// The init function returns a `shutdown` closure the caller must invoke before
// process exit so the in-memory span batch is flushed.
package observability

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ServiceVersion is exposed as the resource attribute service.version. Bumped
// manually on releases; left as "dev" by default.
const ServiceVersion = "dev"

// ShutdownFunc is the cleanup closure returned by Init. Callers MUST invoke it
// before process exit (with a short timeout) so pending spans are flushed.
type ShutdownFunc func(context.Context) error

// noopShutdown is returned when tracing is disabled — calling it is harmless.
func noopShutdown(context.Context) error { return nil }

// Init configures the global TracerProvider and propagators. Safe to call
// multiple times: subsequent calls return the previous shutdown unchanged.
//
// When OTEL_EXPORTER_OTLP_ENDPOINT (or its trace-specific variant) is empty,
// a no-op provider is installed and Init returns a no-op shutdown — no error.
// This lets local dev / CI run without a collector.
func Init(ctx context.Context, serviceName string) (ShutdownFunc, error) {
	endpoint := otlpEndpoint()
	if endpoint == "" {
		// Honour the user-installed propagator if any; otherwise install the
		// W3C trace-context + baggage pair so http/nats helpers can extract
		// inbound contexts even when we don't export anything.
		ensurePropagators()
		// Force a TracerProvider that returns no-op tracers explicitly. This
		// avoids surprising "no provider set" panics in callers that pass our
		// tracer to libraries that assume one exists.
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
		return noopShutdown, nil
	}

	exporter, err := buildOTLPExporter(ctx, endpoint)
	if err != nil {
		return noopShutdown, fmt.Errorf("observability: build otlp exporter: %w", err)
	}

	res, err := buildResource(ctx, serviceName)
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return noopShutdown, fmt.Errorf("observability: build resource: %w", err)
	}

	sampler := buildSampler()

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithMaxExportBatchSize(512),
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(provider)
	ensurePropagators()

	shutdown := func(shutdownCtx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(shutdownCtx, 10*time.Second)
		defer cancel()
		// Ignore the join error so a second shutdown call is harmless.
		flushErr := provider.ForceFlush(shutdownCtx)
		closeErr := provider.Shutdown(shutdownCtx)
		if flushErr != nil {
			return flushErr
		}
		return closeErr
	}
	return shutdown, nil
}

// otlpEndpoint reads the OTel-standard endpoint env vars in precedence order.
// Empty return means tracing is disabled.
func otlpEndpoint() string {
	if v := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
}

// buildOTLPExporter constructs an OTLP gRPC exporter. We force insecure here
// because the local Jaeger container accepts plaintext on 4317 and prod
// deployments will sit behind a TLS-terminating sidecar / agent. Production
// users that need TLS to the collector directly can swap the dial option.
func buildOTLPExporter(ctx context.Context, endpoint string) (*otlptrace.Exporter, error) {
	// Strip a leading scheme if the operator pasted the full URL.
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")

	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	}
	return otlptracegrpc.New(ctx, opts...)
}

// buildResource composes service.name + service.version + deployment.environment
// + the custom OTEL_RESOURCE_ATTRIBUTES bag.
func buildResource(ctx context.Context, serviceName string) (*resource.Resource, error) {
	env := strings.TrimSpace(os.Getenv("RADIOCHECK_ENV"))
	if env == "" {
		env = "development"
	}
	overrideName := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME"))
	if overrideName != "" {
		serviceName = overrideName
	}
	version := strings.TrimSpace(os.Getenv("OTEL_SERVICE_VERSION"))
	if version == "" {
		version = ServiceVersion
	}
	return resource.New(ctx,
		resource.WithFromEnv(), // honours OTEL_RESOURCE_ATTRIBUTES
		resource.WithTelemetrySDK(),
		resource.WithProcess(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(version),
			semconv.DeploymentEnvironment(env),
		),
	)
}

// buildSampler honours OTEL_TRACES_SAMPLER + OTEL_TRACES_SAMPLER_ARG. Anything
// unrecognised falls back to ParentBased(AlwaysOn) — consistent with the OTel
// SDK defaults across languages.
func buildSampler() sdktrace.Sampler {
	name := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER")))
	arg := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG"))

	switch name {
	case "always_on", "alwayson", "":
		return sdktrace.AlwaysSample()
	case "always_off", "alwaysoff":
		return sdktrace.NeverSample()
	case "traceidratio":
		ratio := parseRatio(arg, 1.0)
		return sdktrace.TraceIDRatioBased(ratio)
	case "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample())
	case "parentbased_traceidratio":
		ratio := parseRatio(arg, 1.0)
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))
	default:
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
}

func parseRatio(s string, def float64) float64 {
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || v > 1 {
		return def
	}
	return v
}

// ensurePropagators installs W3C TraceContext + Baggage as the global
// propagators if no propagator was set yet. Idempotent — safe to call from
// every Init() invocation and from tests.
func ensurePropagators() {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
}
