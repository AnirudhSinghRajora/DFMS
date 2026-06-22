// DFMS Chunk Service — gRPC server for file chunking, deduplication,
// and MinIO storage operations. This is the data-plane service: it handles
// raw bytes while the API Gateway handles metadata and auth.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "github.com/AnirudhSinghRajora/DFMS/api/proto/chunkpb"
	"github.com/AnirudhSinghRajora/DFMS/internal/chunking"
	"github.com/AnirudhSinghRajora/DFMS/internal/config"
	"github.com/AnirudhSinghRajora/DFMS/internal/observability"
	"github.com/AnirudhSinghRajora/DFMS/internal/storage"
)

func main() {
	// ── Load Configuration ──────────────────────────────────
	configPath := os.Getenv("DFMS_CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.dev.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// ── Initialize Logger ───────────────────────────────────
	logger, err := observability.NewLogger(cfg.Server.Mode, "chunk-service")
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("Starting DFMS Chunk Service",
		zap.String("mode", cfg.Server.Mode),
		zap.Int("grpc_port", cfg.Server.GRPCPort),
	)

	// ── Initialize Tracing ──────────────────────────────────
	tracerShutdown, err := observability.InitTracer(
		context.Background(), "chunk-service", cfg.Server.Mode,
		os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	)
	if err != nil {
		logger.Warn("Failed to initialize tracing (continuing without)", zap.Error(err))
	} else {
		defer func() { _ = tracerShutdown(context.Background()) }()
		logger.Info("OpenTelemetry tracing initialized")
	}

	// ── Initialize MinIO ────────────────────────────────────
	minioClient, err := storage.NewMinIOClient(&cfg.MinIO)
	if err != nil {
		logger.Fatal("Failed to connect to MinIO", zap.Error(err))
	}
	store := storage.NewMinIOStore(minioClient, cfg.MinIO.ChunkBucket)
	logger.Info("MinIO connected", zap.String("endpoint", cfg.MinIO.Endpoints[0]))

	// ── Initialize Chunk Server ─────────────────────────────
	cdcCfg := chunking.CDCConfig{
		MinSize: cfg.Chunking.MinSize,
		AvgSize: cfg.Chunking.AvgSize,
		MaxSize: cfg.Chunking.MaxSize,
	}

	chunkServer, err := chunking.NewChunkServer(store, cdcCfg, logger)
	if err != nil {
		logger.Fatal("Failed to create chunk server", zap.Error(err))
	}

	// ── Start gRPC Server ───────────────────────────────────
	addr := fmt.Sprintf(":%d", cfg.Server.GRPCPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Fatal("Failed to listen", zap.String("addr", addr), zap.Error(err))
	}

	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(8*1024*1024), // 8MB max message
		grpc.MaxSendMsgSize(8*1024*1024),
		grpc.StatsHandler(otelgrpc.NewServerHandler()), // OTel tracing
	)

	pb.RegisterChunkServiceServer(grpcServer, chunkServer)

	// Enable gRPC reflection for debugging with grpcurl
	reflection.Register(grpcServer)

	// Start serving in a goroutine
	go func() {
		logger.Info("gRPC server listening", zap.String("addr", addr))
		if err := grpcServer.Serve(lis); err != nil {
			logger.Fatal("gRPC server failed", zap.Error(err))
		}
	}()

	// ── Start Metrics HTTP Sidecar ──────────────────────────
	// gRPC can't serve Prometheus metrics on the same port, so we run
	// a lightweight HTTP server on port gRPC+100 (e.g., 9191) for /metrics.
	// It also exposes /health so container orchestrators can probe liveness
	// over HTTP without needing a gRPC health-check client.
	metricsAddr := fmt.Sprintf(":%d", cfg.Server.GRPCPort+100)
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsMux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"chunk-service"}`))
	})
	go func() {
		logger.Info("Metrics HTTP server listening", zap.String("addr", metricsAddr))
		if err := http.ListenAndServe(metricsAddr, metricsMux); err != nil {
			logger.Error("Metrics server failed", zap.Error(err))
		}
	}()

	// ── Graceful Shutdown ───────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down Chunk Service...")
	grpcServer.GracefulStop()
	logger.Info("Chunk Service stopped gracefully")
}
