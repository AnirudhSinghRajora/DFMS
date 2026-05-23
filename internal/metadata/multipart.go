package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/AnirudhSinghRajora/DFMS/internal/cache"
	"github.com/AnirudhSinghRajora/DFMS/internal/storage"
)

const (
	multipartPrefix    = "multipart:"    // Redis key prefix for upload sessions
	multipartPartsKey  = "parts:"        // Redis hash key for parts within a session
	multipartSessionTTL = 24 * time.Hour // Auto-expire abandoned uploads
	minPartSize         = 5 * 1024 * 1024  // 5 MB minimum per part
	maxPartSize         = 500 * 1024 * 1024 // 500 MB maximum per part
	maxPartNumber       = 10000             // Maximum parts per upload
)

// MultipartSession stores the metadata for an in-progress multipart upload.
type MultipartSession struct {
	UploadID  string `json:"upload_id"`
	UserID    string `json:"user_id"`
	FileName  string `json:"file_name"`
	MimeType  string `json:"mime_type"`
	CreatedAt int64  `json:"created_at"`
}

// PartInfo stores metadata about a single uploaded part.
type PartInfo struct {
	PartNum int    `json:"part_num"`
	Size    int64  `json:"size"`
	Key     string `json:"key"` // MinIO object key in temp-uploads bucket
}

// MultipartService handles large file uploads via chunked parts.
// Parts are stored temporarily in MinIO's temp-uploads bucket,
// session state is tracked in Redis with 24h TTL for auto-cleanup.
type MultipartService struct {
	cache    *cache.Client
	store    storage.ObjectStore
	tempStore storage.ObjectStore // temp-uploads bucket
	fileSvc  *FileService
	logger   *zap.Logger
}

// NewMultipartService creates a new multipart upload service.
func NewMultipartService(
	cache *cache.Client,
	mainStore storage.ObjectStore,
	tempStore storage.ObjectStore,
	fileSvc *FileService,
	logger *zap.Logger,
) *MultipartService {
	return &MultipartService{
		cache:     cache,
		store:     mainStore,
		tempStore: tempStore,
		fileSvc:   fileSvc,
		logger:    logger,
	}
}

// InitUpload creates a new multipart upload session.
// Returns an upload_id that must be used for subsequent part uploads.
func (m *MultipartService) InitUpload(ctx context.Context, userID, fileName, mimeType string) (string, error) {
	uploadID := uuid.New().String()

	session := MultipartSession{
		UploadID:  uploadID,
		UserID:    userID,
		FileName:  fileName,
		MimeType:  mimeType,
		CreatedAt: time.Now().Unix(),
	}

	data, err := json.Marshal(session)
	if err != nil {
		return "", fmt.Errorf("marshal session: %w", err)
	}

	key := multipartPrefix + uploadID
	if err := m.cache.Set(ctx, key, string(data), multipartSessionTTL); err != nil {
		return "", fmt.Errorf("save session: %w", err)
	}

	m.logger.Info("Multipart upload initialized",
		zap.String("upload_id", uploadID),
		zap.String("file_name", fileName),
		zap.String("user_id", userID),
	)
	return uploadID, nil
}

// UploadPart stores a single part in the temp-uploads bucket and
// records it in Redis for later assembly.
func (m *MultipartService) UploadPart(ctx context.Context, uploadID string, partNum int, reader io.Reader) (*PartInfo, error) {
	// Validate session exists
	session, err := m.getSession(ctx, uploadID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("upload session not found or expired: %s", uploadID)
	}

	if partNum < 1 || partNum > maxPartNumber {
		return nil, fmt.Errorf("invalid part number %d (must be 1-%d)", partNum, maxPartNumber)
	}

	// Read part data into memory to get size and upload to MinIO
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read part data: %w", err)
	}

	partSize := int64(len(data))

	// Store part in temp-uploads bucket
	objKey := fmt.Sprintf("%s/part_%05d", uploadID, partNum)
	if err := m.tempStore.PutChunk(ctx, objKey, bytes.NewReader(data), partSize); err != nil {
		return nil, fmt.Errorf("store part: %w", err)
	}

	// Record part metadata in Redis
	partInfo := PartInfo{
		PartNum: partNum,
		Size:    partSize,
		Key:     objKey,
	}
	partData, _ := json.Marshal(partInfo)
	partsKey := multipartPrefix + multipartPartsKey + uploadID
	if err := m.cache.HSet(ctx, partsKey, fmt.Sprintf("%d", partNum), string(partData)); err != nil {
		return nil, fmt.Errorf("record part: %w", err)
	}
	_ = m.cache.Expire(ctx, partsKey, multipartSessionTTL)

	m.logger.Info("Part uploaded",
		zap.String("upload_id", uploadID),
		zap.Int("part_num", partNum),
		zap.Int64("size", partSize),
	)
	return &partInfo, nil
}

