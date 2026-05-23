// DFMS Garbage Collector — identifies and removes orphaned chunks using
// a two-phase mark-sweep algorithm. Orphaned chunks (ref_count = 0) are
// first marked, then deleted after a configurable grace period to prevent
// race conditions with concurrent uploads.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/AnirudhSinghRajora/DFMS/internal/cache"
	"github.com/AnirudhSinghRajora/DFMS/internal/config"
	"github.com/AnirudhSinghRajora/DFMS/internal/database"
	"github.com/AnirudhSinghRajora/DFMS/internal/events"
	"github.com/AnirudhSinghRajora/DFMS/internal/gc"
	"github.com/AnirudhSinghRajora/DFMS/internal/observability"
	"github.com/AnirudhSinghRajora/DFMS/internal/storage"
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

	logger, err := observability.NewLogger(cfg.Server.Mode, "gc-worker")
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("Starting DFMS Garbage Collector")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := database.NewPool(ctx, &cfg.Database)
	if err != nil {
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}
	defer pool.Close()

	redisClient, err := cache.NewClient(cfg.Redis)
	if err != nil {
		logger.Fatal("Failed to connect to Redis", zap.Error(err))
	}
	defer func() { _ = redisClient.Close() }()

	minioClient, err := storage.NewMinIOClient(&cfg.MinIO)
	if err != nil {
		logger.Fatal("Failed to connect to MinIO", zap.Error(err))
	}
	store := storage.NewMinIOStore(minioClient, cfg.MinIO.ChunkBucket)

	producer := events.NewProducer(cfg.Kafka.Brokers, logger)
	defer func() { _ = producer.Close() }()

	collector := gc.NewCollector(pool, store, redisClient, producer, cfg.GC, logger)

	go func() {
		if err := collector.Run(ctx); err != nil {
			logger.Error("GC worker exited", zap.Error(err))
		}
	}()

	logger.Info("GC Worker running",
		zap.Duration("scan_interval", cfg.GC.ScanInterval),
		zap.Duration("grace_period", cfg.GC.GracePeriod),
	)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down GC Worker...")
	cancel()
	logger.Info("GC Worker stopped")
}
