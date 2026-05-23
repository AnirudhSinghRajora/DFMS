CREATE TABLE IF NOT EXISTS file_chunks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id     UUID    NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    chunk_id    UUID    NOT NULL REFERENCES chunks(id),
    chunk_index INTEGER NOT NULL,
    byte_offset BIGINT  NOT NULL,

    CONSTRAINT uq_file_chunks_file_index UNIQUE(file_id, chunk_index)
);

CREATE INDEX idx_file_chunks_file_id  ON file_chunks(file_id);
CREATE INDEX idx_file_chunks_chunk_id ON file_chunks(chunk_id);
