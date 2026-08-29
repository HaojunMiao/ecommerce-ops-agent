-- 000033_chinese_lexical_search:由 Go/GSE 统一完成中文分词，PostgreSQL 只负责词元索引与排名。

ALTER TABLE kb_chunks
    ADD COLUMN search_text TEXT NOT NULL DEFAULT '';

-- 旧数据先保留原文作为兼容兜底；连接器下次同步时会根据新的索引管道指纹
-- 自动重新 ingest，并写入 GSE 分词后的 search_text。
UPDATE kb_chunks SET search_text = content;

DROP INDEX kb_chunks_tsv_gin;
ALTER TABLE kb_chunks DROP COLUMN tsv;
ALTER TABLE kb_chunks
    ADD COLUMN tsv TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple', search_text)) STORED;
CREATE INDEX kb_chunks_tsv_gin ON kb_chunks USING gin (tsv);
