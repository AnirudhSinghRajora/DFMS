CREATE TABLE IF NOT EXISTS storage_nodes (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name           VARCHAR(100) UNIQUE NOT NULL,
    endpoint       VARCHAR(255) NOT NULL,
    region         VARCHAR(50)  DEFAULT 'default',
    capacity       BIGINT       NOT NULL,
    used           BIGINT       NOT NULL DEFAULT 0,
    weight         INTEGER      NOT NULL DEFAULT 100,
    status         VARCHAR(20)  NOT NULL DEFAULT 'healthy',
    last_heartbeat TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_storage_nodes_status ON storage_nodes(status);
