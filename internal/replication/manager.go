package replication

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"github.com/AnirudhSinghRajora/DFMS/internal/events"
	"github.com/AnirudhSinghRajora/DFMS/internal/storage"
)

const (
	// maxReplicationWorkers limits concurrent replication tasks to prevent
	// overwhelming MinIO with parallel uploads.
	maxReplicationWorkers = 10
)

// Manager consumes chunk.created events from Kafka and replicates
// chunks across storage nodes according to the consistent hash ring.
type Manager struct {
	ring       *Ring
	store      storage.ObjectStore
	pool       *pgxpool.Pool
	producer   *events.Producer
	logger     *zap.Logger
	replFactor int
}

// NewManager creates a new replication manager.
func NewManager(
	ring *Ring,
	store storage.ObjectStore,
	pool *pgxpool.Pool,
	producer *events.Producer,
	replFactor int,
	logger *zap.Logger,
) *Manager {
	return &Manager{
		ring:       ring,
		store:      store,
		pool:       pool,
		producer:   producer,
		replFactor: replFactor,
		logger:     logger,
	}
}

// HandleChunkCreated processes a chunk.created Kafka message.
// It determines replica placement via the hash ring and copies the chunk
// to each target node. In dev (single MinIO), this updates the
// storage_nodes metadata in PostgreSQL to simulate multi-node placement.
func (m *Manager) HandleChunkCreated(ctx context.Context, msg kafka.Message) error { //nolint:gocritic // msg must be by-value to satisfy events.MessageHandler interface
	// Deserialize the event envelope
	var envelope events.Envelope
	if err := json.Unmarshal(msg.Value, &envelope); err != nil {
		m.logger.Error("Failed to unmarshal envelope", zap.Error(err))
		return nil // Don't retry malformed messages
	}

	var event events.ChunkCreatedEvent
	if err := json.Unmarshal(envelope.Payload, &event); err != nil {
		m.logger.Error("Failed to unmarshal chunk created event", zap.Error(err))
		return nil
	}

	m.logger.Info("Processing chunk replication",
		zap.String("hash", event.ChunkHash[:12]),
		zap.Int64("size", event.ChunkSize),
		zap.String("trace_id", envelope.TraceID),
	)

	// Get target nodes from hash ring
	targetNodes := m.ring.GetNodes(event.ChunkHash, m.replFactor)
	if len(targetNodes) == 0 {
		m.logger.Warn("No healthy nodes available for replication",
			zap.String("hash", event.ChunkHash[:12]),
		)
		return fmt.Errorf("no healthy nodes for replication")
	}

	// Track which nodes this chunk is replicated to
	var (
		placedNodes []string
		mu          sync.Mutex
		wg          sync.WaitGroup
		sem         = make(chan struct{}, maxReplicationWorkers)
	)

	// The primary copy is already on MinIO from the upload.
	// We verify it exists and record the placement.
	exists, err := m.store.ChunkExists(ctx, event.ChunkHash)
	if err != nil || !exists {
		m.logger.Error("Primary chunk not found in MinIO",
			zap.String("hash", event.ChunkHash[:12]),
			zap.Error(err),
		)
		return fmt.Errorf("primary chunk missing: %s", event.ChunkHash[:12])
	}

	// In a multi-node production setup, we'd copy the chunk to each
	// replica's MinIO endpoint. In our single-MinIO dev setup, we simulate
	// this by recording the target nodes in the DB.
	for _, node := range targetNodes {
		wg.Add(1)
		sem <- struct{}{} // Acquire worker slot

		go func(n Node) {
			defer wg.Done()
			defer func() { <-sem }()

			// In production: download from primary MinIO → upload to n.Endpoint
			// In dev: the chunk already exists in the shared MinIO, just track placement
			mu.Lock()
			placedNodes = append(placedNodes, n.ID)
			mu.Unlock()

			m.logger.Debug("Chunk placed on node",
				zap.String("hash", event.ChunkHash[:12]),
				zap.String("node", n.Name),
			)
		}(node)
	}

	wg.Wait()

	// Update storage_nodes array in PostgreSQL
	if len(placedNodes) > 0 {
		_, err := m.pool.Exec(ctx,
			`UPDATE chunks SET storage_nodes = $1 WHERE hash = $2`,
			placedNodes, event.ChunkHash,
		)
		if err != nil {
			m.logger.Error("Failed to update chunk placement",
				zap.String("hash", event.ChunkHash[:12]),
				zap.Error(err),
			)
			return fmt.Errorf("update placement: %w", err)
		}
	}

	m.logger.Info("Chunk replication completed",
		zap.String("hash", event.ChunkHash[:12]),
		zap.Int("replicas", len(placedNodes)),
		zap.Strings("nodes", placedNodes),
	)

	return nil
}

// HandleNodeHealthChanged processes a node.health.changed event.
// When a node goes offline, it identifies all chunks on that node
// and triggers re-replication to maintain the replication factor.
func (m *Manager) HandleNodeHealthChanged(ctx context.Context, msg kafka.Message) error { //nolint:gocritic // msg must be by-value to satisfy events.MessageHandler interface
	var envelope events.Envelope
	if err := json.Unmarshal(msg.Value, &envelope); err != nil {
		return nil
	}

	var event events.NodeHealthEvent
	if err := json.Unmarshal(envelope.Payload, &event); err != nil {
		return nil
	}

	m.logger.Info("Node health changed",
		zap.String("node", event.NodeName),
		zap.String("old", event.OldStatus),
		zap.String("new", event.NewStatus),
	)

	// Update the hash ring
	m.ring.UpdateNodeStatus(event.NodeID, event.NewStatus)

	// If a node went offline, find all chunks that were on it
	// and schedule re-replication
	if event.NewStatus == "offline" {
		rows, err := m.pool.Query(ctx,
			`SELECT hash, size FROM chunks
			 WHERE $1 = ANY(storage_nodes) AND status = 'active'`,
			event.NodeID,
		)
		if err != nil {
			return fmt.Errorf("query chunks on failed node: %w", err)
		}
		defer rows.Close()

		var reReplCount int
		for rows.Next() {
			var hash string
			var size int64
			if err := rows.Scan(&hash, &size); err != nil {
				continue
			}

			// Remove the dead node from the chunk's placement
			_, _ = m.pool.Exec(ctx,
				`UPDATE chunks SET storage_nodes = array_remove(storage_nodes, $1) WHERE hash = $2`,
				event.NodeID, hash,
			)

			// Publish a synthetic chunk.created event to trigger re-replication
			_ = m.producer.Publish(ctx, events.TopicChunksCreated, hash, envelope.TraceID,
				events.ChunkCreatedEvent{
					ChunkHash: hash,
					ChunkSize: size,
					Bucket:    "chunks",
				},
			)
			reReplCount++
		}

		m.logger.Info("Scheduled re-replication for dead node",
			zap.String("node", event.NodeName),
			zap.Int("chunks", reReplCount),
		)
	}

	return nil
}

// LoadNodesFromDB loads storage nodes from the database and initializes
// or updates the hash ring.
func LoadNodesFromDB(ctx context.Context, pool *pgxpool.Pool) ([]Node, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, name, endpoint, weight, status FROM storage_nodes ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query storage nodes: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Name, &n.Endpoint, &n.Weight, &n.Status); err != nil {
			return nil, fmt.Errorf("scan storage node: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}
