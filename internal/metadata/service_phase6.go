package metadata

import (
	"context"
	"fmt"
	"math"

	"go.uber.org/zap"
)

// ── Versioning Service Methods ──────────────────────────────

// ListVersions returns all versions of a file.
func (s *FileService) ListVersions(ctx context.Context, userID, fileName string, parentID *string) ([]*File, error) {
	return s.repo.ListFileVersions(ctx, userID, fileName, parentID)
}

// DownloadVersion prepares a download for a specific file version.
func (s *FileService) DownloadVersion(ctx context.Context, userID, fileID string, version int) (*DownloadResult, error) {
	file, err := s.repo.GetFileVersion(ctx, userID, fileID, version)
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, nil
	}

	manifest, err := s.getManifestCached(ctx, file.ID)
	if err != nil {
		return nil, fmt.Errorf("get manifest for version: %w", err)
	}

	hashes := make([]string, len(manifest))
	for i, m := range manifest {
		hashes[i] = m.ChunkHash
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

// DeleteVersion deletes a specific version of a file.
// If the deleted version was 'active', the previous superseded version
// is promoted back to active.
func (s *FileService) DeleteVersion(ctx context.Context, userID, fileID string, version int) error {
	file, err := s.repo.GetFileVersion(ctx, userID, fileID, version)
	if err != nil {
		return fmt.Errorf("get version: %w", err)
	}
	if file == nil {
		return fmt.Errorf("version %d not found", version)
	}

	// Get manifest to decrement chunk ref counts
	manifest, err := s.repo.GetManifest(ctx, file.ID)
	if err != nil {
		s.logger.Warn("Failed to get manifest for version delete", zap.Error(err))
	}

	// Mark as deleted
	if err := s.repo.UpdateFileStatus(ctx, file.ID, "deleted"); err != nil {
		return fmt.Errorf("delete version: %w", err)
	}

	// Decrement chunk ref counts
	if len(manifest) > 0 {
		hashes := make([]string, len(manifest))
		for i, m := range manifest {
			hashes[i] = m.ChunkHash
		}
		if _, err := s.repo.DecrementRefCounts(ctx, hashes); err != nil {
			s.logger.Error("Failed to decrement ref counts", zap.Error(err))
		}
	}

	// Update user storage
	if err := s.repo.UpdateStorageUsed(ctx, userID, -file.Size); err != nil {
		s.logger.Error("Failed to update storage usage", zap.Error(err))
	}

	// If the active version was deleted, promote the latest superseded version
	if file.Status == "active" {
		latest, err := s.repo.GetLatestFileByName(ctx, userID, file.ParentID, file.Name)
		if err == nil && latest != nil && latest.Status == "superseded" {
			if promoteErr := s.repo.UpdateFileStatus(ctx, latest.ID, "active"); promoteErr != nil {
				s.logger.Error("Failed to promote version", zap.Error(promoteErr))
			}
		}
	}

	s.invalidateFileCache(ctx, file.ID)
	s.invalidateUserCache(ctx, userID)
	return nil
}

// ── Folder Service Methods ──────────────────────────────────

// CreateFolder creates a directory in the file hierarchy.
func (s *FileService) CreateFolder(ctx context.Context, userID, name string, parentID *string) (*File, error) {
	// Validate parent exists if provided
	if parentID != nil {
		parent, err := s.repo.VerifyFolderOwnership(ctx, userID, *parentID)
		if err != nil {
			return nil, fmt.Errorf("verify parent: %w", err)
		}
		if parent == nil {
			return nil, fmt.Errorf("parent folder not found")
		}
	}

	folder, err := s.repo.CreateDirectory(ctx, userID, name, parentID)
	if err != nil {
		return nil, fmt.Errorf("create folder: %w", err)
	}

	s.invalidateUserCache(ctx, userID)
	return folder, nil
}

// ListFolderContents lists all items in a folder.
func (s *FileService) ListFolderContents(ctx context.Context, userID string, folderID *string, page, pageSize int) ([]*File, int64, error) {
	// Validate folder exists if provided
	if folderID != nil {
		folder, err := s.repo.VerifyFolderOwnership(ctx, userID, *folderID)
		if err != nil {
			return nil, 0, fmt.Errorf("verify folder: %w", err)
		}
		if folder == nil {
			return nil, 0, fmt.Errorf("folder not found")
		}
	}

	return s.repo.GetFolderContents(ctx, userID, folderID, ListOptions{
		Page:     page,
		PageSize: pageSize,
	})
}

// MoveFile moves a file or folder to a new parent directory.
// Validates ownership and prevents circular references.
func (s *FileService) MoveFile(ctx context.Context, userID, fileID string, newParentID *string) error {
	// Can't move to itself
	if newParentID != nil && *newParentID == fileID {
		return fmt.Errorf("cannot move a file into itself")
	}

	// Circular reference check for folder moves
	if newParentID != nil {
		isDesc, err := s.repo.IsDescendant(ctx, *newParentID, fileID)
		if err != nil {
			return fmt.Errorf("cycle check: %w", err)
		}
		if isDesc {
			return fmt.Errorf("cannot move a folder into its own descendant (circular reference)")
		}
	}

	if err := s.repo.MoveFile(ctx, userID, fileID, newParentID); err != nil {
		return fmt.Errorf("move file: %w", err)
	}

	s.invalidateFileCache(ctx, fileID)
	s.invalidateUserCache(ctx, userID)
	return nil
}

// DeleteFolder recursively deletes a folder and all its contents.
// Uses PostgreSQL ON DELETE CASCADE for the folder hierarchy, but manually
// handles chunk ref_count decrements for proper garbage collection.
func (s *FileService) DeleteFolder(ctx context.Context, userID, folderID string) error {
	// Verify ownership
	folder, err := s.repo.VerifyFolderOwnership(ctx, userID, folderID)
	if err != nil {
		return fmt.Errorf("verify folder: %w", err)
	}
	if folder == nil {
		return fmt.Errorf("folder not found")
	}

	// Get all descendant file IDs (not directories) for chunk cleanup
	fileIDs, err := s.repo.GetAllDescendantFileIDs(ctx, folderID)
	if err != nil {
		s.logger.Error("Failed to get descendant files", zap.Error(err))
	}

	// Decrement chunk ref counts for each file
	var totalSize int64
	for _, fID := range fileIDs {
		manifest, err := s.repo.GetManifest(ctx, fID)
		if err != nil {
			continue
		}

		file, err := s.repo.GetFileByID(ctx, userID, fID)
		if err == nil && file != nil {
			totalSize += file.Size
		}

		hashes := make([]string, len(manifest))
		for i, m := range manifest {
			hashes[i] = m.ChunkHash
		}
		if _, err := s.repo.DecrementRefCounts(ctx, hashes); err != nil {
			s.logger.Error("Failed to decrement ref counts during folder delete",
				zap.String("file_id", fID),
				zap.Error(err),
			)
		}
	}

	// Delete the folder (cascades to all children via FK)
	if err := s.repo.SoftDeleteFile(ctx, userID, folderID); err != nil {
		return fmt.Errorf("delete folder: %w", err)
	}

	// Update storage usage
	if totalSize > 0 {
		if err := s.repo.UpdateStorageUsed(ctx, userID, -totalSize); err != nil {
			s.logger.Error("Failed to update storage after folder delete", zap.Error(err))
		}
	}

	s.invalidateUserCache(ctx, userID)
	return nil
}

// ── Search Service Method ───────────────────────────────────

// Search performs a file search with filters.
func (s *FileService) Search(ctx context.Context, userID string, q *SearchQuery) ([]*File, int64, error) {
	return s.repo.SearchFiles(ctx, userID, q)
}

// ── Range Download ──────────────────────────────────────────

// RangeDownloadResult contains the data needed to serve a partial download.
type RangeDownloadResult struct {
	FileName     string
	MimeType     string
	FileSize     int64     // Total file size
	RangeStart   int64     // Requested range start
	RangeEnd     int64     // Requested range end (inclusive)
	ContentLen   int64     // Bytes in this response
	ChunkPlan    []RangeChunk
}

// RangeChunk describes which bytes to read from a specific chunk.
type RangeChunk struct {
	Hash       string
	SkipBytes  int64 // Bytes to skip at start of this chunk
	ReadBytes  int64 // Bytes to read from this chunk
}

// PrepareRangeDownload calculates which chunks (and byte offsets within them)
// cover the requested HTTP Range. The manifest's byte_offset field is used
// to find the right chunks without reconstructing the full file.
func (s *FileService) PrepareRangeDownload(ctx context.Context, userID, fileID string, rangeStart, rangeEnd int64) (*RangeDownloadResult, error) {
	file, err := s.repo.GetFileByID(ctx, userID, fileID)
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, nil
	}

	// Clamp range end to file size
	if rangeEnd >= file.Size || rangeEnd < 0 {
		rangeEnd = file.Size - 1
	}
	if rangeStart < 0 {
		rangeStart = 0
	}
	if rangeStart > rangeEnd {
		return nil, fmt.Errorf("invalid range: %d-%d", rangeStart, rangeEnd)
	}

	manifest, err := s.getManifestCached(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("get manifest: %w", err)
	}

	plan := make([]RangeChunk, 0, len(manifest))
	for _, entry := range manifest {
		chunkStart := entry.ByteOffset
		chunkEnd := entry.ByteOffset + entry.ChunkSize - 1

		// Skip chunks entirely before the range
		if chunkEnd < rangeStart {
			continue
		}
		// Stop after chunks past the range
		if chunkStart > rangeEnd {
			break
		}

		// Calculate how many bytes to skip/read within this chunk
		skipBytes := int64(0)
		if rangeStart > chunkStart {
			skipBytes = rangeStart - chunkStart
		}

		readEnd := int64(math.Min(float64(chunkEnd), float64(rangeEnd)))
		readBytes := readEnd - (chunkStart + skipBytes) + 1

		plan = append(plan, RangeChunk{
			Hash:      entry.ChunkHash,
			SkipBytes: skipBytes,
			ReadBytes: readBytes,
		})
	}

	mimeType := ""
	if file.MimeType != nil {
		mimeType = *file.MimeType
	}

	return &RangeDownloadResult{
		FileName:   file.Name,
		MimeType:   mimeType,
		FileSize:   file.Size,
		RangeStart: rangeStart,
		RangeEnd:   rangeEnd,
		ContentLen: rangeEnd - rangeStart + 1,
		ChunkPlan:  plan,
	}, nil
}
