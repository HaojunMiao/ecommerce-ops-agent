-- 000034_embedding_halfvec_2048:真实语义向量统一使用 2048 维。
-- 2048 超过 pgvector 的 vector HNSW 2000 维上限，因此使用 halfvec。

DROP INDEX IF EXISTS kb_chunks_embedding_hnsw;

ALTER TABLE kb_chunks
    ALTER COLUMN embedding TYPE halfvec(2048)
    USING NULL::halfvec(2048);

CREATE INDEX kb_chunks_embedding_hnsw
    ON kb_chunks USING hnsw (embedding halfvec_cosine_ops);

-- 旧向量与新模型不在同一向量空间，不能转换或混用。保留原文与关键词索引，
-- 将文档标记为待处理；下次 Connector 同步会重新生成真实语义向量。
UPDATE kb_documents
SET status = 'pending', ingested_at = NULL;
