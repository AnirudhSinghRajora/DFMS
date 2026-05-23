// Package observability provides Prometheus metrics for all DFMS services.
// Metrics follow the RED method (Rate, Errors, Duration) for request-driven
// services, and the USE method (Utilization, Saturation, Errors) for resources.
//
// All metrics are registered globally via promauto and are safe for concurrent use.
package observability

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const metricsNamespace = "dfms"

// ── Counters (monotonic totals) ─────────────────────────────

var (
	// UploadsTotal counts the number of file uploads, partitioned by status.
	UploadsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "uploads_total",
		Help:      "Total number of file uploads.",
	}, []string{"status"}) // status: success, error

	// DownloadsTotal counts the number of file downloads.
	DownloadsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "downloads_total",
		Help:      "Total number of file downloads.",
	}, []string{"status"})

	// DedupHitsTotal counts chunks that were deduplicated (already existed).
	DedupHitsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "dedup_hits_total",
		Help:      "Total number of chunks deduplicated (already existed in storage).",
	})

	// DedupMissesTotal counts new chunks that were uploaded.
	DedupMissesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "dedup_misses_total",
		Help:      "Total number of new chunks uploaded (no existing copy).",
	})

	// GCChunksCollected counts chunks removed by garbage collection.
	GCChunksCollected = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "gc_chunks_collected_total",
		Help:      "Total number of chunks removed by the garbage collector.",
	})

	// GCBytesReclaimed counts bytes freed by garbage collection.
	GCBytesReclaimed = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "gc_bytes_reclaimed_total",
		Help:      "Total bytes reclaimed by garbage collection.",
	})

	// ReplicationEventsProcessed counts chunk replication events handled.
	ReplicationEventsProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "replication_events_processed_total",
		Help:      "Total number of replication events processed.",
	}, []string{"status"}) // status: success, error

	// HTTPRequestsTotal counts all HTTP requests by method, path, and status.
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "http_requests_total",
		Help:      "Total number of HTTP requests.",
	}, []string{"method", "path", "status_code"})
)

// ── Histograms (latency distributions) ──────────────────────

var (
	// UploadDuration measures end-to-end upload latency.
	UploadDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Name:      "upload_duration_seconds",
		Help:      "End-to-end upload latency in seconds.",
		Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
	})

	// DownloadDuration measures end-to-end download latency.
	DownloadDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Name:      "download_duration_seconds",
		Help:      "End-to-end download latency in seconds.",
		Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
	})

	// ChunkProcessDuration measures CDC chunking + hash time per chunk.
	ChunkProcessDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Name:      "chunk_process_duration_seconds",
		Help:      "Time to process (split + hash) a single chunk.",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	})

	// DBQueryDuration measures PostgreSQL query latency by operation.
	DBQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Name:      "db_query_duration_seconds",
		Help:      "PostgreSQL query duration in seconds.",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
	}, []string{"operation"})

	// MinIOOperationDuration measures MinIO storage operation latency.
	MinIOOperationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Name:      "minio_operation_duration_seconds",
		Help:      "MinIO storage operation duration in seconds.",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"operation"}) // operation: put, get, delete, exists

	// HTTPRequestDuration measures HTTP request latency by route.
	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request duration in seconds.",
		Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"method", "path"})
)

// ── Gauges (current state) ──────────────────────────────────

var (
	// ActiveUploads tracks the number of uploads currently in progress.
	ActiveUploads = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "active_uploads",
		Help:      "Number of file uploads currently in progress.",
	})

	// ActiveDownloads tracks the number of downloads currently in progress.
	ActiveDownloads = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "active_downloads",
		Help:      "Number of file downloads currently in progress.",
	})

	// StorageBytesUsed tracks total bytes stored in MinIO.
	StorageBytesUsed = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "storage_bytes_used",
		Help:      "Total bytes currently stored across all users.",
	})

	// MinIONodeHealthy tracks the health status of each MinIO storage node.
	MinIONodeHealthy = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "minio_node_healthy",
		Help:      "Whether a MinIO storage node is healthy (1) or unhealthy (0).",
	}, []string{"node"})

	// StorageNodesTotal tracks the total number of storage nodes in the cluster.
	StorageNodesTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "storage_nodes_total",
		Help:      "Total number of storage nodes in the cluster.",
	})
)

// ── Middleware ───────────────────────────────────────────────

// GinMetricsMiddleware returns Gin middleware that records HTTP request metrics.
// It tracks request count, duration, and response status code for each route.
func GinMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())
		path := c.FullPath()
		if path == "" {
			path = "unknown" // Avoid high cardinality from 404 paths
		}

		HTTPRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		HTTPRequestDuration.WithLabelValues(c.Request.Method, path).Observe(duration)
	}
}

// ── Helpers ─────────────────────────────────────────────────

// RecordDBQuery records a database query duration for the given operation name.
func RecordDBQuery(operation string, dur time.Duration) {
	DBQueryDuration.WithLabelValues(operation).Observe(dur.Seconds())
}

// RecordMinIOOp records a MinIO operation duration.
func RecordMinIOOp(operation string, dur time.Duration) {
	MinIOOperationDuration.WithLabelValues(operation).Observe(dur.Seconds())
}
