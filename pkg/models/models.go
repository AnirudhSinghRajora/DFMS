// Package models contains shared data models used across DFMS services.
package models

import "time"

// User represents a registered user in the system.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	DisplayName  string    `json:"display_name"`
	Role         string    `json:"role"`
	StorageQuota int64     `json:"storage_quota"`
	StorageUsed  int64     `json:"storage_used"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// File represents file or folder metadata.
type File struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	ParentID    *string   `json:"parent_id,omitempty"`
	Name        string    `json:"name"`
	IsDirectory bool      `json:"is_directory"`
	Size        int64     `json:"size"`
	MimeType    *string   `json:"mime_type,omitempty"`
	Checksum    *string   `json:"checksum,omitempty"`
	Version     int       `json:"version"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Chunk represents a content-addressed data chunk.
type Chunk struct {
	ID           string    `json:"id"`
	Hash         string    `json:"hash"`
	Size         int64     `json:"size"`
	RefCount     int       `json:"ref_count"`
	StorageNodes []string  `json:"storage_nodes"`
	Bucket       string    `json:"bucket"`
	ObjectKey    string    `json:"object_key"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// FileChunk represents the mapping between a file and its chunks (the manifest).
type FileChunk struct {
	ID         string `json:"id"`
	FileID     string `json:"file_id"`
	ChunkID    string `json:"chunk_id"`
	ChunkIndex int    `json:"chunk_index"`
	ByteOffset int64  `json:"byte_offset"`
}

// StorageNode represents a MinIO storage node in the cluster.
type StorageNode struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Endpoint      string    `json:"endpoint"`
	Region        string    `json:"region"`
	Capacity      int64     `json:"capacity"`
	Used          int64     `json:"used"`
	Weight        int       `json:"weight"`
	Status        string    `json:"status"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	CreatedAt     time.Time `json:"created_at"`
}

// File status constants.
const (
	FileStatusActive     = "active"
	FileStatusDeleted    = "deleted"
	FileStatusUploading  = "uploading"
	FileStatusSuperseded = "superseded"
)

// Chunk status constants.
const (
	ChunkStatusActive    = "active"
	ChunkStatusPendingGC = "pending_gc"
	ChunkStatusDeleted   = "deleted"
)

// Node status constants.
const (
	NodeStatusHealthy  = "healthy"
	NodeStatusDegraded = "degraded"
	NodeStatusOffline  = "offline"
)
