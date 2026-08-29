DROP INDEX kb_chunks_tsv_gin;
ALTER TABLE kb_chunks DROP COLUMN tsv;
ALTER TABLE kb_chunks
    ADD COLUMN tsv TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED;
CREATE INDEX kb_chunks_tsv_gin ON kb_chunks USING gin (tsv);

ALTER TABLE kb_chunks DROP COLUMN search_text;
