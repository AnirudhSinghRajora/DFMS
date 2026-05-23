// DFMS API Gateway — the main HTTP entry point for all client requests.
// Handles authentication, rate limiting, request routing, and health checks.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/AnirudhSinghRajora/DFMS/api/proto/chunkpb"
	"github.com/AnirudhSinghRajora/DFMS/internal/auth"
	"github.com/AnirudhSinghRajora/DFMS/internal/cache"
	"github.com/AnirudhSinghRajora/DFMS/internal/config"
	"github.com/AnirudhSinghRajora/DFMS/internal/database"
	"github.com/AnirudhSinghRajora/DFMS/internal/events"
	"github.com/AnirudhSinghRajora/DFMS/internal/metadata"
	"github.com/AnirudhSinghRajora/DFMS/internal/observability"
	"github.com/AnirudhSinghRajora/DFMS/internal/ratelimit"
	"github.com/AnirudhSinghRajora/DFMS/internal/storage"
	apierrors "github.com/AnirudhSinghRajora/DFMS/pkg/errors"
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
	logger, err := observability.NewLogger(cfg.Server.Mode, "api-gateway")
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("Starting DFMS API Gateway",
		zap.String("mode", cfg.Server.Mode),
		zap.Int("http_port", cfg.Server.HTTPPort),
	)

	// ── Initialize Tracing ─────────────────────────────
	tracerShutdown, err := observability.InitTracer(
		context.Background(), "api-gateway", cfg.Server.Mode,
		os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	)
	if err != nil {
		logger.Warn("Failed to initialize tracing (continuing without)", zap.Error(err))
	} else {
		defer func() { _ = tracerShutdown(context.Background()) }()
		logger.Info("OpenTelemetry tracing initialized")
	}

	// ── Initialize Database ─────────────────────────────────
	ctx := context.Background()
	dbPool, err := database.NewPool(ctx, cfg.Database)
	if err != nil {
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}
	defer dbPool.Close()
	logger.Info("Database connected", zap.String("host", cfg.Database.Host))

	// ── Initialize Redis ────────────────────────────────────
	redisClient, err := cache.NewClient(cfg.Redis)
	if err != nil {
		logger.Fatal("Failed to connect to Redis", zap.Error(err))
	}
	defer func() { _ = redisClient.Close() }()
	logger.Info("Redis connected", zap.String("addr", cfg.Redis.Addr))

	// ── Initialize JWT Service ──────────────────────────────
	jwtService, err := auth.NewJWTService(cfg.JWT)
	if err != nil {
		logger.Fatal("Failed to initialize JWT service", zap.Error(err))
	}
	logger.Info("JWT service initialized", zap.String("issuer", cfg.JWT.Issuer))

	// ── Initialize gRPC Client to ChunkService ──────────────
	chunkAddr := fmt.Sprintf("localhost:%d", cfg.Server.GRPCPort)
	chunkConn, err := grpc.NewClient(chunkAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		logger.Fatal("Failed to connect to ChunkService", zap.Error(err))
	}
	defer func() { _ = chunkConn.Close() }()
	chunkClient := pb.NewChunkServiceClient(chunkConn)
	logger.Info("ChunkService gRPC client initialized", zap.String("addr", chunkAddr))

	// ── Initialize Kafka Producer ───────────────────────────
	producer := events.NewProducer(cfg.Kafka.Brokers, logger)
	defer func() { _ = producer.Close() }()

	// ── Initialize Metadata Service ─────────────────────────
	repo := metadata.NewPgxRepository(dbPool)
	fileSvc := metadata.NewFileService(repo, redisClient, chunkClient, producer, logger)

	// ── Initialize Multipart Upload Service ─────────────────
	minioClient, err := storage.NewMinIOClient(cfg.MinIO)
	if err != nil {
		logger.Fatal("Failed to connect to MinIO for multipart", zap.Error(err))
	}
	tempStore := storage.NewMinIOStore(minioClient, cfg.MinIO.TempBucket)
	mainStore := storage.NewMinIOStore(minioClient, cfg.MinIO.ChunkBucket)
	multipartSvc := metadata.NewMultipartService(redisClient, mainStore, tempStore, fileSvc, logger)

	// ── Initialize Rate Limiter ─────────────────────────────
	limiter := ratelimit.NewLimiter(redisClient.Redis())

	// ── Setup Gin Router ────────────────────────────────────
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	// Global middleware
	router.Use(
		requestIDMiddleware(),
		ginLoggerMiddleware(logger),
		gin.Recovery(),
		corsMiddleware(),
		otelgin.Middleware("api-gateway"),
		observability.GinMetricsMiddleware(),
		ratelimit.GlobalRateLimitMiddleware(limiter, cfg.RateLimit.GlobalRPM),
	)

	// ── Health & Metrics Endpoints ───────────────────────
	router.GET("/health", healthHandler())
	router.GET("/ready", readinessHandler(dbPool, redisClient))
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// ── Public Routes (no auth) ─────────────────────────────
	public := router.Group("/api/v1")
	public.Use(ratelimit.EndpointRateLimitMiddleware(limiter, cfg.RateLimit.AuthRPM))
	{
		public.POST("/auth/register", registerHandler(dbPool, jwtService, cfg))
		public.POST("/auth/login", loginHandler(dbPool, jwtService))
		public.POST("/auth/refresh", refreshHandler(jwtService))
	}

	// ── Protected Routes (JWT required) ─────────────────────
	protected := router.Group("/api/v1")
	protected.Use(auth.JWTAuthMiddleware(jwtService))
	protected.Use(ratelimit.UserRateLimitMiddleware(limiter, cfg.RateLimit.UserRPM))
	{
		// File operations
		protected.POST("/files/upload", uploadHandler(fileSvc, cfg))
		protected.GET("/files", listFilesHandler(fileSvc))
		protected.GET("/files/:id", getFileHandler(fileSvc))
		protected.GET("/files/:id/download", downloadHandler(fileSvc, chunkClient))
		protected.DELETE("/files/:id", deleteFileHandler(fileSvc))

		// File versioning
		protected.GET("/files/:id/versions", listVersionsHandler(fileSvc))
		protected.GET("/files/:id/versions/:version/download", downloadVersionHandler(fileSvc, chunkClient))
		protected.DELETE("/files/:id/versions/:version", deleteVersionHandler(fileSvc))

		// Multipart upload
		protected.POST("/files/upload/multipart/init", multipartInitHandler(multipartSvc))
		protected.PUT("/files/upload/multipart/:uploadId/part/:partNum", multipartUploadPartHandler(multipartSvc))
		protected.POST("/files/upload/multipart/:uploadId/complete", multipartCompleteHandler(multipartSvc))
		protected.DELETE("/files/upload/multipart/:uploadId", multipartAbortHandler(multipartSvc))

		// Folder operations
		protected.POST("/folders", createFolderHandler(fileSvc))
		protected.GET("/folders/:id/contents", folderContentsHandler(fileSvc))
		protected.PUT("/files/:id/move", moveFileHandler(fileSvc))
		protected.DELETE("/folders/:id", deleteFolderHandler(fileSvc))

		// Search
		protected.GET("/search", searchHandler(fileSvc))

		// Storage usage
		protected.GET("/storage/usage", storageUsageHandler(dbPool))
	}

	// ── Admin Routes ────────────────────────────────────────
	admin := router.Group("/api/v1/admin")
	admin.Use(auth.JWTAuthMiddleware(jwtService))
	admin.Use(auth.RequireRole("admin"))
	{
		admin.GET("/nodes", listNodesPlaceholder())
	}

	// ── Start Server with Graceful Shutdown ──────────────────
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		logger.Info("HTTP server listening", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Server failed", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Shutting down server...")

	// Give outstanding requests 10 seconds to complete
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server forced to shutdown", zap.Error(err))
	}

	logger.Info("Server stopped gracefully")
}

