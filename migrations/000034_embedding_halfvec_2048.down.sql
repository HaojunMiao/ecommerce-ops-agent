DROP INDEX IF EXISTS kb_chunks_embedding_hnsw;

ALTER TABLE kb_chunks
    ALTER COLUMN embedding TYPE vector(1536)
    USING NULL::vector(1536);

CREATE INDEX kb_chunks_embedding_hnsw
    ON kb_chunks USING hnsw (embedding vector_cosine_ops);

UPDATE kb_documents
SET status = 'pending', ingested_at = NULL;
