package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ── Folder Operations ───────────────────────────────────────

// CreateDirectory creates a folder in the file hierarchy.
// Folders are files with is_directory=true and size=0.
func (r *PgxRepository) CreateDirectory(ctx context.Context, userID, name string, parentID *string) (*File, error) {
	var f File
	err := r.pool.QueryRow(ctx,
		`INSERT INTO files (user_id, parent_id, name, is_directory, size, status)
		 VALUES ($1, $2, $3, true, 0, 'active')
		 RETURNING id, user_id, parent_id, name, is_directory, size, mime_type,
		           checksum, version, status, created_at, updated_at`,
		userID, parentID, name,
	).Scan(&f.ID, &f.UserID, &f.ParentID, &f.Name, &f.IsDirectory, &f.Size,
		&f.MimeType, &f.Checksum, &f.Version, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}
	return &f, nil
}

// GetFolderContents lists all direct children of a folder (files + subfolders).
// If folderID is nil, lists root-level items for the user.
func (r *PgxRepository) GetFolderContents(ctx context.Context, userID string, folderID *string, opts ListOptions) ([]*File, int64, error) {
	if opts.PageSize <= 0 {
		opts.PageSize = 50
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	offset := (opts.Page - 1) * opts.PageSize

	// Count query
	var total int64
	var countQuery string
	var countArgs []interface{}

	if folderID == nil {
		countQuery = `SELECT COUNT(*) FROM files WHERE user_id = $1 AND parent_id IS NULL AND status = 'active'`
		countArgs = []interface{}{userID}
	} else {
		countQuery = `SELECT COUNT(*) FROM files WHERE user_id = $1 AND parent_id = $2 AND status = 'active'`
		countArgs = []interface{}{userID, *folderID}
	}

	if err := r.pool.QueryRow(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count folder contents: %w", err)
	}

	// Fetch query: directories first, then files, alphabetical within each
	var listQuery string
	var listArgs []interface{}

	if folderID == nil {
		listQuery = `SELECT id, user_id, parent_id, name, is_directory, size, mime_type,
		                    checksum, version, status, created_at, updated_at
		             FROM files
		             WHERE user_id = $1 AND parent_id IS NULL AND status = 'active'
		             ORDER BY is_directory DESC, name ASC
		             LIMIT $2 OFFSET $3`
		listArgs = []interface{}{userID, opts.PageSize, offset}
	} else {
		listQuery = `SELECT id, user_id, parent_id, name, is_directory, size, mime_type,
		                    checksum, version, status, created_at, updated_at
		             FROM files
		             WHERE user_id = $1 AND parent_id = $2 AND status = 'active'
		             ORDER BY is_directory DESC, name ASC
		             LIMIT $3 OFFSET $4`
		listArgs = []interface{}{userID, *folderID, opts.PageSize, offset}
	}

	rows, err := r.pool.Query(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list folder contents: %w", err)
	}
	defer rows.Close()

	var files []*File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.UserID, &f.ParentID, &f.Name, &f.IsDirectory,
			&f.Size, &f.MimeType, &f.Checksum, &f.Version, &f.Status,
			&f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan folder item: %w", err)
		}
		files = append(files, &f)
	}
	return files, total, nil
}

// MoveFile moves a file or folder to a new parent directory.
// Before moving, callers must verify the move won't create a circular reference
// using IsDescendant.
func (r *PgxRepository) MoveFile(ctx context.Context, userID, fileID string, newParentID *string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE files SET parent_id = $1, updated_at = NOW()
		 WHERE id = $2 AND user_id = $3 AND status = 'active'`,
		newParentID, fileID, userID,
	)
	if err != nil {
		return fmt.Errorf("move file: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("file not found or not owned by user")
	}
	return nil
}

// IsDescendant checks if potentialChild is a descendant of potentialParent
// in the folder hierarchy. Used to prevent circular references when moving.
// Uses a recursive CTE that walks up the tree from potentialChild.
func (r *PgxRepository) IsDescendant(ctx context.Context, potentialChild, potentialParent string) (bool, error) {
	var isDescendant bool
	err := r.pool.QueryRow(ctx,
		`WITH RECURSIVE ancestors AS (
			SELECT id, parent_id FROM files WHERE id = $1
			UNION ALL
			SELECT f.id, f.parent_id FROM files f
			JOIN ancestors a ON f.id = a.parent_id
		)
		SELECT EXISTS (SELECT 1 FROM ancestors WHERE id = $2)`,
		potentialChild, potentialParent,
	).Scan(&isDescendant)
	if err != nil {
		return false, fmt.Errorf("check descendant: %w", err)
	}
	return isDescendant, nil
}

// GetAllDescendantFileIDs returns the IDs of all files (not directories)
// that are descendants of the given folder. Used for cascade delete to
// decrement chunk ref_counts properly.
func (r *PgxRepository) GetAllDescendantFileIDs(ctx context.Context, folderID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`WITH RECURSIVE tree AS (
			SELECT id, is_directory FROM files WHERE parent_id = $1 AND status = 'active'
			UNION ALL
			SELECT f.id, f.is_directory FROM files f
			JOIN tree t ON f.parent_id = t.id
			WHERE f.status = 'active'
		)
		SELECT id FROM tree WHERE is_directory = false`,
		folderID,
	)
	if err != nil {
		return nil, fmt.Errorf("get descendant files: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan descendant file id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// VerifyFolderOwnership confirms a folder exists and belongs to the user.
func (r *PgxRepository) VerifyFolderOwnership(ctx context.Context, userID, folderID string) (*File, error) {
	var f File
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, parent_id, name, is_directory, size, mime_type,
		        checksum, version, status, created_at, updated_at
		 FROM files WHERE id = $1 AND user_id = $2 AND is_directory = true AND status = 'active'`,
		folderID, userID,
	).Scan(&f.ID, &f.UserID, &f.ParentID, &f.Name, &f.IsDirectory, &f.Size,
		&f.MimeType, &f.Checksum, &f.Version, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("verify folder: %w", err)
	}
	return &f, nil
}
