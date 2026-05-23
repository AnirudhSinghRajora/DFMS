-- Enable trigram extension for fuzzy text search (must be before GIN index)
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE IF NOT EXISTS files (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_id     UUID         REFERENCES files(id) ON DELETE CASCADE,
    name          VARCHAR(512) NOT NULL,
    is_directory  BOOLEAN      NOT NULL DEFAULT false,
    size          BIGINT       NOT NULL DEFAULT 0,
    mime_type     VARCHAR(127),
    checksum      VARCHAR(64),
    version       INTEGER      NOT NULL DEFAULT 1,
    status        VARCHAR(20)  NOT NULL DEFAULT 'active',
    metadata      JSONB        DEFAULT '{}',
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_files_user_parent_name_version UNIQUE(user_id, parent_id, name, version)
);

CREATE INDEX idx_files_user_id   ON files(user_id);
CREATE INDEX idx_files_parent_id ON files(parent_id);
CREATE INDEX idx_files_status    ON files(status);
CREATE INDEX idx_files_checksum  ON files(checksum) WHERE checksum IS NOT NULL;
CREATE INDEX idx_files_name_gin  ON files USING gin(name gin_trgm_ops);
