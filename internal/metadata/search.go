package metadata

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SearchQuery holds all the parameters for a file search request.
type SearchQuery struct {
	Query    string     // Text query (searched against file name via ILIKE)
	MimeType string     // Filter by mime_type (exact match)
	MinSize  *int64     // Filter: size >= min_size
	MaxSize  *int64     // Filter: size <= max_size
	After    *time.Time // Filter: created_at >= after
	Before   *time.Time // Filter: created_at <= before
	Page     int
	PageSize int
}

// SearchFiles performs a full-text search on file names with optional filters.
// Uses PostgreSQL's pg_trgm GIN index for efficient ILIKE matching.
// Results are paginated and only return active files (not directories).
func (r *PgxRepository) SearchFiles(ctx context.Context, userID string, q *SearchQuery) ([]*File, int64, error) {
	if q.PageSize <= 0 {
		q.PageSize = 20
	}
	if q.Page <= 0 {
		q.Page = 1
	}
	offset := (q.Page - 1) * q.PageSize

	// Build dynamic WHERE clause with parameterized queries
	var conditions []string
	var args []interface{}
	argIdx := 1

	// Base conditions: user ownership, active status, not directories
	conditions = append(conditions, fmt.Sprintf("user_id = $%d", argIdx))
	args = append(args, userID)
	argIdx++

	conditions = append(conditions, "status = 'active'", "is_directory = false")

	// Text search on name (uses pg_trgm GIN index)
	if q.Query != "" {
		conditions = append(conditions, fmt.Sprintf("name ILIKE $%d", argIdx))
		args = append(args, "%"+q.Query+"%")
		argIdx++
	}

	// MIME type filter
	if q.MimeType != "" {
		conditions = append(conditions, fmt.Sprintf("mime_type = $%d", argIdx))
		args = append(args, q.MimeType)
		argIdx++
	}

	// Size range filters
	if q.MinSize != nil {
		conditions = append(conditions, fmt.Sprintf("size >= $%d", argIdx))
		args = append(args, *q.MinSize)
		argIdx++
	}
	if q.MaxSize != nil {
		conditions = append(conditions, fmt.Sprintf("size <= $%d", argIdx))
		args = append(args, *q.MaxSize)
		argIdx++
	}

	// Date range filters
	if q.After != nil {
		conditions = append(conditions, fmt.Sprintf("created_at >= $%d", argIdx))
		args = append(args, *q.After)
		argIdx++
	}
	if q.Before != nil {
		conditions = append(conditions, fmt.Sprintf("created_at <= $%d", argIdx))
		args = append(args, *q.Before)
		argIdx++
	}

	whereClause := strings.Join(conditions, " AND ")

	// Count total matches
	var total int64
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM files WHERE %s", whereClause)
	if err := r.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("search count: %w", err)
	}

	// Fetch page of results
	selectQuery := fmt.Sprintf(
		`SELECT id, user_id, parent_id, name, is_directory, size, mime_type,
		        checksum, version, status, created_at, updated_at
		 FROM files WHERE %s
		 ORDER BY created_at DESC
		 LIMIT $%d OFFSET $%d`,
		whereClause, argIdx, argIdx+1,
	)
	args = append(args, q.PageSize, offset)

	rows, err := r.pool.Query(ctx, selectQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var files []*File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.UserID, &f.ParentID, &f.Name, &f.IsDirectory,
			&f.Size, &f.MimeType, &f.Checksum, &f.Version, &f.Status,
			&f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan search result: %w", err)
		}
		files = append(files, &f)
	}
	return files, total, nil
}
