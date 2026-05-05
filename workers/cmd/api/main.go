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
	"radiocheck/internal/catalog"
	"radiocheck/internal/config"
	"radiocheck/internal/db"
	"radiocheck/internal/evidence"
	"radiocheck/internal/events"
	"radiocheck/internal/index"
	"radiocheck/internal/storage"
	"radiocheck/internal/supervisor"
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

	// Supervisor.
	sup := supervisor.New(pool, indexStore, nc, evidSvc, campaigns, stations, commercials, logger)

	// Re-launch workers for campaigns that were active before restart.
	if err := sup.RestoreActive(ctx); err != nil {
		logger.Warn("supervisor restore active failed", zap.Error(err))
	}

	// Campaigns handler with supervisor wired in.
	campaignsHandler := &handlers.CampaignsHandler{
		Repo:       campaigns,
		Supervisor: sup,
	}

	deps := api.Deps{
		Stations:    &handlers.StationsHandler{Repo: stations},
		Clients:     &handlers.ClientsHandler{Repo: clients},
		Campaigns:   campaignsHandler,
		Commercials: &handlers.CommercialsHandler{Repo: commercials, NATS: nc, MastersPath: cfg.MastersPath},
		Detections:  &handlers.DetectionsHandler{Repo: detections, Storage: s3Client},
		Health:      &handlers.HealthHandler{DB: pool, NATS: nc},
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
