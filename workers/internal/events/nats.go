package events

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	SubjectFingerprintGenerate = "fingerprint.generate"
	SubjectIndexReload         = "index.reload"
	SubjectDetectionConfirmed  = "detections.confirmed"
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
