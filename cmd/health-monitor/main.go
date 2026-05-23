// DFMS Health Monitor — periodically probes storage node health via
// MinIO read/write tests. Updates node status in PostgreSQL and publishes
// health change events to Kafka for the Replication Manager to act on.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/AnirudhSinghRajora/DFMS/internal/config"
	"github.com/AnirudhSinghRajora/DFMS/internal/database"
	"github.com/AnirudhSinghRajora/DFMS/internal/events"
	"github.com/AnirudhSinghRajora/DFMS/internal/health"
	"github.com/AnirudhSinghRajora/DFMS/internal/observability"
)

func main() {
	configPath := os.Getenv("DFMS_CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.dev.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logger, err := observability.NewLogger(cfg.Server.Mode, "health-monitor")
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("Starting DFMS Health Monitor")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := database.NewPool(ctx, &cfg.Database)
	if err != nil {
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}
	defer pool.Close()

	producer := events.NewProducer(cfg.Kafka.Brokers, logger)
	defer func() { _ = producer.Close() }()

	monitor := health.NewMonitor(pool, producer, cfg.HealthMonitor, &cfg.MinIO, logger)

	// Run monitor in background
	go func() {
		if err := monitor.Run(ctx); err != nil {
			logger.Error("Health monitor exited", zap.Error(err))
		}
	}()

	logger.Info("Health Monitor running")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down Health Monitor...")
	cancel()
	logger.Info("Health Monitor stopped")
}
