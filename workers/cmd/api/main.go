package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/api"
	"radiocheck/internal/api/handlers"
	"radiocheck/internal/auth"
	"radiocheck/internal/calibration"
	"radiocheck/internal/catalog"
	"radiocheck/internal/config"
	"radiocheck/internal/db"
	"radiocheck/internal/evidence"
	"radiocheck/internal/events"
	"radiocheck/internal/index"
	"radiocheck/internal/observability"
	"radiocheck/internal/sharing"
	"radiocheck/internal/storage"
	"radiocheck/internal/supervisor"
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
	matTypesRepo  := catalog.NewMaterialTypes(pool)
	matsRepo      := catalog.NewMaterials(pool)
	cmpMatsRepo   := catalog.NewCampaignMaterials(pool)
	distRulesRepo := catalog.NewDistributionRules(pool)
	distOverRepo  := catalog.NewDistributionOverrides(pool)
	pricingRepo   := catalog.NewPricing(pool)
	dailySumRepo  := catalog.NewDailySummary(pool)

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
	defer indexSub.Unsubscribe() //nolint:errcheck

	// Shared-hash detection (§18.2.2 follow-up). Subscribes to
	// fingerprint.shared-scan, runs MatchWindow against the catalog, flags
	// is_shared on overlapping ranges, then republishes index.reload so the
	// in-memory index picks up the new flags. See docs/shared-hash-detection.md.
	sharingSubscriber := sharing.NewSubscriber(pool, nc, logger)
	sharingSub, err := sharingSubscriber.Subscribe(ctx)
	if err != nil {
		log.Fatalf("sharing subscribe: %v", err)
	}
	defer sharingSub.Unsubscribe() //nolint:errcheck

	// Evidence service.
	evidSvc := evidence.NewService(pool, s3Client, nc, detections, logger)
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
	sup := supervisor.New(pool, indexStore, nc, evidSvc, campaigns, stations, commercials, healthEvents, cfg.SegmentsPath, logger)

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

	// Campaigns handler with supervisor wired in.
	campaignsHandler := &handlers.CampaignsHandler{
		Repo:       campaigns,
		Supervisor: sup,
		Log:        logger,
	}

	deps := api.Deps{
		Stations:     &handlers.StationsHandler{Repo: stations},
		Clients:      &handlers.ClientsHandler{Repo: clients},
		Campaigns:    campaignsHandler,
		Commercials:  &handlers.CommercialsHandler{Repo: commercials, NATS: nc, MastersPath: cfg.MastersPath, Supervisor: sup, Log: logger},
		Detections:   &handlers.DetectionsHandler{Repo: detections, Storage: s3Client, SummaryRepo: dailySumRepo},
		Health:       &handlers.HealthHandler{DB: pool, NATS: nc, Sup: sup},
		StreamHealth: &handlers.StreamHealthHandler{HealthEvents: healthEvents, Stations: stations},
		Auth:         handlers.NewAuthHandler(pool),
		APIKey:       auth.NewAPIKeyMiddleware(pool),
		APIKeys:      handlers.NewAPIKeysHandler(pool),
		Admin:        &handlers.AdminHandler{Tiering: tieringJob, Threshold: sup, Calibration: calibrationScheduler, Log: logger},
		Webhooks:     handlers.NewWebhooksHandler(pool, clients, deliverer.Outbox()),
		MaterialTypes:         &handlers.MaterialTypesHandler{Repo: matTypesRepo},
		Materials:             &handlers.MaterialsHandler{Repo: matsRepo, MastersPath: cfg.MastersPath, NATS: nc},
		CampaignMaterials:     &handlers.CampaignMaterialsHandler{Repo: cmpMatsRepo, Supervisor: sup, Log: logger},
		DistributionRules:     &handlers.DistributionRulesHandler{Repo: distRulesRepo},
		DistributionOverrides: &handlers.DistributionOverridesHandler{Repo: distOverRepo},
		Pricing:               &handlers.PricingHandler{Repo: pricingRepo},
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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx) //nolint:errcheck
}
