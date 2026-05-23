-- Index for efficient version listing by file name.
-- Supports queries that look up all versions of a file by (user_id, name).
-- Partial index excludes deleted files to keep the index small.
CREATE INDEX IF NOT EXISTS idx_files_name_user ON files(user_id, name) WHERE status != 'deleted';
