package observability

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
)

// ObserveWithTraceExemplar attaches the current span's trace_id as a
// Prometheus exemplar to the given histogram observation. When the active
// span is invalid (sampling disabled, no provider set) it falls back to a
// plain Observe so dashboards keep recording the value.
//
// Implementation notes
//
//   - Prometheus exemplars require the metric to satisfy ExemplarObserver.
//     Newer histogram types do; older releases may not. We type-assert and
//     downgrade on failure to keep this safe to call from any code path.
//   - The label MUST be exactly "trace_id" so Grafana's Tempo / Jaeger
//     datasource can auto-link the exemplar to the trace.
func ObserveWithTraceExemplar(ctx context.Context, h prometheus.Observer, value float64) {
	if h == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	sc := span.SpanContext()
	if !sc.IsValid() {
		h.Observe(value)
		return
	}
	if eo, ok := h.(prometheus.ExemplarObserver); ok {
		eo.ObserveWithExemplar(value, prometheus.Labels{"trace_id": sc.TraceID().String()})
		return
	}
	h.Observe(value)
}