// ── Middleware ────────────────────────────────────────────────

// requestIDMiddleware generates a unique request ID for each request
// and sets it in the response header and gin context.
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// ginLoggerMiddleware logs each HTTP request using the structured Zap logger.
func ginLoggerMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		fields := []zap.Field{
			zap.Int("status", status),
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.Duration("latency", latency),
			zap.String("client_ip", c.ClientIP()),
			zap.String("request_id", c.GetString("request_id")),
			zap.Int("body_size", c.Writer.Size()),
		}

		if status >= 500 {
			logger.Error("Request completed with server error", fields...)
		} else if status >= 400 {
			logger.Warn("Request completed with client error", fields...)
		} else {
			logger.Info("Request completed", fields...)
		}
	}
}

// corsMiddleware configures Cross-Origin Resource Sharing headers.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, X-Request-ID")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// ── Handlers ─────────────────────────────────────────────────

func healthHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "api-gateway",
			"version": observability.AppVersion,
		})
	}
}

func readinessHandler(db *pgxpool.Pool, redis *cache.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		checks := gin.H{}
		ready := true

		// Check database
		if err := database.HealthCheck(c.Request.Context(), db); err != nil {
			checks["database"] = "unhealthy"
			ready = false
		} else {
			checks["database"] = "healthy"
		}

		// Check Redis
		if err := redis.HealthCheck(c.Request.Context()); err != nil {
			checks["redis"] = "unhealthy"
			ready = false
		} else {
			checks["redis"] = "healthy"
		}

		status := http.StatusOK
		if !ready {
			status = http.StatusServiceUnavailable
		}

		c.JSON(status, gin.H{
			"status": map[bool]string{true: "ready", false: "not_ready"}[ready],
			"checks": checks,
		})
	}
}

