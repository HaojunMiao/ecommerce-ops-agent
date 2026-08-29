DROP INDEX IF EXISTS kb_chunks_kb_embedding_identity;

ALTER TABLE kb_chunks
    DROP COLUMN IF EXISTS embedding_identity;

ALTER TABLE kb_documents
    DROP COLUMN IF EXISTS embedding_identity;
