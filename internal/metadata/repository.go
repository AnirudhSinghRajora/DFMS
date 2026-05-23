// Package metadata provides PostgreSQL-backed file and chunk metadata management.
// This package lives in the API Gateway and handles all relational data
// operations while the ChunkService handles raw byte storage.
package metadata

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── Domain Types ────────────────────────────────────────────

// File represents a stored file's metadata.
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

// Chunk represents a content-addressed chunk stored in MinIO.
type Chunk struct {
	ID       string `json:"id"`
	Hash     string `json:"hash"`
	Size     int64  `json:"size"`
	RefCount int    `json:"ref_count"`
	Status   string `json:"status"`
}

// ManifestEntry represents a single entry in a file's chunk manifest.
type ManifestEntry struct {
	ChunkHash  string `json:"chunk_hash"`
	ChunkSize  int64  `json:"chunk_size"`
	ChunkIndex int    `json:"chunk_index"`
	ByteOffset int64  `json:"byte_offset"`
}

// CreateFileParams holds parameters for creating a new file record.
type CreateFileParams struct {
	UserID   string
	Name     string
	Size     int64
	MimeType string
	Checksum string
}

// ListOptions holds pagination and filtering for file listing.
type ListOptions struct {
	Page     int
	PageSize int
	Status   string // Filter by status (default: "active")
}

// ── Repository Interface ────────────────────────────────────

// Repository defines all database operations for file/chunk metadata.
type Repository interface {
	// Files
	CreateFile(ctx context.Context, params CreateFileParams) (*File, error)
	GetFileByID(ctx context.Context, userID, fileID string) (*File, error)
	ListFilesByUser(ctx context.Context, userID string, opts ListOptions) ([]*File, int64, error)
	SoftDeleteFile(ctx context.Context, userID, fileID string) error

	// Chunks
	UpsertChunk(ctx context.Context, hash string, size int64) (*Chunk, error)
	GetChunksByHashes(ctx context.Context, hashes []string) (map[string]*Chunk, error)
	IncrementRefCount(ctx context.Context, hash string) error
	DecrementRefCounts(ctx context.Context, hashes []string) ([]string, error)

	// Manifests
	SaveManifest(ctx context.Context, fileID string, entries []ManifestEntry) error
	GetManifest(ctx context.Context, fileID string) ([]ManifestEntry, error)

	// Quota
	GetStorageUsage(ctx context.Context, userID string) (used, quota int64, err error)
	UpdateStorageUsed(ctx context.Context, userID string, delta int64) error

	// Versioning
	GetLatestFileByName(ctx context.Context, userID string, parentID *string, name string) (*File, error)
	ListFileVersions(ctx context.Context, userID, fileName string, parentID *string) ([]*File, error)
	CreateFileWithVersion(ctx context.Context, params CreateFileParams, parentID *string, version int) (*File, error)
	GetFileVersion(ctx context.Context, userID, fileID string, version int) (*File, error)
	UpdateFileStatus(ctx context.Context, fileID, status string) error

	// Folders
	CreateDirectory(ctx context.Context, userID, name string, parentID *string) (*File, error)
	GetFolderContents(ctx context.Context, userID string, folderID *string, opts ListOptions) ([]*File, int64, error)
	MoveFile(ctx context.Context, userID, fileID string, newParentID *string) error
	IsDescendant(ctx context.Context, potentialChild, potentialParent string) (bool, error)
	GetAllDescendantFileIDs(ctx context.Context, folderID string) ([]string, error)
	VerifyFolderOwnership(ctx context.Context, userID, folderID string) (*File, error)

	// Search
	SearchFiles(ctx context.Context, userID string, q *SearchQuery) ([]*File, int64, error)
}

// ── PostgreSQL Implementation ───────────────────────────────

// PgxRepository implements Repository using pgx connection pool.
type PgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository creates a new PostgreSQL-backed repository.
func NewPgxRepository(pool *pgxpool.Pool) *PgxRepository {
	return &PgxRepository{pool: pool}
}

