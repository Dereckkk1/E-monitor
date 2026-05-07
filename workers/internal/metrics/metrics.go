package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	WorkerBytesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_worker_bytes_received_total",
		Help: "Total bytes received from stream per station.",
	}, []string{"station_id"})

	WorkerReconnectsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_worker_reconnects_total",
		Help: "Total stream reconnect attempts per station.",
	}, []string{"station_id"})

	WorkerStallRestarts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_worker_stall_restarts_total",
		Help: "Total stall-triggered worker restarts per station.",
	}, []string{"station_id"})

	WorkerActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "radiocheck_worker_active_total",
		Help: "Number of currently running workers.",
	})

	MatchWindowDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "radiocheck_match_window_duration_seconds",
		Help:    "Duration of each matching window computation.",
		Buckets: prometheus.DefBuckets,
	}, []string{"station_id"})

	DetectionTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_detections_total",
		Help: "Total detection events by station and status.",
	}, []string{"station_id", "status"})

	EvidenceUploadFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "radiocheck_evidence_upload_failures_total",
		Help: "Total evidence upload failures.",
	})

	EvidenceQueueSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "radiocheck_evidence_queue_size",
		Help: "Number of items in evidence retry spool.",
	})

	// Webhook delivery (§13.1.4).
	WebhookDeliveriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_webhook_deliveries_total",
		Help: "Total webhook deliveries by terminal status.",
	}, []string{"status"}) // enqueued | delivered | failed | retry | dead

	WebhookDeliveryDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "radiocheck_webhook_delivery_duration_seconds",
		Help:    "HTTP duration of webhook POST requests.",
		Buckets: prometheus.DefBuckets,
	})

	WebhookQueueSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "radiocheck_webhook_queue_size",
		Help: "Number of webhook_deliveries rows with status='pending'.",
	})

	WebhookDLQSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "radiocheck_webhook_dlq_size",
		Help: "Number of webhook_deliveries rows with status='dead'.",
	})
)

func init() {
	prometheus.MustRegister(
		WorkerBytesTotal, WorkerReconnectsTotal, WorkerStallRestarts,
		WorkerActive, MatchWindowDuration, DetectionTotal,
		EvidenceUploadFailures, EvidenceQueueSize,
		WebhookDeliveriesTotal, WebhookDeliveryDuration, WebhookQueueSize, WebhookDLQSize,
	)
}
