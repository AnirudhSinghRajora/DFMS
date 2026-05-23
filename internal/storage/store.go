package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
)

// ObjectStore defines the interface for chunk storage operations.
// This abstraction allows swapping MinIO for another backend (S3, GCS, etc.)
// without changing business logic.
type ObjectStore interface {
	PutChunk(ctx context.Context, hash string, data io.Reader, size int64) error
	GetChunk(ctx context.Context, hash string) (io.ReadCloser, int64, error)
	DeleteChunk(ctx context.Context, hash string) error
	ChunkExists(ctx context.Context, hash string) (bool, error)
	HealthCheck(ctx context.Context) error
}

// MinIOStore implements ObjectStore using MinIO as the backing store.
type MinIOStore struct {
	client *minio.Client
	bucket string
}

// NewMinIOStore creates a new MinIO-backed chunk store.
func NewMinIOStore(client *minio.Client, bucket string) *MinIOStore {
	return &MinIOStore{
		client: client,
		bucket: bucket,
	}
}

// chunkKey generates the object key for a chunk hash using directory sharding.
// Format: {hash[0:2]}/{hash[2:4]}/{hash}
// This prevents any single directory from containing millions of objects,
// which degrades listing performance in object stores.
//
// Example: hash "a1b2c3..." → "a1/b2/a1b2c3..."
func chunkKey(hash string) string {
	if len(hash) < 4 {
		return hash
	}
	return fmt.Sprintf("%s/%s/%s", hash[0:2], hash[2:4], hash)
}

// PutChunk uploads a chunk to MinIO. The chunk is stored under a
// sharded directory path derived from its SHA-256 hash.
//
// The operation is idempotent — uploading the same hash twice simply
// overwrites with identical content.
func (s *MinIOStore) PutChunk(ctx context.Context, hash string, data io.Reader, size int64) error {
	key := chunkKey(hash)

	_, err := s.client.PutObject(ctx, s.bucket, key, data, size, minio.PutObjectOptions{
		ContentType: "application/octet-stream",
		UserMetadata: map[string]string{
			"sha256": hash,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to upload chunk %s: %w", hash[:12], err)
	}

	return nil
}

// GetChunk downloads a chunk from MinIO. The caller is responsible
// for closing the returned ReadCloser.
func (s *MinIOStore) GetChunk(ctx context.Context, hash string) (io.ReadCloser, int64, error) {
	key := chunkKey(hash)

	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get chunk %s: %w", hash[:12], err)
	}

	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, 0, fmt.Errorf("failed to stat chunk %s: %w", hash[:12], err)
	}

	return obj, info.Size, nil
}

// DeleteChunk removes a chunk from MinIO.
func (s *MinIOStore) DeleteChunk(ctx context.Context, hash string) error {
	key := chunkKey(hash)

	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete chunk %s: %w", hash[:12], err)
	}

	return nil
}

// ChunkExists checks if a chunk exists in MinIO without downloading it.
func (s *MinIOStore) ChunkExists(ctx context.Context, hash string) (bool, error) {
	key := chunkKey(hash)

	_, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		// MinIO returns an error response for non-existent objects
		resp := minio.ToErrorResponse(err)
		if resp.Code == "NoSuchKey" {
			return false, nil
		}
		return false, fmt.Errorf("failed to check chunk %s: %w", hash[:12], err)
	}

	return true, nil
}

// HealthCheck verifies the MinIO connection by checking bucket accessibility.
func (s *MinIOStore) HealthCheck(ctx context.Context) error {
	_, err := s.client.BucketExists(ctx, s.bucket)
	return err
}
