package observability

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// instrumentationName is the import path used as the OTel instrumentation
// scope. The standard convention is "package path", but we collapse all
// Radiocheck spans under a single name so dashboards can filter by service
// instead of by Go package.
const instrumentationName = "radiocheck"

// Tracer returns the package-wide tracer. Sub-packages should call this
// instead of building their own via otel.Tracer so the instrumentation scope
// stays uniform across all spans.
func Tracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}
