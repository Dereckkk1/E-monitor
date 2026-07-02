package main

import (
	"context"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/api"
	"radiocheck/internal/api/handlers"
	"radiocheck/internal/audit"
	"radiocheck/internal/auth"
	"radiocheck/internal/calibration"
	"radiocheck/internal/campaignalerts"
	"radiocheck/internal/catalog"
	"radiocheck/internal/config"
	"radiocheck/internal/db"
	"radiocheck/internal/events"
	"radiocheck/internal/evidence"
	"radiocheck/internal/fingerprintqueue"
	"radiocheck/internal/index"
	"radiocheck/internal/mailer"
	"radiocheck/internal/observability"
	"radiocheck/internal/reqmetrics"
	"radiocheck/internal/sharing"
	"radiocheck/internal/similarity"
	"radiocheck/internal/storage"
	"radiocheck/internal/supervisor"
	"radiocheck/internal/users"
	"radiocheck/internal/webhook"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx := context.Background()

	// Logger.
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("logger: %v", err)
	}
	defer logger.Sync() //nolint:errcheck

	// OpenTelemetry tracing (§15.3). Init returns a no-op shutdown when no
	// OTLP endpoint is configured so dev / CI keep working without a
	// collector. The deferred shutdown flushes pending spans on SIGTERM.
	tracingShutdown, err := observability.Init(ctx, "radiocheck-api")
	if err != nil {
		logger.Warn("tracing init failed; continuing without traces", zap.Error(err))
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tracingShutdown(shutdownCtx); err != nil {
			logger.Warn("tracing shutdown failed", zap.Error(err))
		}
	}()

	pool, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	// Bootstrap admin user (§16). Idempotent: when the email already
	// exists, this is a no-op; when env vars are unset, also no-op.
	// Documented in docs/auth-bootstrap.md.
	if err := auth.EnsureAdmin(ctx, pool, auth.BootstrapConfig{
		Email:    os.Getenv("RADIOCHECK_BOOTSTRAP_ADMIN_EMAIL"),
		Password: os.Getenv("RADIOCHECK_BOOTSTRAP_ADMIN_PASSWORD"),
		Role:     "admin",
	}, logger); err != nil {
		log.Fatalf("bootstrap admin: %v", err)
	}

	nc, err := events.Connect(cfg.NATSURL)
	if err != nil {
		log.Fatalf("nats: %v", err)
	}
	defer nc.Close()

	s3Client, err := storage.New(ctx, cfg.S3Endpoint, cfg.S3PublicEndpoint, cfg.S3Bucket,
		cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		log.Fatalf("s3: %v", err)
	}

	stations := catalog.NewStations(pool)
	clients := catalog.NewClients(pool)
	campaigns := catalog.NewCampaigns(pool)
	commercials := catalog.NewCommercials(pool)
	detections := catalog.NewDetections(pool)
	healthEvents := catalog.NewHealthEvents(pool)

	// New catalog repos (Tasks 4-9 / 13-18).
	matTypesRepo := catalog.NewMaterialTypes(pool)
	matsRepo := catalog.NewMaterials(pool)
	cmpMatsRepo := catalog.NewCampaignMaterials(pool)
	distRulesRepo := catalog.NewDistributionRules(pool)
	distOverRepo := catalog.NewDistributionOverrides(pool)
	pricingRepo := catalog.NewPricing(pool)
	dailySumRepo := catalog.NewDailySummary(pool)

	// Index store + loader.
	indexStore := index.New()
	loader := index.NewLoader(indexStore, pool, nc, logger)
	if err := loader.LoadAll(ctx); err != nil {
		logger.Warn("index loader: initial load failed", zap.Error(err))
	}
	indexSub, err := loader.Subscribe(ctx)
	if err != nil {
		log.Fatalf("index loader subscribe: %v", err)
	}
	// Periodic index reconcile (audit E4): rebuild the whole index from the DB
	// on a timer so a dropped index.reload (NATS core is fire-and-forget) or a
	// campaign_materials mutation that missed a publish self-heals within one
	// interval. Default 30m; override with INDEX_RECONCILE_INTERVAL (Go duration).
	indexReconcileInterval := 30 * time.Minute
	if v := os.Getenv("INDEX_RECONCILE_INTERVAL"); v != "" {
		if d, perr := time.ParseDuration(v); perr == nil && d > 0 {
			indexReconcileInterval = d
		} else {
			logger.Warn("invalid INDEX_RECONCILE_INTERVAL, using default 30m", zap.String("value", v))
		}
	}
	go loader.RunReconcileLoop(ctx, indexReconcileInterval)

	// Partition maintenance (audit E1): keep monthly partitions provisioned so
	// INSERTs into detections/detection_campaigns/stream_health_events never hit
	// "no partition of relation found". Migration 0048 extends the horizon on
	// deploy; this keeps a rolling 6-month buffer, at startup and once a day.
	// Warns (never fatal) if ensure_month_partitions is somehow absent.
	go func() {
		ensure := func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := pool.Exec(bgCtx, `SELECT ensure_month_partitions(6)`); err != nil {
				logger.Warn("partition maintenance: ensure_month_partitions failed", zap.Error(err))
			}
		}
		ensure()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ensure()
			}
		}
	}()
	defer indexSub.Unsubscribe() //nolint:errcheck

	// Shared-hash detection (§18.2.2 follow-up). Subscribes to
	// fingerprint.shared-scan, runs MatchWindow against the catalog, flags
	// is_shared on overlapping ranges, then republishes index.reload so the
	// in-memory index picks up the new flags. See docs/shared-hash-detection.md.
	// Após cada shared-scan de material, popula/atualiza as regiões
	// discriminantes de gêmeos acústicos (spec 2026-07-01). Injetado como callback
	// pra evitar ciclo de import (catalog já importa sharing). Best-effort.
	twinDisc := catalog.NewTwinDiscriminative(pool)
	sharingSubscriber := sharing.NewSubscriber(pool, nc, logger, func(cbCtx context.Context, materialID uuid.UUID) {
		if err := twinDisc.PopulateForMaterial(cbCtx, materialID); err != nil {
			logger.Warn("twin-disc: PopulateForMaterial falhou (best-effort)",
				zap.String("material_id", materialID.String()), zap.Error(err))
		}
	})
	sharingSub, err := sharingSubscriber.Subscribe(ctx)
	if err != nil {
		log.Fatalf("sharing subscribe: %v", err)
	}
	defer sharingSub.Unsubscribe() //nolint:errcheck

	// Start similarity-check subscriber (per-client duplicate warning).
	simSubscriber := similarity.NewSubscriber(pool, nc, logger)
	simSub, err := simSubscriber.Subscribe(ctx)
	if err != nil {
		log.Fatalf("similarity subscribe: %v", err)
	}
	defer simSub.Unsubscribe() //nolint:errcheck

	// §9.9 Audit. Default ON; set AUDIT_ENABLED=false to bypass (kill switch
	// for emergencies — see plano §9.9). Nil auditor means evidence.Service
	// skips the audit step entirely.
	var auditor *audit.Auditor
	if os.Getenv("AUDIT_ENABLED") != "false" {
		auditor = audit.NewAuditor(pool, logger, 0, 0) // 0,0 → use §9.3 defaults
		logger.Info("audit enabled (§9.9 pre-persist evidence audit)")
	} else {
		logger.Warn("audit DISABLED via AUDIT_ENABLED=false — all detections will be persisted regardless of evidence quality")
	}

	// §18.2.2-v2 — coverage-based version disambiguation. Default OFF; set
	// DISAMBIG_BY_COVERAGE=true to enable. When off, evidence + supervisor
	// behave exactly as pre-v2 (suppress stays suppress, no reattribution).
	// Kill switch: flip the env var and restart — no code change needed.
	disambigByCoverage := os.Getenv("DISAMBIG_BY_COVERAGE") == "true"
	if disambigByCoverage {
		logger.Info("§18.2.2-v2 coverage-based disambiguation ENABLED (DISAMBIG_BY_COVERAGE=true)")
	}

	// Desambiguação de gêmeos acústicos de mesma duração pelo trecho discriminante
	// (spec 2026-07-01). Default OFF; set DISAMBIG_TWIN_DISCRIMINATIVE=true. Roda no
	// pass-path do evidence só quando o passo de cobertura não agiu. Calibrar
	// floor/margin em sombra antes de ligar em prod.
	disambigTwin := os.Getenv("DISAMBIG_TWIN_DISCRIMINATIVE") == "true"
	if disambigTwin {
		logger.Info("desambiguação de gêmeos por trecho discriminante ENABLED (DISAMBIG_TWIN_DISCRIMINATIVE=true)")
	}
	// Retração de gêmeo ambíguo (sinal fraco/confuso). Default OFF: preserva a
	// atribuição em vez de retrair (sem fila de revisão, retrair sub-contaria).
	// Ligar só depois do Plano 2 (fila de revisão). Ver docs/architecture/twin-disambiguation.md.
	twinRetractAmbiguous := os.Getenv("DISAMBIG_TWIN_RETRACT_AMBIGUOUS") == "true"
	if twinRetractAmbiguous {
		logger.Info("retração de gêmeo ambíguo ENABLED (DISAMBIG_TWIN_RETRACT_AMBIGUOUS=true)")
	}

	// F-119 multi-attribution — default OFF. Quando ON, uma tocada física conta
	// pra TODAS as campanhas que rodam o mesmo áudio na emissora (fan-out de
	// projeções em detection_campaigns). OFF = só a projeção canônica (1:1).
	multiAttribution := os.Getenv("MULTI_ATTRIBUTION") == "true"
	if multiAttribution {
		logger.Info("F-119 multi-attribution ENABLED (MULTI_ATTRIBUTION=true)")
	}

	// Dedup confidence-aware (audit A2) — default OFF. Quando ON, o dedup §18.2.2
	// escolhe o corte de MAIOR cobertura em vez do mais longo, impedindo que um
	// corte que só false-confirmou a região compartilhada suprima a veiculação
	// real (classe 90fm/ASAAS). Ligar SÓ após calibrar a margem com os dados de
	// dedup_suppressions (a cobertura é wall-clock, audit B1).
	disambigConfidenceAware := os.Getenv("DISAMBIG_CONFIDENCE_AWARE") == "true"
	if disambigConfidenceAware {
		logger.Info("dedup confidence-aware ENABLED (DISAMBIG_CONFIDENCE_AWARE=true)")
	}
	detectionCampaigns := catalog.NewDetectionCampaigns(pool)

	// Evidence service.
	evidSvc := evidence.NewService(pool, s3Client, nc, detections, detectionCampaigns, auditor, disambigByCoverage, disambigTwin, twinRetractAmbiguous, multiAttribution, logger)
	evidSub, err := evidSvc.Subscribe(ctx)
	if err != nil {
		log.Fatalf("evidence subscribe: %v", err)
	}
	defer evidSub.Unsubscribe() //nolint:errcheck

	// Evidence tiering job (§11.4). For now hot/cold/archive all share the
	// same R2/MinIO client; in production the operator can override the
	// archive client to point at a different bucket / storage class. The
	// schedule fires once a day at 03:00 BR.
	tieringJob := evidence.NewTieringJob(pool, s3Client, s3Client, s3Client, logger)
	go tieringJob.Schedule(ctx)

	// Supervisor.
	sup := supervisor.New(pool, indexStore, nc, evidSvc, campaigns, stations, commercials, matsRepo, healthEvents, disambigConfidenceAware, cfg.SegmentsPath, logger)

	// §18.2.2 — subscribe to detections.pending so the supervisor can apply
	// version disambiguation before re-emitting on detections.confirmed.
	//
	// SINGLE-INSTANCE: este subscriber não usa queue group. Rodar múltiplas
	// instâncias do binário levará a duplicação de eventos detections.confirmed
	// (cada réplica tem seu próprio dedup buffer in-memory).
	// Tracking: docs/follow-ups-fase2.md (F-70 leader election multi-réplica).
	pendingSub, err := sup.SubscribePendingDetections(ctx)
	if err != nil {
		log.Fatalf("supervisor pending subscribe: %v", err)
	}
	defer pendingSub.Unsubscribe() //nolint:errcheck

	// Re-launch workers for campaigns that were active before restart.
	if err := sup.RestoreActive(ctx); err != nil {
		logger.Warn("supervisor restore active failed", zap.Error(err))
	}

	// Lifecycle scheduler (§18.2.1): promote programada→ativa→concluida by date.
	sup.StartLifecycle(ctx)

	// Daily calibration job: promote stations out of calibration mode after 7 days (§9.4).
	// This handles the *initial* calibration window — stations entering the
	// system collect noise samples for 7 days, then get promoted with their
	// computed noise_p99.
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				jobCtx, jobCancel := context.WithTimeout(ctx, 5*time.Minute)
				if err := calibration.RunCalibrationJob(jobCtx, pool, logger); err != nil {
					logger.Error("calibration job failed", zap.Error(err))
				}
				jobCancel()
			}
		}
	}()

	// Periodic re-calibration scheduler (§9.4): every 24h, find stations
	// whose station_thresholds row hasn't been updated for ≥7d and reset
	// calibration_mode=true so a fresh sample buffer is collected. The
	// initial RunCalibrationJob above (also running daily) then promotes
	// them out again with the freshly computed noise_p99.
	//
	// Multi-replica safety: pg_try_advisory_lock guards each tick so only
	// one API instance drives the scan at a time.
	calibrationScheduler := calibration.NewScheduler(pool, logger)
	if v := os.Getenv("CALIBRATION_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			calibrationScheduler.Interval = d
		} else {
			logger.Warn("invalid CALIBRATION_INTERVAL, using default",
				zap.String("value", v))
		}
	}
	if v := os.Getenv("CALIBRATION_MIN_AGE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			calibrationScheduler.MinAge = d
		} else {
			logger.Warn("invalid CALIBRATION_MIN_AGE, using default",
				zap.String("value", v))
		}
	}
	go func() {
		if err := calibrationScheduler.Run(ctx); err != nil {
			logger.Error("calibration scheduler exited with error", zap.Error(err))
		}
	}()

	// Webhook subsystem (§13.1.4):
	//   1. Deliverer: subscribes to detections.confirmed NATS events and
	//      enqueues outbox rows in webhook_deliveries.
	//   2. Worker: polls webhook_deliveries and POSTs them with HMAC-SHA256
	//      signatures, retrying with exponential backoff up to the DLQ.
	deliverer := webhook.New(pool, nc, logger, clients, commercials, stations)
	if err := deliverer.Start(ctx); err != nil {
		logger.Warn("webhook deliverer start failed", zap.Error(err))
	}
	webhookWorker := webhook.NewWorker(pool, logger)
	go func() {
		if err := webhookWorker.Run(ctx); err != nil {
			logger.Error("webhook worker exited with error", zap.Error(err))
		}
	}()

	// Users repo — shared across auth, me, and users handlers (Tasks 5–8).
	usersRepo := users.NewRepo(pool)

	// Emails diários de alerta de campanha (docs/features/campaign-notification-emails.md).
	// Opt-in via NOTIFICATIONS_ENABLED; sem credenciais SMTP, mailer.New retorna noop.
	if cfg.NotificationsEnabled {
		mail := mailer.New(mailer.Config{
			Enabled: cfg.NotificationsEnabled,
			Host:    cfg.SMTPHost,
			Port:    cfg.SMTPPort,
			User:    cfg.SMTPUser,
			Pass:    cfg.SMTPPass,
			From:    cfg.MailFrom,
		}, logger)
		alertRepo := campaignalerts.New(pool)
		alertLogs := campaignalerts.NewLogStore(pool)
		alertSvc := campaignalerts.NewService(alertRepo, alertLogs, usersRepo, mail, cfg.NotificationsBaseURL, logger)
		alertSched := campaignalerts.NewScheduler(pool, alertSvc, cfg.NotificationsSendHour, logger)
		if v := os.Getenv("NOTIFICATIONS_INTERVAL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				alertSched.Interval = d
			}
		}
		go alertSched.Run(ctx)
		logger.Info("campaign notification emails enabled",
			zap.Int("send_hour", cfg.NotificationsSendHour),
			zap.String("base_url", cfg.NotificationsBaseURL))
	}

	// Reconciler da fila de fingerprint (incidente 2026-06-12): re-publica
	// fingerprint.generate para materiais presos em pending/generating/failed.
	// Sempre ligado — é rede de segurança, não feature.
	fpQueue := fingerprintqueue.New(pool, nc, logger)
	go fpQueue.Run(ctx)

	// Request metrics writer + IP block-list (painel /admin/monitoring).
	// Async batched writer evita pressionar latência do caminho hot.
	// Documentado em docs/features/admin-monitoring.md.
	metricsCfg := reqmetrics.Config{
		BufferSize: 4096,
		BatchSize:  200,
		FlushEvery: 2 * time.Second,
		Retention:  30 * 24 * time.Hour,
		PruneEvery: 6 * time.Hour,
		SlowMs:     2000,
	}
	metricsWriter := reqmetrics.NewWriter(pool, logger, metricsCfg)
	go metricsWriter.Run(ctx, metricsCfg)

	blockList := reqmetrics.NewBlockList(ctx, pool, logger)
	go blockList.Run(ctx)

	// Campaigns handler with supervisor wired in.
	campaignsHandler := &handlers.CampaignsHandler{
		Repo:       campaigns,
		Supervisor: sup,
		Log:        logger,
	}

	deps := api.Deps{
		Stations:     &handlers.StationsHandler{Repo: stations, Workers: sup},
		Clients:      &handlers.ClientsHandler{Repo: clients},
		Campaigns:    campaignsHandler,
		Commercials:  &handlers.CommercialsHandler{Repo: commercials, NATS: nc, MastersPath: cfg.MastersPath, Supervisor: sup, Log: logger},
		Detections:   &handlers.DetectionsHandler{Repo: detections, CampaignRepo: campaigns, Storage: s3Client, SummaryRepo: dailySumRepo},
		Health:       &handlers.HealthHandler{DB: pool, NATS: nc, Sup: sup},
		StreamHealth: &handlers.StreamHealthHandler{HealthEvents: healthEvents, Stations: stations, Sup: sup},
		Auth:         handlers.NewAuthHandler(pool, usersRepo),
		APIKey:       auth.NewAPIKeyMiddleware(pool),
		APIKeys:      handlers.NewAPIKeysHandler(pool),
		Admin:        &handlers.AdminHandler{Tiering: tieringJob, Threshold: sup, Calibration: calibrationScheduler, Log: logger},
		SystemHealth: &handlers.SystemHealthHandler{
			DB:            pool,
			NATS:          nc,
			Sup:           sup,
			RedisURL:      cfg.RedisURL,
			S3Endpoint:    cfg.S3Endpoint,
			ClapURL:       os.Getenv("CLAP_VERIFIER_URL"),
			PrometheusURL: os.Getenv("PROMETHEUS_URL"),
			GrafanaURL:    os.Getenv("GRAFANA_URL"),
			JaegerURL:     os.Getenv("JAEGER_URL"),
			Log:           logger,
		},
		AdminMonitoring: &handlers.AdminMonitoringHandler{
			DB:    pool,
			Block: blockList,
			Log:   logger,
		},
		StationFailures: &handlers.StationFailuresHandler{
			Repo: catalog.NewStationFailures(pool),
			Log:  logger,
		},
		CampaignFailures: &handlers.CampaignFailuresHandler{
			Repo: catalog.NewCampaignFailures(pool),
			Log:  logger,
		},
		Notifications: &handlers.NotificationsHandler{
			Repo: catalog.NewNotifications(pool),
			Log:  logger,
		},
		DailyFailuresDigest: &handlers.DailyFailuresDigestHandler{
			Campaigns: catalog.NewCampaignFailures(pool),
			Seen:      catalog.NewNotifications(pool),
			Log:       logger,
		},
		Metrics:               metricsWriter,
		BlockList:             blockList,
		Webhooks:              handlers.NewWebhooksHandler(pool, clients, deliverer.Outbox()),
		MaterialTypes:         &handlers.MaterialTypesHandler{Repo: matTypesRepo},
		Materials:             &handlers.MaterialsHandler{Repo: matsRepo, MastersPath: cfg.MastersPath, NATS: nc, DistRules: distRulesRepo},
		CampaignMaterials:     &handlers.CampaignMaterialsHandler{Repo: cmpMatsRepo, CampaignRepo: campaigns, Supervisor: sup, Log: logger},
		DistributionRules:     &handlers.DistributionRulesHandler{Repo: distRulesRepo, CampaignRepo: campaigns},
		DistributionOverrides: &handlers.DistributionOverridesHandler{Repo: distOverRepo},
		Pricing:               &handlers.PricingHandler{Repo: pricingRepo, CampaignRepo: campaigns},
		Users:                 handlers.NewUsersHandler(usersRepo),
		Me:                    handlers.NewMeHandler(usersRepo),
		Reports:               &handlers.ReportsHandler{Detections: detections, CampaignRepo: campaigns, Pool: pool},
		Insights:              handlers.NewInsightsHandler(catalog.NewInsights(pool)),
		LiveMap:               handlers.NewLiveMapHandler(catalog.NewLiveMap(pool)),
		ManagementOverview:    &handlers.ManagementOverviewHandler{Repo: catalog.NewManagementOverview(pool), Workers: sup},
	}

	srv := &http.Server{
		Addr:    ":" + cfg.APIPort,
		Handler: api.NewRouter(deps),
	}

	go func() {
		log.Printf("api listening on :%s", cfg.APIPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	// pprof endpoint on a separate internal listener. The handlers were
	// registered on http.DefaultServeMux by the `_ "net/http/pprof"` import;
	// we deliberately keep them off the public api.NewRouter mux so they're
	// never reachable through Cloudflare Tunnel. Access from the host with:
	//   docker compose exec api wget -qO - http://localhost:6060/debug/pprof/profile?seconds=30 > cpu.pprof
	//   go tool pprof cpu.pprof
	// Disable with PPROF_ENABLED=false. Bound to 127.0.0.1 so even within the
	// docker network it's only reachable from inside the api container.
	if os.Getenv("PPROF_ENABLED") != "false" {
		pprofSrv := &http.Server{
			Addr:    "127.0.0.1:6060",
			Handler: http.DefaultServeMux,
		}
		go func() {
			logger.Info("pprof listening on 127.0.0.1:6060 (container-internal)")
			if err := pprofSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Warn("pprof server exited", zap.Error(err))
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx) //nolint:errcheck
}
