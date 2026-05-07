package supervisor

// Webhook publisher hook (§13.1.4).
//
// Decision: in this codebase confirmed detections are NOT published from the
// supervisor. The ingestor.Worker (workers/internal/ingestor/worker.go,
// publishDetection at line ~404) emits NATS messages on the
// `detections.confirmed` subject, and the supervisor only owns lifecycle of
// those workers — it never sees individual detections.
//
// The webhook subsystem subscribes to that same NATS subject (see
// webhook.Deliverer in workers/internal/webhook/deliverer.go), so the
// detection->webhook fan-out is already wired without touching supervisor.go
// or ingestor/worker.go (both of which are owned by other agents in this
// development cycle).
//
// PublishDetectionWebhook is therefore implemented as a no-op TODO: callers
// inside the supervisor package can adopt it later if a code path is added
// that needs to enqueue a webhook *outside* the NATS pipeline (for example,
// re-emitting a detection during a backfill job). It is intentionally typed
// against `any` so it can be stubbed without depending on the webhook
// package, avoiding an import cycle today.

import (
	"context"
)

// PublishDetectionWebhook is a hook for future use. Today the
// detection->webhook bridge runs through NATS (see webhook.Deliverer) so the
// supervisor doesn't need to call anything synchronously. This stub gives a
// stable place to plug an outbox enqueue if direct publishing becomes
// desirable.
//
// IMPORTANT: do not call this from supervisor.go without first wiring an
// `OutboxEnqueuer` dependency into Supervisor (see comment above).
func (s *Supervisor) PublishDetectionWebhook(ctx context.Context, detection any) {
	if s == nil || s.log == nil {
		return
	}
	s.log.Debug("supervisor: PublishDetectionWebhook is a no-op (handled via NATS bridge)")
}
