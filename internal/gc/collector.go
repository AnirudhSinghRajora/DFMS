// Package gc implements two-phase garbage collection for orphaned chunks.
// Phase 1 (mark): Identifies chunks with ref_count = 0 and marks them pending_gc.
// Phase 2 (sweep): After a grace period, re-verifies and deletes from MinIO + DB.
package gc

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/AnirudhSinghRajora/DFMS/internal/cache"
	"github.com/AnirudhSinghRajora/DFMS/internal/config"
	"github.com/AnirudhSinghRajora/DFMS/internal/events"
	"github.com/AnirudhSinghRajora/DFMS/internal/observability"
	"github.com/AnirudhSinghRajora/DFMS/internal/storage"
)

const (
	// gcLockKey is the Redis key used for distributed locking to prevent
	// multiple GC workers from running simultaneously.
	gcLockKey = "dfms:gc:lock"
	gcLockTTL = 5 * time.Minute
)

// Collector implements two-phase mark-sweep garbage collection.
type Collector struct {
	pool     *pgxpool.Pool
	store    storage.ObjectStore
	cache    *cache.Client
	producer *events.Producer
	cfg      config.GCConfig
	logger   *zap.Logger
}

// NewCollector creates a new garbage collector.
func NewCollector(
	pool *pgxpool.Pool,
	store storage.ObjectStore,
	cache *cache.Client,
	producer *events.Producer,
	cfg config.GCConfig,
	logger *zap.Logger,
) *Collector {
	return &Collector{
		pool:     pool,
		store:    store,
		cache:    cache,
		producer: producer,
		cfg:      cfg,
		logger:   logger,
	}
}

// Run starts the periodic GC loop. It blocks until the context is cancelled.
func (c *Collector) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.cfg.ScanInterval)
	defer ticker.Stop()

	c.logger.Info("GC worker started",
		zap.Duration("scan_interval", c.cfg.ScanInterval),
		zap.Duration("grace_period", c.cfg.GracePeriod),
		zap.Int("batch_size", c.cfg.BatchSize),
	)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("GC worker shutting down")
			return nil
		case <-ticker.C:
			c.runCycle(ctx)
		}
	}
}

// runCycle executes one GC cycle: acquire lock → mark → sweep → release lock.
func (c *Collector) runCycle(ctx context.Context) {
	// Distributed lock: only one GC worker runs at a time
	acquired, err := c.cache.SetNX(ctx, gcLockKey, "running", gcLockTTL)
	if err != nil {
		c.logger.Error("Failed to acquire GC lock", zap.Error(err))
		return
	}
	if !acquired {
		c.logger.Debug("GC lock held by another worker, skipping")
		return
	}
	defer func() {
		_ = c.cache.Delete(ctx, gcLockKey)
	}()

	c.logger.Info("GC cycle started")
	start := time.Now()

	marked := c.markOrphans(ctx)
	swept := c.sweepMarked(ctx)

	c.logger.Info("GC cycle completed",
		zap.Int("marked", marked),
		zap.Int("swept", swept),
		zap.Duration("duration", time.Since(start)),
	)
}

// markOrphans finds chunks with ref_count = 0 that are still active
// and marks them as pending_gc. We repurpose created_at as the "mark time"
// to track when the chunk entered the GC grace period.
func (c *Collector) markOrphans(ctx context.Context) int {
	// PostgreSQL doesn't support LIMIT in UPDATE; use a subquery.
	tag, err := c.pool.Exec(ctx,
		`UPDATE chunks SET status = 'pending_gc', created_at = NOW()
		 WHERE id IN (
			SELECT id FROM chunks
			WHERE ref_count = 0 AND status = 'active'
			LIMIT $1
		 )`, c.cfg.BatchSize)
	if err != nil {
		c.logger.Error("Failed to mark orphans", zap.Error(err))
		return 0
	}
	return int(tag.RowsAffected())
}

// sweepMarked deletes chunks that have been pending_gc for longer than
// the grace period. It re-verifies ref_count = 0 before deleting to
// prevent race conditions with concurrent uploads.
func (c *Collector) sweepMarked(ctx context.Context) int {
	graceSec := int(c.cfg.GracePeriod.Seconds())
	rows, err := c.pool.Query(ctx,
		`SELECT id, hash, size FROM chunks
		 WHERE status = 'pending_gc'
		 AND created_at < NOW() - make_interval(secs => $1)
		 LIMIT $2`,
		graceSec, c.cfg.BatchSize,
	)
	if err != nil {
		c.logger.Error("Failed to query pending_gc chunks", zap.Error(err))
		return 0
	}
	defer rows.Close()

	var swept int
	for rows.Next() {
		var id, hash string
		var size int64
		if err := rows.Scan(&id, &hash, &size); err != nil {
			continue
		}

		// Re-verify: check if something referenced this chunk during the grace period
		var refCount int
		err := c.pool.QueryRow(ctx,
			`SELECT ref_count FROM chunks WHERE id = $1`, id,
		).Scan(&refCount)
		if err != nil {
			continue
		}
		if refCount > 0 {
			// Chunk was re-referenced — resurrect it
			_, _ = c.pool.Exec(ctx,
				`UPDATE chunks SET status = 'active' WHERE id = $1`, id)
			c.logger.Info("Chunk resurrected (ref_count > 0 during grace)",
				zap.String("hash", hash[:12]),
				zap.Int("ref_count", refCount),
			)
			continue
		}

		// Delete from MinIO
		if err := c.store.DeleteChunk(ctx, hash); err != nil {
			c.logger.Error("Failed to delete chunk from MinIO",
				zap.String("hash", hash[:12]),
				zap.Error(err),
			)
			continue
		}

		// Delete FK references in file_chunks first, then the chunk row
		_, _ = c.pool.Exec(ctx,
			`DELETE FROM file_chunks WHERE chunk_id = $1`, id)
		_, err = c.pool.Exec(ctx,
			`DELETE FROM chunks WHERE id = $1 AND ref_count = 0`, id)
		if err != nil {
			c.logger.Error("Failed to delete chunk from DB",
				zap.String("hash", hash[:12]),
				zap.Error(err),
			)
			continue
		}

		// Publish deletion event
		_ = c.producer.Publish(ctx, events.TopicChunksDeleted, hash, "",
			events.ChunkDeletedEvent{
				ChunkHash: hash,
				ChunkSize: size,
			},
		)

		swept++
		observability.GCChunksCollected.Inc()
		observability.GCBytesReclaimed.Add(float64(size))
		c.logger.Info("Chunk garbage collected",
			zap.String("hash", hash[:12]),
			zap.Int64("size", size),
		)
	}

	return swept
}
