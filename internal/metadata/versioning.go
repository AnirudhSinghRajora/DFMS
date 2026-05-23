package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ── Versioning ──────────────────────────────────────────────

// GetLatestFileByName returns the latest active version of a file with
// the given name under the specified parent directory.
// Returns nil, nil if no file with that name exists.
func (r *PgxRepository) GetLatestFileByName(ctx context.Context, userID string, parentID *string, name string) (*File, error) {
	var f File
	var query string
	var args []interface{}

	if parentID == nil {
		query = `SELECT id, user_id, parent_id, name, is_directory, size, mime_type,
		                checksum, version, status, created_at, updated_at
		         FROM files
		         WHERE user_id = $1 AND name = $2 AND parent_id IS NULL
		               AND status IN ('active', 'superseded')
		         ORDER BY version DESC LIMIT 1`
		args = []interface{}{userID, name}
	} else {
		query = `SELECT id, user_id, parent_id, name, is_directory, size, mime_type,
		                checksum, version, status, created_at, updated_at
		         FROM files
		         WHERE user_id = $1 AND name = $2 AND parent_id = $3
		               AND status IN ('active', 'superseded')
		         ORDER BY version DESC LIMIT 1`
		args = []interface{}{userID, name, *parentID}
	}

	err := r.pool.QueryRow(ctx, query, args...).Scan(
		&f.ID, &f.UserID, &f.ParentID, &f.Name, &f.IsDirectory, &f.Size,
		&f.MimeType, &f.Checksum, &f.Version, &f.Status, &f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get latest file by name: %w", err)
	}
	return &f, nil
}

// ListFileVersions returns all versions of a file with the given name
// belonging to the specified user, ordered from newest to oldest.
func (r *PgxRepository) ListFileVersions(ctx context.Context, userID, fileName string, parentID *string) ([]*File, error) {
	var query string
	var args []interface{}

	if parentID == nil {
		query = `SELECT id, user_id, parent_id, name, is_directory, size, mime_type,
		                checksum, version, status, created_at, updated_at
		         FROM files
		         WHERE user_id = $1 AND name = $2 AND parent_id IS NULL
		               AND status IN ('active', 'superseded')
		         ORDER BY version DESC`
		args = []interface{}{userID, fileName}
	} else {
		query = `SELECT id, user_id, parent_id, name, is_directory, size, mime_type,
		                checksum, version, status, created_at, updated_at
		         FROM files
		         WHERE user_id = $1 AND name = $2 AND parent_id = $3
		               AND status IN ('active', 'superseded')
		         ORDER BY version DESC`
		args = []interface{}{userID, fileName, *parentID}
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list file versions: %w", err)
	}
	defer rows.Close()

	var files []*File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.UserID, &f.ParentID, &f.Name, &f.IsDirectory,
			&f.Size, &f.MimeType, &f.Checksum, &f.Version, &f.Status,
			&f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan file version: %w", err)
		}
		files = append(files, &f)
	}
	return files, nil
}

// UpdateFileStatus updates a file's status (e.g., active → superseded → deleted).
func (r *PgxRepository) UpdateFileStatus(ctx context.Context, fileID, status string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE files SET status = $1, updated_at = NOW() WHERE id = $2`,
		status, fileID,
	)
	if err != nil {
		return fmt.Errorf("update file status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("file not found: %s", fileID)
	}
	return nil
}

// CreateFileWithVersion inserts a new file record with a specific version number.
// This is used for versioning: the caller provides the next version number.
func (r *PgxRepository) CreateFileWithVersion(ctx context.Context, params CreateFileParams, parentID *string, version int) (*File, error) {
	var f File
	err := r.pool.QueryRow(ctx,
		`INSERT INTO files (user_id, parent_id, name, size, mime_type, checksum, version, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 'active')
		 RETURNING id, user_id, parent_id, name, is_directory, size, mime_type,
		           checksum, version, status, created_at, updated_at`,
		params.UserID, parentID, params.Name, params.Size, params.MimeType, params.Checksum, version,
	).Scan(&f.ID, &f.UserID, &f.ParentID, &f.Name, &f.IsDirectory, &f.Size,
		&f.MimeType, &f.Checksum, &f.Version, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create file with version: %w", err)
	}
	return &f, nil
}

// GetFileVersion returns a specific version of a file by its ID and version number.
// This allows downloading any version, not just the latest.
func (r *PgxRepository) GetFileVersion(ctx context.Context, userID, fileID string, version int) (*File, error) {
	// First get the file to find its name
	var name string
	var parentID *string
	err := r.pool.QueryRow(ctx,
		`SELECT name, parent_id FROM files WHERE id = $1 AND user_id = $2`,
		fileID, userID,
	).Scan(&name, &parentID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get file name: %w", err)
	}

	// Then find the specific version by name + version number
	var f File
	var query string
	var args []interface{}

	if parentID == nil {
		query = `SELECT id, user_id, parent_id, name, is_directory, size, mime_type,
		                checksum, version, status, created_at, updated_at
		         FROM files
		         WHERE user_id = $1 AND name = $2 AND parent_id IS NULL AND version = $3
		               AND status IN ('active', 'superseded')`
		args = []interface{}{userID, name, version}
	} else {
		query = `SELECT id, user_id, parent_id, name, is_directory, size, mime_type,
		                checksum, version, status, created_at, updated_at
		         FROM files
		         WHERE user_id = $1 AND name = $2 AND parent_id = $3 AND version = $4
		               AND status IN ('active', 'superseded')`
		args = []interface{}{userID, name, *parentID, version}
	}

	err = r.pool.QueryRow(ctx, query, args...).Scan(
		&f.ID, &f.UserID, &f.ParentID, &f.Name, &f.IsDirectory, &f.Size,
		&f.MimeType, &f.Checksum, &f.Version, &f.Status, &f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get file version: %w", err)
	}
	return &f, nil
}
