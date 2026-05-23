// Package events provides Kafka-based event publishing and consumption
// for the DFMS distributed event system. Events drive asynchronous
// workflows: replication, garbage collection, and health monitoring.
package events

import (
	"encoding/json"
	"time"
)

// ── Topic Constants ─────────────────────────────────────────
// These must match the topics created in docker-compose kafka-init.

const (
	TopicChunksCreated = "dfms.chunks.created"
	TopicChunksDeleted = "dfms.chunks.deleted"
	TopicFilesDeleted  = "dfms.files.deleted"
	TopicNodesHealth   = "dfms.nodes.health"
)

// ── Event Types ─────────────────────────────────────────────

// Envelope wraps every event with common metadata for tracing and debugging.
type Envelope struct {
	EventType string          `json:"event_type"`
	Timestamp time.Time       `json:"timestamp"`
	TraceID   string          `json:"trace_id,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

// ChunkCreatedEvent is published when a new chunk is uploaded to MinIO.
// Consumed by the Replication Manager to trigger chunk replication.
type ChunkCreatedEvent struct {
	ChunkHash string `json:"chunk_hash"`
	ChunkSize int64  `json:"chunk_size"`
	Bucket    string `json:"bucket"`
	ObjectKey string `json:"object_key"`
	UserID    string `json:"user_id"`
	FileID    string `json:"file_id,omitempty"`
}

// FileDeletedEvent is published when a file is soft-deleted.
// Consumed by downstream services for cleanup and audit logging.
type FileDeletedEvent struct {
	FileID      string   `json:"file_id"`
	UserID      string   `json:"user_id"`
	ChunkHashes []string `json:"chunk_hashes"`
	FileSize    int64    `json:"file_size"`
}

// ChunkDeletedEvent is published when the GC removes a chunk from MinIO.
type ChunkDeletedEvent struct {
	ChunkHash string `json:"chunk_hash"`
	ChunkSize int64  `json:"chunk_size"`
}

// NodeHealthEvent is published when a storage node's health status changes.
// Consumed by the Replication Manager to trigger re-replication on failure.
type NodeHealthEvent struct {
	NodeID     string `json:"node_id"`
	NodeName   string `json:"node_name"`
	OldStatus  string `json:"old_status"`
	NewStatus  string `json:"new_status"` // "healthy", "degraded", "offline"
	Endpoint   string `json:"endpoint"`
	LatencyMs  int64  `json:"latency_ms,omitempty"`
}

// NewEnvelope creates an Envelope for the given event type and payload.
func NewEnvelope(eventType, traceID string, payload interface{}) (*Envelope, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Envelope{
		EventType: eventType,
		Timestamp: time.Now().UTC(),
		TraceID:   traceID,
		Payload:   data,
	}, nil
}

// Marshal serializes an Envelope to JSON bytes for Kafka publishing.
func (e *Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}