// ── Auth Handlers ────────────────────────────────────────────

type registerRequest struct {
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=8,max=72"`
	DisplayName string `json:"display_name" binding:"required,min=1,max=100"`
}

func registerHandler(db *pgxpool.Pool, jwtService *auth.JWTService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req registerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apiErr := apierrors.NewBadRequest("Invalid input: " + err.Error())
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		// Hash password
		hashedPassword, err := auth.HashPassword(req.Password)
		if err != nil {
			apiErr := apierrors.NewBadRequest(err.Error())
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		// Insert user
		var userID string
		err = db.QueryRow(c.Request.Context(),
			`INSERT INTO users (email, password_hash, display_name, storage_quota)
			 VALUES ($1, $2, $3, $4)
			 RETURNING id`,
			req.Email, hashedPassword, req.DisplayName, cfg.Storage.DefaultQuota,
		).Scan(&userID)

		if err != nil {
			// Check for unique constraint violation
			if isDuplicateKeyError(err) {
				apiErr := apierrors.NewConflict(apierrors.CodeAuthUserExists, "A user with this email already exists")
				c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
				return
			}
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		// Generate token pair
		tokens, err := jwtService.GenerateTokenPair(userID, req.Email, "user")
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"user": gin.H{
				"id":           userID,
				"email":        req.Email,
				"display_name": req.DisplayName,
			},
			"tokens": tokens,
		})
	}
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func loginHandler(db *pgxpool.Pool, jwtService *auth.JWTService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req loginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apiErr := apierrors.NewBadRequest("Invalid input: " + err.Error())
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		// Fetch user by email
		var userID, passwordHash, displayName, role string
		var isActive bool
		err := db.QueryRow(c.Request.Context(),
			`SELECT id, password_hash, display_name, role, is_active
			 FROM users WHERE email = $1`,
			req.Email,
		).Scan(&userID, &passwordHash, &displayName, &role, &isActive)

		if err != nil {
			apiErr := apierrors.NewUnauthorized(apierrors.CodeAuthInvalidCredentials, "Invalid email or password")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		if !isActive {
			apiErr := apierrors.NewUnauthorized(apierrors.CodeAuthInvalidCredentials, "Account is deactivated")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		// Verify password
		if err := auth.VerifyPassword(passwordHash, req.Password); err != nil {
			apiErr := apierrors.NewUnauthorized(apierrors.CodeAuthInvalidCredentials, "Invalid email or password")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		// Generate token pair
		tokens, err := jwtService.GenerateTokenPair(userID, req.Email, role)
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"user": gin.H{
				"id":           userID,
				"email":        req.Email,
				"display_name": displayName,
				"role":         role,
			},
			"tokens": tokens,
		})
	}
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

func refreshHandler(jwtService *auth.JWTService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req refreshRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apiErr := apierrors.NewBadRequest("refresh_token is required")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		// Validate refresh token
		claims, err := jwtService.ValidateRefreshToken(req.RefreshToken)
		if err != nil {
			apiErr := apierrors.NewUnauthorized(apierrors.CodeAuthTokenInvalid, "Invalid or expired refresh token")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		// Generate new token pair (refresh token rotation)
		tokens, err := jwtService.GenerateTokenPair(claims.UserID, "", "user")
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"tokens": tokens,
		})
	}
}

