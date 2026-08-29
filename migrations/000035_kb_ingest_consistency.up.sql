-- 000035_kb_ingest_consistency:防止异步旧任务与嵌入模型切换污染当前索引。

ALTER TABLE kb_documents
    ADD COLUMN embedding_identity TEXT NOT NULL DEFAULT '';

ALTER TABLE kb_chunks
    ADD COLUMN embedding_identity TEXT NOT NULL DEFAULT '';

CREATE INDEX kb_chunks_kb_embedding_identity
    ON kb_chunks (kb_id, embedding_identity);

-- 历史行没有足够信息证明来自当前嵌入空间。将已有 KB 置为 indexing，
-- 由下一次 Connector 同步按当前完整嵌入器身份重建；检索会排除旧 chunks。
UPDATE kb_documents
SET status = 'pending', ingested_at = NULL;

UPDATE kbs
SET status = 'indexing', updated_at = now()
WHERE EXISTS (SELECT 1 FROM kb_documents d WHERE d.kb_id = kbs.id);
