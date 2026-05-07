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

	// ── Backup & restore (§14.4) ─────────────────────────────────────────
	// These are mirrored from the textfile-collector exports written by
	// infra/scripts/backup.sh & restore-test.sh, but also exposed by the API
	// process so dashboards have a single source of truth when the host is
	// not running node_exporter.
	PostgresBackupLastSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "radiocheck_postgres_backup_last_success_timestamp",
		Help: "Unix epoch of the most recent successful pg_basebackup.",
	})

	PostgresBackupSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "radiocheck_postgres_backup_size_bytes",
		Help: "Size of the most recent pg_basebackup tarball, in bytes.",
	})

	PostgresBackupDuration = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "radiocheck_postgres_backup_duration_seconds",
		Help: "Wall-clock duration of the most recent pg_basebackup attempt.",
	})

	PostgresRestoreTestStatus = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "radiocheck_postgres_restore_test_last_status",
		Help: "Result of the most recent restore-test run (1 ok, 0 fail).",
	})

	// ── Evidence tiering (§11.4) ─────────────────────────────────────────
	EvidenceTierMovements = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_evidence_tier_movements_total",
		Help: "Number of evidence objects moved between storage tiers.",
	}, []string{"from", "to"})

	EvidenceStorageBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "radiocheck_evidence_storage_bytes",
		Help: "Aggregate evidence storage by tier, in bytes.",
	}, []string{"tier"})

	EvidenceTieringLastRun = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "radiocheck_evidence_tiering_last_run_timestamp",
		Help: "Unix epoch of the last evidence tiering job run.",
	})

	EvidenceTieringErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "radiocheck_evidence_tiering_errors_total",
		Help: "Total non-fatal errors hit by the evidence tiering job.",
	})
)

func init() {
	prometheus.MustRegister(
		WorkerBytesTotal, WorkerReconnectsTotal, WorkerStallRestarts,
		WorkerActive, MatchWindowDuration, DetectionTotal,
		EvidenceUploadFailures, EvidenceQueueSize,
		PostgresBackupLastSuccess, PostgresBackupSize, PostgresBackupDuration,
		PostgresRestoreTestStatus,
		EvidenceTierMovements, EvidenceStorageBytes,
		EvidenceTieringLastRun, EvidenceTieringErrors,
	)
}