func storageUsageHandler(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := auth.GetUserID(c)
		if userID == "" {
			apiErr := apierrors.NewUnauthorized(apierrors.CodeAuthTokenInvalid, "User not found in context")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		var storageUsed, storageQuota int64
		err := db.QueryRow(c.Request.Context(),
			`SELECT storage_used, storage_quota FROM users WHERE id = $1`,
			userID,
		).Scan(&storageUsed, &storageQuota)

		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"used":       storageUsed,
			"quota":      storageQuota,
			"available":  storageQuota - storageUsed,
			"used_pct":   float64(storageUsed) / float64(storageQuota) * 100,
		})
	}
}

// ── File Handlers ────────────────────────────────────────────

func uploadHandler(fileSvc *metadata.FileService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := auth.GetUserID(c)
		file, header, err := c.Request.FormFile("file")
		if err != nil {
			apiErr := apierrors.NewBadRequest("file field is required")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}
		defer file.Close()

		if header.Size > cfg.Storage.MaxUploadSize {
			apiErr := apierrors.NewTooLarge(fmt.Sprintf("file exceeds max size of %d bytes", cfg.Storage.MaxUploadSize))
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		mimeType := header.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		resp, err := fileSvc.Upload(c.Request.Context(), userID, header.Filename, mimeType, file)
		if err != nil {
			if contains(err.Error(), "QUOTA_EXCEEDED") {
				apiErr := apierrors.NewTooLarge(err.Error())
				c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
				return
			}
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.JSON(http.StatusCreated, gin.H{"file": resp})
	}
}

func listFilesHandler(fileSvc *metadata.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := auth.GetUserID(c)
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
		if page < 1 { page = 1 }
		if pageSize < 1 || pageSize > 100 { pageSize = 20 }

		resp, err := fileSvc.ListFiles(c.Request.Context(), userID, page, pageSize)
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}

func getFileHandler(fileSvc *metadata.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := auth.GetUserID(c)
		fileID := c.Param("id")

		file, err := fileSvc.GetFile(c.Request.Context(), userID, fileID)
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}
		if file == nil {
			apiErr := apierrors.NewNotFound(apierrors.CodeFileNotFound, "File not found")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}
		c.JSON(http.StatusOK, gin.H{"file": file})
	}
}