func (r *PgxRepository) CreateFile(ctx context.Context, params CreateFileParams) (*File, error) {
	var f File
	err := r.pool.QueryRow(ctx,
		`INSERT INTO files (user_id, name, size, mime_type, checksum, status)
		 VALUES ($1, $2, $3, $4, $5, 'active')
		 RETURNING id, user_id, parent_id, name, is_directory, size, mime_type,
		           checksum, version, status, created_at, updated_at`,
		params.UserID, params.Name, params.Size, params.MimeType, params.Checksum,
	).Scan(&f.ID, &f.UserID, &f.ParentID, &f.Name, &f.IsDirectory, &f.Size,
		&f.MimeType, &f.Checksum, &f.Version, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create file: %w", err)
	}
	return &f, nil
}

func (r *PgxRepository) GetFileByID(ctx context.Context, userID, fileID string) (*File, error) {
	var f File
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, parent_id, name, is_directory, size, mime_type,
		        checksum, version, status, created_at, updated_at
		 FROM files WHERE id = $1 AND user_id = $2 AND status = 'active'`,
		fileID, userID,
	).Scan(&f.ID, &f.UserID, &f.ParentID, &f.Name, &f.IsDirectory, &f.Size,
		&f.MimeType, &f.Checksum, &f.Version, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("get file: %w", err)
	}
	return &f, nil
}

func (r *PgxRepository) ListFilesByUser(ctx context.Context, userID string, opts ListOptions) ([]*File, int64, error) {
	if opts.PageSize <= 0 {
		opts.PageSize = 20
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	status := "active"
	if opts.Status != "" {
		status = opts.Status
	}
	offset := (opts.Page - 1) * opts.PageSize

	// Get total count
	var total int64
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM files WHERE user_id = $1 AND status = $2`,
		userID, status,
	).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count files: %w", err)
	}

	// Get page
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, parent_id, name, is_directory, size, mime_type,
		        checksum, version, status, created_at, updated_at
		 FROM files WHERE user_id = $1 AND status = $2
		 ORDER BY updated_at DESC
		 LIMIT $3 OFFSET $4`,
		userID, status, opts.PageSize, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list files: %w", err)
	}
	defer rows.Close()

	var files []*File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.UserID, &f.ParentID, &f.Name, &f.IsDirectory,
			&f.Size, &f.MimeType, &f.Checksum, &f.Version, &f.Status,
			&f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan file: %w", err)
		}
		files = append(files, &f)
	}

	return files, total, nil
}

func (r *PgxRepository) SoftDeleteFile(ctx context.Context, userID, fileID string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE files SET status = 'deleted', updated_at = NOW()
		 WHERE id = $1 AND user_id = $2 AND status = 'active'`,
		fileID, userID,
	)
	if err != nil {
		return fmt.Errorf("soft delete file: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("file not found or already deleted")
	}
	return nil
}

// UpsertChunk inserts a new chunk or returns the existing one if the hash
// already exists. If it already exists, the ref_count is incremented.
func (r *PgxRepository) UpsertChunk(ctx context.Context, hash string, size int64) (*Chunk, error) {
	var c Chunk
	err := r.pool.QueryRow(ctx,
		`INSERT INTO chunks (hash, size, ref_count, bucket, object_key, status)
		 VALUES ($1, $2, 1, 'chunks', $3, 'active')
		 ON CONFLICT (hash) DO UPDATE SET ref_count = chunks.ref_count + 1
		 RETURNING id, hash, size, ref_count, status`,
		hash, size, chunkObjectKey(hash),
	).Scan(&c.ID, &c.Hash, &c.Size, &c.RefCount, &c.Status)
	if err != nil {
		return nil, fmt.Errorf("upsert chunk: %w", err)
	}
	return &c, nil
}