// CompleteUpload assembles all parts in order, feeds the combined data
// through the standard upload pipeline (CDC chunking, dedup, MinIO), and
// cleans up the temporary parts.
func (m *MultipartService) CompleteUpload(ctx context.Context, uploadID string) (*FileResponse, error) {
	session, err := m.getSession(ctx, uploadID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("upload session not found or expired: %s", uploadID)
	}

	// Get all parts from Redis
	partsKey := multipartPrefix + multipartPartsKey + uploadID
	partsMap, err := m.cache.HGetAll(ctx, partsKey)
	if err != nil {
		return nil, fmt.Errorf("get parts: %w", err)
	}
	if len(partsMap) == 0 {
		return nil, fmt.Errorf("no parts uploaded for session %s", uploadID)
	}

	// Parse and sort parts by number
	parts := make([]PartInfo, 0, len(partsMap))
	for _, v := range partsMap {
		var p PartInfo
		if err := json.Unmarshal([]byte(v), &p); err != nil {
			continue
		}
		parts = append(parts, p)
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNum < parts[j].PartNum })

	// Create a multi-reader that concatenates all parts from MinIO
	readers := make([]io.Reader, len(parts))
	closers := make([]io.ReadCloser, len(parts))
	for i, p := range parts {
		rc, _, err := m.tempStore.GetChunk(ctx, p.Key)
		if err != nil {
			// Close any already-opened readers
			for j := 0; j < i; j++ {
				closers[j].Close()
			}
			return nil, fmt.Errorf("read part %d: %w", p.PartNum, err)
		}
		closers[i] = rc
		readers[i] = rc
	}
	defer func() {
		for _, c := range closers {
			if c != nil {
				c.Close()
			}
		}
	}()
	combinedReader := io.MultiReader(readers...)

	// Feed through standard upload pipeline
	resp, err := m.fileSvc.Upload(ctx, session.UserID, session.FileName, session.MimeType, combinedReader)
	if err != nil {
		return nil, fmt.Errorf("process upload: %w", err)
	}

	// Cleanup: delete temp parts and session
	go m.cleanup(context.Background(), uploadID, parts)

	m.logger.Info("Multipart upload completed",
		zap.String("upload_id", uploadID),
		zap.String("file_id", resp.ID),
		zap.Int("parts", len(parts)),
	)
	return resp, nil
}

// AbortUpload cancels an in-progress upload, cleaning up all temp parts.
func (m *MultipartService) AbortUpload(ctx context.Context, uploadID, userID string) error {
	session, err := m.getSession(ctx, uploadID)
	if err != nil {
		return err
	}
	if session == nil {
		return fmt.Errorf("upload session not found: %s", uploadID)
	}
	if session.UserID != userID {
		return fmt.Errorf("upload session not owned by user")
	}

	// Get parts for cleanup
	partsKey := multipartPrefix + multipartPartsKey + uploadID
	partsMap, err := m.cache.HGetAll(ctx, partsKey)
	if err != nil {
		return fmt.Errorf("get parts for cleanup: %w", err)
	}

	var parts []PartInfo
	for _, v := range partsMap {
		var p PartInfo
		if err := json.Unmarshal([]byte(v), &p); err != nil {
			continue
		}
		parts = append(parts, p)
	}

	m.cleanup(ctx, uploadID, parts)
	m.logger.Info("Multipart upload aborted", zap.String("upload_id", uploadID))
	return nil
}

// getSession retrieves and deserializes a multipart upload session from Redis.
func (m *MultipartService) getSession(ctx context.Context, uploadID string) (*MultipartSession, error) {
	key := multipartPrefix + uploadID
	data, err := m.cache.Get(ctx, key)
	if err != nil {
		return nil, nil // Key doesn't exist = session expired/not found
	}

	var session MultipartSession
	if err := json.Unmarshal([]byte(data), &session); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &session, nil
}

// cleanup removes temp parts from MinIO and session/parts from Redis.
func (m *MultipartService) cleanup(ctx context.Context, uploadID string, parts []PartInfo) {
	for _, p := range parts {
		if err := m.tempStore.DeleteChunk(ctx, p.Key); err != nil {
			m.logger.Warn("Failed to cleanup temp part",
				zap.String("key", p.Key),
				zap.Error(err),
			)
		}
	}
	_ = m.cache.Delete(ctx, multipartPrefix+uploadID)
	_ = m.cache.Delete(ctx, multipartPrefix+multipartPartsKey+uploadID)
}