func downloadHandler(fileSvc *metadata.FileService, chunkClient pb.ChunkServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := auth.GetUserID(c)
		fileID := c.Param("id")

		result, err := fileSvc.PrepareDownload(c.Request.Context(), userID, fileID)
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}
		if result == nil {
			apiErr := apierrors.NewNotFound(apierrors.CodeFileNotFound, "File not found")
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		// Always advertise range support
		c.Header("Accept-Ranges", "bytes")
		if result.MimeType != "" {
			c.Header("Content-Type", result.MimeType)
		} else {
			c.Header("Content-Type", "application/octet-stream")
		}
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, result.FileName))

		// Check for Range header
		rangeHeader := c.GetHeader("Range")
		if rangeHeader != "" {
			// Parse "bytes=start-end"
			rangeStart, rangeEnd, ok := parseRangeHeader(rangeHeader, result.Size)
			if !ok {
				c.Header("Content-Range", fmt.Sprintf("bytes */%d", result.Size))
				c.Status(http.StatusRequestedRangeNotSatisfiable)
				return
			}

			rangeResult, err := fileSvc.PrepareRangeDownload(c.Request.Context(), userID, fileID, rangeStart, rangeEnd)
			if err != nil {
				apiErr := apierrors.NewInternal(err)
				c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
				return
			}

			c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rangeResult.RangeStart, rangeResult.RangeEnd, rangeResult.FileSize))
			c.Header("Content-Length", strconv.FormatInt(rangeResult.ContentLen, 10))
			c.Status(http.StatusPartialContent)

			// Stream only the range chunks
			for _, chunk := range rangeResult.ChunkPlan {
			stream, err := chunkClient.DownloadFile(c.Request.Context(), &pb.DownloadFileRequest{
					ChunkHashes: []string{chunk.Hash},
				})
				if err != nil {
					break
				}

				var bytesRead int64
				for {
					resp, err := stream.Recv()
					if err != nil {
						break
					}

					data := resp.Data
					// Skip bytes at start
					if bytesRead < chunk.SkipBytes {
						skip := chunk.SkipBytes - bytesRead
						if skip >= int64(len(data)) {
							bytesRead += int64(len(data))
							continue
						}
						data = data[skip:]
						bytesRead += skip
					}

					// Truncate if we'd exceed ReadBytes
					remaining := chunk.ReadBytes - (bytesRead - chunk.SkipBytes)
					if int64(len(data)) > remaining {
						data = data[:remaining]
					}

					if _, writeErr := c.Writer.Write(data); writeErr != nil {
						return
					}
					bytesRead += int64(len(data))

					if bytesRead-chunk.SkipBytes >= chunk.ReadBytes {
						break
					}
				}
			}
			c.Writer.Flush()
			return
		}

		// Full download (no Range header)
		c.Header("Content-Length", strconv.FormatInt(result.Size, 10))

		stream, err := chunkClient.DownloadFile(c.Request.Context(), &pb.DownloadFileRequest{
			ChunkHashes: result.ChunkHashes,
		})
		if err != nil {
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.Status(http.StatusOK)
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			if _, writeErr := c.Writer.Write(resp.Data); writeErr != nil {
				break
			}
			c.Writer.Flush()
		}
	}
}

// parseRangeHeader parses an HTTP Range header value like "bytes=0-1023".
// Returns the start, end (inclusive), and whether parsing succeeded.
func parseRangeHeader(header string, fileSize int64) (int64, int64, bool) {
	// Must start with "bytes="
	if len(header) < 7 || header[:6] != "bytes=" {
		return 0, 0, false
	}

	rangeSpec := header[6:]

	// Find the dash separator
	dashIdx := -1
	for i, ch := range rangeSpec {
		if ch == '-' {
			dashIdx = i
			break
		}
	}
	if dashIdx == -1 {
		return 0, 0, false
	}

	startStr := rangeSpec[:dashIdx]
	endStr := rangeSpec[dashIdx+1:]

	var start, end int64

	if startStr == "" {
		// Suffix range: "-500" means last 500 bytes
		suffixLen, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || suffixLen <= 0 {
			return 0, 0, false
		}
		start = fileSize - suffixLen
		end = fileSize - 1
		if start < 0 {
			start = 0
		}
	} else {
		var err error
		start, err = strconv.ParseInt(startStr, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		if endStr == "" {
			end = fileSize - 1 // "0-" means 0 to end
		} else {
			end, err = strconv.ParseInt(endStr, 10, 64)
			if err != nil {
				return 0, 0, false
			}
		}
	}

	if start < 0 || start >= fileSize || start > end {
		return 0, 0, false
	}
	if end >= fileSize {
		end = fileSize - 1
	}

	return start, end, true
}

func deleteFileHandler(fileSvc *metadata.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := auth.GetUserID(c)
		fileID := c.Param("id")

		if err := fileSvc.DeleteFile(c.Request.Context(), userID, fileID); err != nil {
			if contains(err.Error(), "not found") {
				apiErr := apierrors.NewNotFound(apierrors.CodeFileNotFound, "File not found")
				c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
				return
			}
			apiErr := apierrors.NewInternal(err)
			c.JSON(apiErr.StatusCode, apiErr.ToResponse(c.GetString("request_id")))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "File deleted successfully"})
	}
}

func listNodesPlaceholder() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"nodes": []interface{}{}, "message": "Node listing will be implemented in Phase 5"})
	}
}

// isDuplicateKeyError checks if a PostgreSQL error is a unique constraint violation.
func isDuplicateKeyError(err error) bool {
	return err != nil && (
		contains(err.Error(), "23505") ||
		contains(err.Error(), "duplicate key"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