func (r *PgxRepository) GetChunksByHashes(ctx context.Context, hashes []string) (map[string]*Chunk, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, hash, size, ref_count, status
		 FROM chunks WHERE hash = ANY($1)`,
		hashes,
	)
	if err != nil {
		return nil, fmt.Errorf("get chunks by hashes: %w", err)
	}
	defer rows.Close()

	result := make(map[string]*Chunk)
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.ID, &c.Hash, &c.Size, &c.RefCount, &c.Status); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		result[c.Hash] = &c
	}
	return result, nil
}

func (r *PgxRepository) IncrementRefCount(ctx context.Context, hash string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE chunks SET ref_count = ref_count + 1 WHERE hash = $1`,
		hash,
	)
	return err
}

// DecrementRefCounts decrements ref_count for all given hashes and returns
// the hashes that reached ref_count = 0 (candidates for GC).
func (r *PgxRepository) DecrementRefCounts(ctx context.Context, hashes []string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`UPDATE chunks SET ref_count = ref_count - 1
		 WHERE hash = ANY($1) AND ref_count > 0
		 RETURNING hash, ref_count`,
		hashes,
	)
	if err != nil {
		return nil, fmt.Errorf("decrement ref counts: %w", err)
	}
	defer rows.Close()

	var orphaned []string
	for rows.Next() {
		var hash string
		var refCount int
		if err := rows.Scan(&hash, &refCount); err != nil {
			return nil, fmt.Errorf("scan ref count: %w", err)
		}
		if refCount == 0 {
			orphaned = append(orphaned, hash)
		}
	}
	return orphaned, nil
}

// SaveManifest inserts the chunk manifest (file_chunks) for a file.
// This uses a single batch insert for efficiency.
func (r *PgxRepository) SaveManifest(ctx context.Context, fileID string, entries []ManifestEntry) error {
	if len(entries) == 0 {
		return nil
	}

	// Use pgx batch for efficient multi-row insert
	batch := &pgx.Batch{}
	for _, e := range entries {
		batch.Queue(
			`INSERT INTO file_chunks (file_id, chunk_id, chunk_index, byte_offset)
			 SELECT $1, c.id, $2, $3
			 FROM chunks c WHERE c.hash = $4`,
			fileID, e.ChunkIndex, e.ByteOffset, e.ChunkHash,
		)
	}

	br := r.pool.SendBatch(ctx, batch)
	defer br.Close()

	for range entries {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("insert manifest entry: %w", err)
		}
	}

	return nil
}

func (r *PgxRepository) GetManifest(ctx context.Context, fileID string) ([]ManifestEntry, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT c.hash, c.size, fc.chunk_index, fc.byte_offset
		 FROM file_chunks fc
		 JOIN chunks c ON c.id = fc.chunk_id
		 WHERE fc.file_id = $1
		 ORDER BY fc.chunk_index ASC`,
		fileID,
	)
	if err != nil {
		return nil, fmt.Errorf("get manifest: %w", err)
	}
	defer rows.Close()

	var entries []ManifestEntry
	for rows.Next() {
		var e ManifestEntry
		if err := rows.Scan(&e.ChunkHash, &e.ChunkSize, &e.ChunkIndex, &e.ByteOffset); err != nil {
			return nil, fmt.Errorf("scan manifest entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (r *PgxRepository) GetStorageUsage(ctx context.Context, userID string) (used int64, quota int64, err error) {
	err = r.pool.QueryRow(ctx,
		`SELECT storage_used, storage_quota FROM users WHERE id = $1`,
		userID,
	).Scan(&used, &quota)
	if err != nil {
		return 0, 0, fmt.Errorf("get storage usage: %w", err)
	}
	return used, quota, nil
}

func (r *PgxRepository) UpdateStorageUsed(ctx context.Context, userID string, delta int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET storage_used = storage_used + $1, updated_at = NOW()
		 WHERE id = $2`,
		delta, userID,
	)
	return err
}

// chunkObjectKey generates the MinIO object key from a chunk hash.
func chunkObjectKey(hash string) string {
	if len(hash) < 4 {
		return hash
	}
	return fmt.Sprintf("%s/%s/%s", hash[0:2], hash[2:4], hash)
}
