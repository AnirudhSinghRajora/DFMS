// DFMS Replication Manager — consumes Kafka events to replicate chunks
// across storage nodes. Uses consistent hashing for placement decisions
// and bounded concurrency for parallel replication tasks.
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
	"github.com/AnirudhSinghRajora/DFMS/internal/observability"
	"github.com/AnirudhSinghRajora/DFMS/internal/replication"
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

	logger, err := observability.NewLogger(cfg.Server.Mode, "replication-manager")
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("Starting DFMS Replication Manager")

	// Database
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := database.NewPool(ctx, cfg.Database)
	if err != nil {
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}
	defer pool.Close()

	// MinIO
	minioClient, err := storage.NewMinIOClient(cfg.MinIO)
	if err != nil {
		logger.Fatal("Failed to connect to MinIO", zap.Error(err))
	}
	store := storage.NewMinIOStore(minioClient, cfg.MinIO.ChunkBucket)

	// Kafka producer (for re-replication events)
	producer := events.NewProducer(cfg.Kafka.Brokers, logger)
	defer func() { _ = producer.Close() }()

	// Load storage nodes and build hash ring
	nodes, err := replication.LoadNodesFromDB(ctx, pool)
	if err != nil {
		logger.Fatal("Failed to load storage nodes", zap.Error(err))
	}
	ring := replication.NewRing(nodes, cfg.Replication.VirtualNodes)
	logger.Info("Hash ring initialized",
		zap.Int("nodes", ring.NodeCount()),
		zap.Int("virtual_nodes", cfg.Replication.VirtualNodes),
	)

	// Replication manager
	mgr := replication.NewManager(ring, store, pool, producer, cfg.Replication.Factor, logger)

	// Kafka consumer for chunk.created events
	chunkConsumer := events.NewConsumer(
		cfg.Kafka.Brokers,
		"dfms-replication-group",
		events.TopicChunksCreated,
		logger,
	)
	defer func() { _ = chunkConsumer.Close() }()

	// Kafka consumer for node.health events
	healthConsumer := events.NewConsumer(
		cfg.Kafka.Brokers,
		"dfms-replication-group",
		events.TopicNodesHealth,
		logger,
	)
	defer func() { _ = healthConsumer.Close() }()

	// Start consumers in goroutines
	go func() {
		if err := chunkConsumer.Subscribe(ctx, mgr.HandleChunkCreated); err != nil {
			logger.Error("Chunk consumer exited", zap.Error(err))
		}
	}()

	go func() {
		if err := healthConsumer.Subscribe(ctx, mgr.HandleNodeHealthChanged); err != nil {
			logger.Error("Health consumer exited", zap.Error(err))
		}
	}()

	logger.Info("Replication Manager running")

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down Replication Manager...")
	cancel()
	logger.Info("Replication Manager stopped")
}
