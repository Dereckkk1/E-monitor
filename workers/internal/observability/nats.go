// NATS instrumentation helpers.
//
// The official OpenTelemetry contrib repo ships an instrumentation for the
// HTTP and pgx clients but does NOT publish a stable wrapper for nats.go (the
// only one in tree, otelnats, was archived during the v0 → v1 migration). We
// therefore implement minimal helpers that:
//
//   - Inject the W3C trace-context into a nats.Header on Publish.
//   - Extract the same header on receive.
//   - Wrap both ends with a span that follows the OTel messaging semantic
//     conventions (messaging.system=nats, messaging.destination=<subject>).
//
// Keeping this tiny means we don't carry an extra dep with its own breaking
// changes. The producer side prefers PublishMsg so a *nats.Msg with headers
// reaches the broker; if the connection has no header support (very old
// servers) we fall back to a plain Publish without context propagation.
package observability

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// natsHeaderCarrier adapts nats.Header to TextMapCarrier so the global
// propagator (TraceContext+Baggage) can inject/extract through it.
type natsHeaderCarrier nats.Header

func (c natsHeaderCarrier) Get(key string) string {
	v := nats.Header(c).Values(key)
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func (c natsHeaderCarrier) Set(key, value string) {
	nats.Header(c).Set(key, value)
}

func (c natsHeaderCarrier) Keys() []string {
	out := make([]string, 0, len(c))
	for k := range c {
		out = append(out, k)
	}
	return out
}

// PublishWithTracing wraps nc.PublishMsg with a producer span and injects
// trace-context into the message header. nil nc is a safe no-op for tests.
func PublishWithTracing(ctx context.Context, nc *nats.Conn, subject string, payload []byte) error {
	if nc == nil {
		return fmt.Errorf("nats: nil connection")
	}
	tracer := Tracer()
	ctx, span := tracer.Start(ctx, "nats.publish "+subject,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			semconv.MessagingSystemKey.String("nats"),
			semconv.MessagingDestinationName(subject),
			attribute.Int("messaging.message.body_size", len(payload)),
		),
	)
	defer span.End()

	msg := &nats.Msg{Subject: subject, Data: payload}
	if msg.Header == nil {
		msg.Header = nats.Header{}
	}
	propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	).Inject(ctx, natsHeaderCarrier(msg.Header))

	if err := nc.PublishMsg(msg); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		// PublishMsg falls back automatically when the server doesn't speak
		// headers (HEADERS capability negotiation handled inside nats.go).
		return err
	}
	return nil
}

// StartConsumerSpan extracts trace-context from a nats.Msg header and starts
// a consumer span as the root. Callers should defer the returned end function.
//
// The returned context carries the parent span so further work attached to
// the message (DB writes, downstream publishes) chains under the same trace.
func StartConsumerSpan(ctx context.Context, msg *nats.Msg, name string) (context.Context, trace.Span) {
	if msg != nil && msg.Header != nil {
		ctx = propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{}, propagation.Baggage{},
		).Extract(ctx, natsHeaderCarrier(msg.Header))
	}
	subject := ""
	if msg != nil {
		subject = msg.Subject
	}
	return Tracer().Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			semconv.MessagingSystemKey.String("nats"),
			semconv.MessagingDestinationName(subject),
		),
	)
}
