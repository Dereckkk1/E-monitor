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

	pool, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	nc, err := events.Connect(cfg.NATSURL)
	if err != nil {
		log.Fatalf("nats: %v", err)
	}
	defer nc.Close()

	s3Client, err := storage.New(ctx, cfg.S3Endpoint, cfg.S3Bucket, cfg.S3Region,
		cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		log.Fatalf("s3: %v", err)
	}

	stations := catalog.NewStations(pool)
	clients := catalog.NewClients(pool)
	campaigns := catalog.NewCampaigns(pool)
	commercials := catalog.NewCommercials(pool)
	detections := catalog.NewDetections(pool)
	healthEvents := catalog.NewHealthEvents(pool)

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
	sup := supervisor.New(pool, indexStore, nc, evidSvc, campaigns, stations, commercials, healthEvents, logger)

	// Re-launch workers for campaigns that were active before restart.
	if err := sup.RestoreActive(ctx); err != nil {
		logger.Warn("supervisor restore active failed", zap.Error(err))
	}

	// Lifecycle scheduler (§18.2.1): promote programada→ativa→concluida by date.
	sup.StartLifecycle(ctx)

	// Daily calibration job: promote stations out of calibration mode after 7 days (§9.4).
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
	}

	deps := api.Deps{
		Stations:     &handlers.StationsHandler{Repo: stations},
		Clients:      &handlers.ClientsHandler{Repo: clients},
		Campaigns:    campaignsHandler,
		Commercials:  &handlers.CommercialsHandler{Repo: commercials, NATS: nc, MastersPath: cfg.MastersPath, Supervisor: sup},
		Detections:   &handlers.DetectionsHandler{Repo: detections, Storage: s3Client},
		Health:       &handlers.HealthHandler{DB: pool, NATS: nc, Sup: sup},
		StreamHealth: &handlers.StreamHealthHandler{HealthEvents: healthEvents, Stations: stations},
		Auth:         handlers.NewAuthHandler(pool),
		APIKey:       auth.NewAPIKeyMiddleware(pool),
		APIKeys:      handlers.NewAPIKeysHandler(pool),
		Admin:        &handlers.AdminHandler{Tiering: tieringJob, Log: logger},
		Webhooks:     handlers.NewWebhooksHandler(pool, clients, deliverer.Outbox()),
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
