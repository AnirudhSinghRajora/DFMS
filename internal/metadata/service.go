package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"go.uber.org/zap"

	pb "github.com/AnirudhSinghRajora/DFMS/api/proto/chunkpb"
	"github.com/AnirudhSinghRajora/DFMS/internal/cache"
	"github.com/AnirudhSinghRajora/DFMS/internal/events"
)

const (
	// Cache TTLs
	fileCacheTTL     = 10 * time.Minute
	manifestCacheTTL = 10 * time.Minute
)

// FileResponse is the API response for file operations.
type FileResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	MimeType    string    `json:"mime_type,omitempty"`
	Checksum    string    `json:"checksum"`
	Version     int       `json:"version"`
	ChunkCount  int       `json:"chunk_count"`
	NewChunks   int       `json:"new_chunks"`
	DedupChunks int       `json:"dedup_chunks"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListResponse is the paginated file listing response.
type ListResponse struct {
	Files      []*File `json:"files"`
	Total      int64   `json:"total"`
	Page       int     `json:"page"`
	PageSize   int     `json:"page_size"`
	TotalPages int     `json:"total_pages"`
}

// DownloadResult holds metadata needed to stream a file download.
type DownloadResult struct {
	FileName    string
	MimeType    string
	Size        int64
	ChunkHashes []string
}

// FileService orchestrates file operations by combining the PostgreSQL
// repository, Redis cache, gRPC ChunkService client, and Kafka producer.
type FileService struct {
	repo        Repository
	cache       *cache.Client
	chunkClient pb.ChunkServiceClient
	producer    *events.Producer // Optional: nil disables event publishing
	logger      *zap.Logger
}

// NewFileService creates a new file service.
// The producer is optional — pass nil to disable Kafka event publishing.
func NewFileService(
	repo Repository,
	cache *cache.Client,
	chunkClient pb.ChunkServiceClient,
	producer *events.Producer,
	logger *zap.Logger,
) *FileService {
	return &FileService{
		repo:        repo,
		cache:       cache,
		chunkClient: chunkClient,
		producer:    producer,
		logger:      logger,
	}
}

// Upload orchestrates the full upload pipeline:
//  1. Check quota
//  2. Stream file to ChunkService via gRPC (CDC → dedup → MinIO)
//  3. Save metadata in PostgreSQL (file record, chunk records, manifest)
//  4. Update user's storage usage
//  5. Invalidate caches
func (s *FileService) Upload(ctx context.Context, userID, fileName, mimeType string, fileReader io.Reader) (*FileResponse, error) {
	// 1. Check quota
	used, quota, err := s.repo.GetStorageUsage(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("check quota: %w", err)
	}
	// We can't check exact size before upload (streaming), but we can reject
	// if already at 95% capacity. Exact check happens after upload completes.
	if float64(used)/float64(quota) > 0.95 {
		return nil, fmt.Errorf("QUOTA_EXCEEDED: storage quota nearly exhausted (%.1f%% used)", float64(used)/float64(quota)*100)
	}

	// 2. Stream file to ChunkService
	stream, err := s.chunkClient.UploadFile(ctx)
	if err != nil {
		return nil, fmt.Errorf("open upload stream: %w", err)
	}

	// Send metadata as the first message
	err = stream.Send(&pb.UploadFileRequest{
		Data: &pb.UploadFileRequest_Metadata{
			Metadata: &pb.UploadMetadata{
				FileName: fileName,
				UserId:   userID,
				MimeType: mimeType,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("send metadata: %w", err)
	}

	// Stream file bytes in 64KB frames
	buf := make([]byte, 64*1024)
	for {
		n, readErr := fileReader.Read(buf)
		if n > 0 {
			err = stream.Send(&pb.UploadFileRequest{
				Data: &pb.UploadFileRequest_ChunkData{
					ChunkData: buf[:n],
				},
			})
			if err != nil {
				return nil, fmt.Errorf("send data: %w", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read file: %w", readErr)
		}
	}

	// Close send side and receive response
	resp, err := stream.CloseAndRecv()
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}

	// Post-upload quota check: reject if file would exceed quota
	if used+resp.TotalSize > quota {
		// TODO: Clean up uploaded chunks (they'll be GC'd eventually by ref_count = 0)
		return nil, fmt.Errorf("QUOTA_EXCEEDED: file size %d would exceed quota (used: %d, quota: %d)",
			resp.TotalSize, used, quota)
	}

	// 3. Save metadata in PostgreSQL (with auto-versioning)
	params := CreateFileParams{
		UserID:   userID,
		Name:     fileName,
		Size:     resp.TotalSize,
		MimeType: mimeType,
		Checksum: resp.FileChecksum,
	}

	// Check if a file with this name already exists → auto-version
	existing, err := s.repo.GetLatestFileByName(ctx, userID, nil, fileName)
	if err != nil {
		return nil, fmt.Errorf("check existing file: %w", err)
	}

	var file *File
	switch {
	case existing != nil && existing.Status == "active":
		// Mark the current active version as superseded
		if updateErr := s.repo.UpdateFileStatus(ctx, existing.ID, "superseded"); updateErr != nil {
			s.logger.Error("Failed to supersede previous version", zap.Error(updateErr))
		}
		// Create new version
		file, err = s.repo.CreateFileWithVersion(ctx, params, existing.ParentID, existing.Version+1)
	case existing != nil && existing.Status == "superseded":
		// All versions were superseded (active was deleted), create next version
		file, err = s.repo.CreateFileWithVersion(ctx, params, existing.ParentID, existing.Version+1)
	default:
		// No existing file — create version 1
		file, err = s.repo.CreateFile(ctx, params)
	}
	if err != nil {
		return nil, fmt.Errorf("create file record: %w", err)
	}

	// Upsert chunks and build manifest
	manifest := make([]ManifestEntry, 0, len(resp.Chunks))
	var byteOffset int64

	for _, chunkInfo := range resp.Chunks {
		// Upsert chunk (insert or increment ref_count)
		if _, err := s.repo.UpsertChunk(ctx, chunkInfo.Hash, chunkInfo.Size); err != nil {
			s.logger.Error("Failed to upsert chunk",
				zap.String("hash", chunkInfo.Hash[:12]),
				zap.Error(err),
			)
			// Continue — partial metadata is better than no metadata
		}

		manifest = append(manifest, ManifestEntry{
			ChunkHash:  chunkInfo.Hash,
			ChunkSize:  chunkInfo.Size,
			ChunkIndex: int(chunkInfo.Index),
			ByteOffset: byteOffset,
		})
		byteOffset += chunkInfo.Size
	}

	// Save manifest (file_chunks table)
	if err := s.repo.SaveManifest(ctx, file.ID, manifest); err != nil {
		return nil, fmt.Errorf("save manifest: %w", err)
	}

	// 4. Publish chunk.created events for replication (after DB commit)
	// This MUST happen after UpsertChunk so the replication manager's
	// UPDATE chunks SET storage_nodes finds an existing row.
	if s.producer != nil {
		for _, ci := range resp.Chunks {
			if !ci.IsDuplicate {
				_ = s.producer.Publish(ctx, events.TopicChunksCreated, ci.Hash, "",
					events.ChunkCreatedEvent{
						ChunkHash: ci.Hash,
						ChunkSize: ci.Size,
						Bucket:    "chunks",
						UserID:    userID,
						FileID:    file.ID,
					},
				)
			}
		}
	}

	// 5. Update storage usage
	if err := s.repo.UpdateStorageUsed(ctx, userID, resp.TotalSize); err != nil {
		s.logger.Error("Failed to update storage usage", zap.Error(err))
		// Non-fatal: the file is stored, usage tracking is slightly off
	}

	// 6. Invalidate file list cache
	s.invalidateUserCache(ctx, userID)

	s.logger.Info("File upload completed",
		zap.String("file_id", file.ID),
		zap.String("name", fileName),
		zap.Int64("size", resp.TotalSize),
		zap.Int32("chunks", resp.ChunkCount),
		zap.Int32("new", resp.NewChunks),
		zap.Int32("deduped", resp.DedupChunks),
	)

	return &FileResponse{
		ID:          file.ID,
		Name:        fileName,
		Size:        resp.TotalSize,
		MimeType:    mimeType,
		Checksum:    resp.FileChecksum,
		Version:     file.Version,
		ChunkCount:  int(resp.ChunkCount),
		NewChunks:   int(resp.NewChunks),
		DedupChunks: int(resp.DedupChunks),
		CreatedAt:   file.CreatedAt,
	}, nil
}

// PrepareDownload fetches file metadata and manifest, returning everything
// needed to stream the download. The actual byte streaming is done by the
// API gateway calling ChunkService.DownloadFile.
func (s *FileService) PrepareDownload(ctx context.Context, userID, fileID string) (*DownloadResult, error) {
	// Get file metadata (cache → DB)
	file, err := s.GetFile(ctx, userID, fileID)
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, nil // Not found
	}

	// Get manifest (cache → DB)
	manifest, err := s.getManifestCached(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("get manifest: %w", err)
	}

	// Extract ordered chunk hashes
	hashes := make([]string, len(manifest))
	for i, e := range manifest {
		hashes[i] = e.ChunkHash
	}

	mimeType := ""
	if file.MimeType != nil {
		mimeType = *file.MimeType
	}

	return &DownloadResult{
		FileName:    file.Name,
		MimeType:    mimeType,
		Size:        file.Size,
		ChunkHashes: hashes,
	}, nil
}

// ListFiles returns a paginated list of active files for a user.
func (s *FileService) ListFiles(ctx context.Context, userID string, page, pageSize int) (*ListResponse, error) {
	files, total, err := s.repo.ListFilesByUser(ctx, userID, ListOptions{
		Page:     page,
		PageSize: pageSize,
		Status:   "active",
	})
	if err != nil {
		return nil, err
	}

	totalPages := int(total) / pageSize
	if int(total)%pageSize != 0 {
		totalPages++
	}

	return &ListResponse{
		Files:      files,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}, nil
}

// GetFile returns a single file by ID, checking ownership.
func (s *FileService) GetFile(ctx context.Context, userID, fileID string) (*File, error) {
	// Try cache first
	cacheKey := fmt.Sprintf("file:%s", fileID)
	if cached, err := s.cache.Get(ctx, cacheKey); err == nil {
		var f File
		if json.Unmarshal([]byte(cached), &f) == nil && f.UserID == userID {
			return &f, nil
		}
	}

	// Cache miss → DB
	file, err := s.repo.GetFileByID(ctx, userID, fileID)
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, nil
	}

	// Populate cache
	if data, err := json.Marshal(file); err == nil {
		_ = s.cache.Set(ctx, cacheKey, string(data), fileCacheTTL)
	}

	return file, nil
}

// DeleteFile soft-deletes a file, decrements chunk ref counts, and updates
// the user's storage usage.
func (s *FileService) DeleteFile(ctx context.Context, userID, fileID string) error {
	// Get file to know the size
	file, err := s.GetFile(ctx, userID, fileID)
	if err != nil {
		return err
	}
	if file == nil {
		return fmt.Errorf("file not found")
	}

	// Get manifest to know which chunks to decrement
	manifest, err := s.repo.GetManifest(ctx, fileID)
	if err != nil {
		return fmt.Errorf("get manifest: %w", err)
	}

	// Soft delete the file
	if err := s.repo.SoftDeleteFile(ctx, userID, fileID); err != nil {
		return err
	}

	// Decrement chunk ref counts
	if len(manifest) > 0 {
		hashes := make([]string, len(manifest))
		for i, e := range manifest {
			hashes[i] = e.ChunkHash
		}

		orphaned, err := s.repo.DecrementRefCounts(ctx, hashes)
		if err != nil {
			s.logger.Error("Failed to decrement ref counts", zap.Error(err))
		}

		// If any chunks are now orphaned, tell ChunkService to delete from MinIO
		if len(orphaned) > 0 {
			s.logger.Info("Deleting orphaned chunks", zap.Int("count", len(orphaned)))
			if _, err := s.chunkClient.DeleteChunks(ctx, &pb.DeleteChunksRequest{
				ChunkHashes: orphaned,
			}); err != nil {
				s.logger.Error("Failed to delete orphaned chunks from MinIO", zap.Error(err))
				// Non-fatal: GC will clean them up later
			}
		}
	}

	// Update storage usage
	if err := s.repo.UpdateStorageUsed(ctx, userID, -file.Size); err != nil {
		s.logger.Error("Failed to update storage usage after delete", zap.Error(err))
	}

	// Invalidate caches
	s.invalidateFileCache(ctx, fileID)
	s.invalidateUserCache(ctx, userID)

	// Publish file.deleted event
	if s.producer != nil {
		hashes := make([]string, len(manifest))
		for i, e := range manifest {
			hashes[i] = e.ChunkHash
		}
		_ = s.producer.Publish(ctx, events.TopicFilesDeleted, fileID, "",
			events.FileDeletedEvent{
				FileID:      fileID,
				UserID:      userID,
				ChunkHashes: hashes,
				FileSize:    file.Size,
			},
		)
	}

	return nil
}

// ── Cache Helpers ────────────────────────────────────────────

func (s *FileService) getManifestCached(ctx context.Context, fileID string) ([]ManifestEntry, error) {
	cacheKey := fmt.Sprintf("manifest:%s", fileID)

	// Try cache
	if cached, err := s.cache.Get(ctx, cacheKey); err == nil {
		var entries []ManifestEntry
		if json.Unmarshal([]byte(cached), &entries) == nil {
			return entries, nil
		}
	}

	// Cache miss → DB
	entries, err := s.repo.GetManifest(ctx, fileID)
	if err != nil {
		return nil, err
	}

	// Populate cache
	if data, err := json.Marshal(entries); err == nil {
		_ = s.cache.Set(ctx, cacheKey, string(data), manifestCacheTTL)
	}

	return entries, nil
}

func (s *FileService) invalidateFileCache(ctx context.Context, fileID string) {
	_ = s.cache.Delete(ctx, fmt.Sprintf("file:%s", fileID), fmt.Sprintf("manifest:%s", fileID))
}

func (s *FileService) invalidateUserCache(ctx context.Context, userID string) {
	_ = s.cache.Delete(ctx, fmt.Sprintf("filelist:%s", userID))
}
