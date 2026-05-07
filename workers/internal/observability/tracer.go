package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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

// PropagateTraceContext copies the OTel SpanContext from src into dst without
// inheriting cancellation. Useful when a request handler kicks off an async
// goroutine that must survive the request's lifetime but should remain part
// of the same trace.
func PropagateTraceContext(src, dst context.Context) context.Context {
	sc := trace.SpanContextFromContext(src)
	if !sc.IsValid() {
		return dst
	}
	return trace.ContextWithSpanContext(dst, sc)
}

// AddSpanAttributes attaches kv to the active span on ctx (if any). No-op
// when ctx carries no recording span — handy for places that want to
// annotate without forcing the caller to thread span variables.
func AddSpanAttributes(ctx context.Context, kv ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(kv...)
}
