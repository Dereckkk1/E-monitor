package events

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	SubjectFingerprintGenerate = "fingerprint.generate"
	SubjectIndexReload         = "index.reload"
	// SubjectDetectionPending carries a state-machine confirmation from the
	// ingestor to the supervisor for §18.2.2 version disambiguation. The
	// supervisor decides whether to publish (SubjectDetectionConfirmed),
	// suppress, or retract a previously-published detection. Workers no
	// longer publish directly to SubjectDetectionConfirmed.
	SubjectDetectionPending = "detections.pending"
	// SubjectDetectionConfirmed is the post-disambiguation event consumed by
	// evidence and webhook delivery.
	SubjectDetectionConfirmed = "detections.confirmed"
	// SubjectDetectionRetracted signals that a previously published detection
	// was overruled by a longer cut from the same client (§18.2.2). Webhook
	// deliverer fans the event out to subscribers and the catalog updates
	// detections.retracted_at.
	SubjectDetectionRetracted = "detections.retracted"
	// SubjectFingerprintSharedScan triggers the shared-hash detection pass
	// for a freshly-persisted commercial. Published by the Python fingerprint
	// service AFTER write_hashes + mark_status('ready'), consumed by the api
	// process which decodes the master, runs MatchWindow against the existing
	// catalog, and flags fingerprint_hashes.is_shared on overlapping ranges.
	// See docs/shared-hash-detection.md.
	SubjectFingerprintSharedScan = "fingerprint.shared-scan"
	// SubjectMaterialSimilarityCheck triggers the per-client similarity scan
	// (workers/internal/similarity) after a material's fingerprint becomes
	// ready. Payload: {"material_id":"<uuid>"}. Published by the Python
	// fingerprint daemon, consumed by similarity.Subscriber in the api process.
	// See docs/superpowers/specs/2026-05-13-material-similarity-warning-design.md.
	SubjectMaterialSimilarityCheck = "material.similarity-check"
)

func Connect(url string) (*nats.Conn, error) {
	nc, err := nats.Connect(url,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("nats: connect: %w", err)
	}
	return nc, nil
}

func Close(nc *nats.Conn) {
	if nc != nil {
		nc.Drain()
	}
}
