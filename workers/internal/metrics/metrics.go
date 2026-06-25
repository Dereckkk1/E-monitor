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

	// ── Campaign lifecycle (§18.2.1) ─────────────────────────────────────
	// CampaignsByStatus is a snapshot gauge of campaigns grouped by status.
	// Updated by the lifecycle scheduler on every tick.
	CampaignsByStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "radiocheck_campaigns_by_status",
		Help: "Number of campaigns currently in each lifecycle status.",
	}, []string{"status"})

	// CampaignTransitions counts every status transition observed by the
	// lifecycle scheduler (programada→ativa and ativa→concluida).
	CampaignTransitions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_campaign_lifecycle_transitions_total",
		Help: "Total campaign lifecycle status transitions, labeled by from/to.",
	}, []string{"from", "to"})

	// ── Threshold dinâmico (§9.4) ────────────────────────────────────────
	// StationThreshold tracks the current min_hashes value applied by each
	// running worker. Updated on worker startup and on every periodic /
	// admin-triggered refresh. Useful to spot drift between calibration
	// state and what the matcher is actually using in memory.
	StationThreshold = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "radiocheck_station_threshold",
		Help: "Current min_hashes threshold applied by the worker for each station.",
	}, []string{"station_id"})

	// StationThresholdRefreshes counts every refresh the supervisor performs
	// against station_thresholds, labelled by outcome. 'updated' means the
	// value changed; 'unchanged' means it matched the in-memory copy;
	// 'error' means GetThreshold failed (and the in-memory copy was kept).
	StationThresholdRefreshes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_station_threshold_refreshes_total",
		Help: "Number of dynamic threshold refresh attempts per station, by outcome.",
	}, []string{"station_id", "outcome"})

	// WorkerCommercials is the number of commercials a worker has loaded for a
	// station. Set by the supervisor on every (re)start and on every
	// reconciler tick. A value of 0 on an active campaign's station is the
	// signature failure mode that masked the 2026-05-08 missed detections —
	// alert if it stays at 0 longer than the reconciler interval.
	WorkerCommercials = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "radiocheck_worker_commercials",
		Help: "Number of commercials currently loaded by the worker for each station.",
	}, []string{"station_id"})

	// WorkerReconcileRuns counts every reconciler pass per outcome.
	// 'unchanged' = list matched DB; 'restarted' = list differed and worker
	// was rebuilt; 'error' = the lookup failed (previous state preserved).
	WorkerReconcileRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_worker_reconcile_runs_total",
		Help: "Outcome of the per-station commercial reconciler ticks.",
	}, []string{"station_id", "outcome"})

	// WorkerConnectBackoff is the current circuit-breaker backoff (seconds) the
	// supervisor is waiting before respawning a worker that has never connected
	// (flavor B stall — see supervisor/connect_backoff.go). 0 = connected /
	// normal cadence. A station stuck near the cap (1800s) is firewall-banned
	// or has a dead URL; surfaced on /operations and used to spot IP blocks
	// (incidente jun/2026: 34.39.163.110 dropped by livespanel/streamingdevideo).
	WorkerConnectBackoff = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "radiocheck_worker_connect_backoff_seconds",
		Help: "Current connect-backoff delay before respawning a never-connecting worker, per station.",
	}, []string{"station_id"})

	// ── Calibration scheduler (§9.4) ─────────────────────────────────────
	// Counter: every recalibration attempt, labeled by terminal result.
	CalibrationRunsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_calibration_runs_total",
		Help: "Total per-station calibration scheduler runs, labeled by result.",
	}, []string{"result"}) // success | error

	// Gauge: unix epoch of the last successful recalibration per station.
	// Used by the CalibrationStale alert.
	CalibrationLastSuccessTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "radiocheck_calibration_last_success_timestamp",
		Help: "Unix epoch of the most recent successful recalibration per station.",
	}, []string{"station_id"})

	// Histogram: recalibration duration per call (any station). We deliberately
	// drop the station_id label here: with 200+ stations × ~10 buckets that
	// would mean 2k+ time series in Prometheus per scrape — a cardinality
	// bomb for a metric that observers consume as an aggregate. If a per-
	// station drill-down is ever needed, derive it from `runs_total` +
	// per-station logs instead.
	//
	// Buckets are tuned for the actual range we observe: the SQL itself
	// resolves in milliseconds, but stuck connections / slow runs can cross
	// many seconds; the upper bucket aligns with StationTimeout (5min).
	CalibrationDurationSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "radiocheck_calibration_duration_seconds",
		Help:    "Duration of a single calibration scheduler run (any station).",
		Buckets: []float64{5, 15, 30, 60, 120, 300},
	})

	// ── Version disambiguation (§18.2.2) ─────────────────────────────────
	// MatchDisambiguation counts how often a confirmed detection was either
	// suppressed at submission time (a longer cut already published) or
	// caused the supervisor to retract a previously published row.
	MatchDisambiguation = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_match_disambiguation_total",
		Help: "Detections affected by §18.2.2 version disambiguation, by action.",
	}, []string{"action"}) // suppressed | retracted | reattributed_by_coverage | restored_on_reject | reattributed_on_reject

	// ── §9.9 Audit de Evidência Pré-Persist ────────────────────────────
	// AuditAttempts counts each audit run by outcome:
	//   passed   — saved clip matches master, detection is kept
	//   rejected — saved clip does NOT match master, evidence_status='audit_rejected'
	//   error    — audit infra failure (DB/ffmpeg); upload proceeded as if disabled
	AuditAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_audit_attempts_total",
		Help: "Pre-persist audit runs by outcome (passed|rejected|error).",
	}, []string{"result"})

	// AuditScore tracks the histogram peak count produced by each audit.
	// Buckets chosen to span noise (1–3) through clean matches (10–100).
	AuditScore = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "radiocheck_audit_score",
		Help:    "Peak histogram score from §9.9 audit (higher = stronger match).",
		Buckets: []float64{1, 2, 3, 5, 8, 12, 20, 40, 80, 160},
	})

	// AuditCoverage tracks the fraction of master frames present in the
	// winning delta bin (the §9.6 "temporal coverage" calculated post-hoc
	// on the saved clip).
	AuditCoverage = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "radiocheck_audit_coverage",
		Help:    "Coverage (distinct master frames / total) from §9.9 audit.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.2, 0.3, 0.4, 0.5, 0.7, 0.9},
	})

	// AuditDuration tracks audit latency on the async evidence path.
	AuditDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "radiocheck_audit_duration_seconds",
		Help:    "Wall-clock duration of §9.9 audit (decode + hashes + histogram).",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
	})

	// ── Fila de fingerprint (reconciler — incidente 2026-06-12) ──
	// FingerprintStuck mede materiais presos por status no último tick do
	// reconciler. >0 sustentado = fila travada (alerta FingerprintStuck).
	FingerprintStuck = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "radiocheck_fingerprint_stuck",
		Help: "Materiais com fingerprint preso (pending/generating velhos ou failed).",
	}, []string{"status"})

	FingerprintRetriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_fingerprint_retries_total",
		Help: "Re-disparos de fingerprint.generate pelo reconciler, por status original.",
	}, []string{"status"})

	// ── Cooldown × re-veiculação (T8-A, plano de remediação 2026-06-12) ──
	// Conta matches com assinatura de NOVA veiculação (offset no início do
	// comercial, metade final do cooldown) descartados pelo StateCooldown.
	// Volume relevante aqui = justificativa pra implementar re-arm (T8-B).
	CooldownPossibleReairTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_cooldown_possible_reair_total",
		Help: "Possíveis re-veiculações engolidas pelo cooldown da state machine.",
	}, []string{"station_id"})

	// ── Notificações por email (emails diários de alerta de campanha) ──
	NotificationsSentTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_notifications_sent_total",
		Help: "Total de emails de alerta de campanha enviados, por tipo.",
	}, []string{"type"})

	NotificationsFailedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_notifications_failed_total",
		Help: "Total de falhas de envio de email de alerta, por tipo.",
	}, []string{"type"})

	NotificationsRecipients = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "radiocheck_notifications_recipients",
		Help: "Número de destinatários do último disparo, por tipo.",
	}, []string{"type"})
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
		WebhookDeliveriesTotal, WebhookDeliveryDuration, WebhookQueueSize, WebhookDLQSize,
		CampaignsByStatus, CampaignTransitions,
		StationThreshold, StationThresholdRefreshes,
		WorkerCommercials, WorkerReconcileRuns, WorkerConnectBackoff,
		CalibrationRunsTotal, CalibrationLastSuccessTimestamp, CalibrationDurationSeconds,
		MatchDisambiguation,
		AuditAttempts, AuditScore, AuditCoverage, AuditDuration,
		NotificationsSentTotal, NotificationsFailedTotal, NotificationsRecipients,
		FingerprintStuck, FingerprintRetriesTotal,
		CooldownPossibleReairTotal,
	)
}
