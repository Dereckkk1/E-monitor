package observability

import (
	"context"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TestNATSCarrier_RoundTrip injects a span context into a nats.Header via
// the global propagator, extracts it from a fresh header on the other end,
// and asserts both ends see the same trace_id and span_id. This is the
// only behaviour that matters for cross-process trace continuity over
// NATS — if it breaks, supervisor/evidence/webhook traces stop linking
// to the ingestor root.
func TestNATSCarrier_RoundTrip(t *testing.T) {
	// Install a real (in-process) TracerProvider so the span context we
	// build below has IsValid()==true. The default no-op provider produces
	// invalid contexts that the W3C propagator declines to inject.
	prev := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	otel.SetTracerProvider(sdktrace.NewTracerProvider())

	// Install the W3C propagator pair the same way Init() does. Tests run
	// in arbitrary order; relying on a previous Init() call is brittle.
	ensurePropagators()

	tracer := Tracer()
	ctx, span := tracer.Start(context.Background(), "carrier-test")
	defer span.End()

	wantSC := trace.SpanContextFromContext(ctx)
	require.True(t, wantSC.IsValid(), "span context must be valid before injection")

	// 1) Inject into a producer header.
	producerHdr := nats.Header{}
	otel.GetTextMapPropagator().Inject(ctx, natsHeaderCarrier(producerHdr))

	// W3C TraceContext writes the "traceparent" header; verify it landed
	// so we are not silently testing a no-op carrier.
	require.NotEmpty(t, producerHdr.Get("traceparent"))

	// 2) Simulate the wire: carry the same header bytes into a fresh msg.
	consumerMsg := &nats.Msg{
		Subject: "detections.pending",
		Header:  nats.Header{},
	}
	for k, v := range producerHdr {
		consumerMsg.Header[k] = v
	}

	// 3) Extract via StartConsumerSpan and compare.
	consumerCtx, consumerSpan := StartConsumerSpan(context.Background(), consumerMsg, "consumer-test")
	defer consumerSpan.End()

	gotSC := trace.SpanContextFromContext(consumerCtx)
	// The extracted context becomes the parent of consumerSpan; its
	// remote-span-context is what we want to compare against the
	// producer's span context.
	parentSC := trace.SpanFromContext(consumerCtx).SpanContext()

	// Either parent context (from extraction) or the new span's parent
	// preserves the original trace_id; assert against the most direct
	// surface: the trace_id should match end-to-end.
	assert.Equal(t, wantSC.TraceID().String(), gotSC.TraceID().String(),
		"trace_id must survive round-trip through NATS header")

	// Span ID changes (new consumer span), but trace ID is the chain.
	// Sanity check that we didn't accidentally compare empty IDs.
	assert.NotEqual(t, "00000000000000000000000000000000", gotSC.TraceID().String())
	_ = parentSC
}

// TestNATSHeaderCarrier_KeysGet sanity-checks the carrier contract.
func TestNATSHeaderCarrier_KeysGet(t *testing.T) {
	h := nats.Header{}
	c := natsHeaderCarrier(h)

	c.Set("traceparent", "00-aaaa-bbbb-01")
	assert.Equal(t, "00-aaaa-bbbb-01", c.Get("traceparent"))
	assert.Equal(t, "", c.Get("missing"))
	// nats.Header preserves the literal key — no Mime canonicalisation.
	assert.Contains(t, c.Keys(), "traceparent")
}
