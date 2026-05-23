// Package storage provides MinIO object storage integration for the DFMS.
// It handles all raw byte operations: uploading, downloading, deleting, and
// checking the existence of content-addressed chunks.
package storage

import (
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/AnirudhSinghRajora/DFMS/internal/config"
)

// NewMinIOClient creates a new MinIO client and ensures the required
// buckets exist. This is called during service startup.
func NewMinIOClient(cfg *config.MinIOConfig) (*minio.Client, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("no MinIO endpoints configured")
	}

	client, err := minio.New(cfg.Endpoints[0], &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	// Ensure required buckets exist
	ctx := context.Background()
	for _, bucket := range []string{cfg.ChunkBucket, cfg.TempBucket} {
		exists, err := client.BucketExists(ctx, bucket)
		if err != nil {
			return nil, fmt.Errorf("failed to check bucket %q: %w", bucket, err)
		}
		if !exists {
			if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
				return nil, fmt.Errorf("failed to create bucket %q: %w", bucket, err)
			}
		}
	}

	return client, nil
}
