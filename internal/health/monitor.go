// Package health provides storage node health monitoring for the DFMS cluster.
// It periodically probes each registered MinIO endpoint with read/write tests
// and publishes health status changes to Kafka.
package health

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/zap"

	"github.com/AnirudhSinghRajora/DFMS/internal/config"
	"github.com/AnirudhSinghRajora/DFMS/internal/events"
)

// Monitor periodically checks storage node health by probing MinIO endpoints.
type Monitor struct {
	pool     *pgxpool.Pool
	producer *events.Producer
	cfg      config.HealthMonitorConfig
	minioCfg *config.MinIOConfig
	logger   *zap.Logger
}

// NewMonitor creates a new health monitor.
func NewMonitor(
	pool *pgxpool.Pool,
	producer *events.Producer,
	cfg config.HealthMonitorConfig,
	minioCfg *config.MinIOConfig,
	logger *zap.Logger,
) *Monitor {
	return &Monitor{
		pool:     pool,
		producer: producer,
		cfg:      cfg,
		minioCfg: minioCfg,
		logger:   logger,
	}
}

// nodeRecord represents a storage node row from the database.
type nodeRecord struct {
	ID       string
	Name     string
	Endpoint string
	Status   string
}

// Run starts the periodic health check loop. It blocks until the context
// is cancelled. Each tick probes all registered storage nodes.
func (m *Monitor) Run(ctx context.Context) error {
	ticker := time.NewTicker(m.cfg.CheckInterval)
	defer ticker.Stop()

	m.logger.Info("Health monitor started",
		zap.Duration("interval", m.cfg.CheckInterval),
	)

	// Run once immediately on startup
	m.checkAllNodes(ctx)

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Health monitor shutting down")
			return nil
		case <-ticker.C:
			m.checkAllNodes(ctx)
		}
	}
}

// checkAllNodes loads nodes from DB and probes each one.
func (m *Monitor) checkAllNodes(ctx context.Context) {
	rows, err := m.pool.Query(ctx,
		`SELECT id, name, endpoint, status FROM storage_nodes`)
	if err != nil {
		m.logger.Error("Failed to load storage nodes", zap.Error(err))
		return
	}
	defer rows.Close()

	var nodes []nodeRecord
	for rows.Next() {
		var n nodeRecord
		if err := rows.Scan(&n.ID, &n.Name, &n.Endpoint, &n.Status); err != nil {
			m.logger.Error("Failed to scan node", zap.Error(err))
			continue
		}
		nodes = append(nodes, n)
	}

	for _, node := range nodes {
		m.checkNode(ctx, node)
	}
}

// checkNode performs a health probe against a single storage node.
// It tests: connectivity, write, read, and cleanup.
func (m *Monitor) checkNode(ctx context.Context, node nodeRecord) {
	probeCtx, cancel := context.WithTimeout(ctx, m.cfg.Timeout)
	defer cancel()

	start := time.Now()
	newStatus := "healthy"

	// Create a MinIO client for this node's endpoint
	client, err := minio.New(node.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(m.minioCfg.AccessKey, m.minioCfg.SecretKey, ""),
		Secure: m.minioCfg.UseSSL,
	})
	if err != nil {
		newStatus = "offline"
		m.logger.Warn("Failed to create MinIO client for node",
			zap.String("node", node.Name), zap.Error(err))
	}

	if newStatus == "healthy" {
		// Test 1: Bucket exists (connectivity)
		_, bucketErr := client.BucketExists(probeCtx, m.minioCfg.ChunkBucket)
		if bucketErr != nil {
			newStatus = "offline"
		}
	}

	if newStatus == "healthy" {
		// Test 2: Write a small probe object
		probeKey := fmt.Sprintf("_health_probe/%s", node.ID)
		probeData := []byte("health-check-probe")
		_, putErr := client.PutObject(probeCtx, m.minioCfg.TempBucket, probeKey,
			bytes.NewReader(probeData), int64(len(probeData)),
			minio.PutObjectOptions{ContentType: "text/plain"})
		if putErr != nil {
			newStatus = "degraded"
		} else {
			// Test 3: Read it back
			obj, getErr := client.GetObject(probeCtx, m.minioCfg.TempBucket, probeKey, minio.GetObjectOptions{})
			if getErr != nil {
				newStatus = "degraded"
			} else {
				obj.Close()
			}
			// Test 4: Cleanup
			_ = client.RemoveObject(probeCtx, m.minioCfg.TempBucket, probeKey, minio.RemoveObjectOptions{})
		}
	}

	latency := time.Since(start)

	// Update database
	_, err = m.pool.Exec(ctx,
		`UPDATE storage_nodes SET status = $1, last_heartbeat = NOW() WHERE id = $2`,
		newStatus, node.ID,
	)
	if err != nil {
		m.logger.Error("Failed to update node status", zap.String("node", node.Name), zap.Error(err))
	}

	// Publish event if status changed
	if newStatus != node.Status {
		m.logger.Warn("Node status changed",
			zap.String("node", node.Name),
			zap.String("old", node.Status),
			zap.String("new", newStatus),
			zap.Duration("latency", latency),
		)

		_ = m.producer.Publish(ctx, events.TopicNodesHealth, node.ID, "",
			events.NodeHealthEvent{
				NodeID:    node.ID,
				NodeName:  node.Name,
				OldStatus: node.Status,
				NewStatus: newStatus,
				Endpoint:  node.Endpoint,
				LatencyMs: latency.Milliseconds(),
			},
		)
	} else {
		m.logger.Debug("Node healthy",
			zap.String("node", node.Name),
			zap.Duration("latency", latency),
		)
	}
}
