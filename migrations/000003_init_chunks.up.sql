CREATE TABLE IF NOT EXISTS chunks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    hash          VARCHAR(64)  UNIQUE NOT NULL,
    size          BIGINT       NOT NULL,
    ref_count     INTEGER      NOT NULL DEFAULT 1,
    storage_nodes TEXT[]       NOT NULL DEFAULT '{}',
    bucket        VARCHAR(63)  NOT NULL,
    object_key    VARCHAR(512) NOT NULL,
    status        VARCHAR(20)  NOT NULL DEFAULT 'active',
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_chunks_hash      ON chunks(hash);
CREATE INDEX idx_chunks_status    ON chunks(status);
CREATE INDEX idx_chunks_orphaned  ON chunks(ref_count) WHERE ref_count = 0 AND status = 'active';
