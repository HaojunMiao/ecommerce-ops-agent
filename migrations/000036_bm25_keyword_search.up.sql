-- 000036_bm25_keyword_search: replace ts_rank_cd ranking with a real BM25 index.
-- search_text is already tokenized by Go/GSE; whitespace tokenization preserves
-- exactly those terms for both indexing and querying.

CREATE EXTENSION IF NOT EXISTS pg_search;

CREATE INDEX kb_chunks_search_bm25
    ON kb_chunks
    USING bm25 (id, (search_text::pdb.whitespace('alias=search_text')))
    WITH (key_field = 'id');

-- Keep the legacy tsv/GIN objects for one release as a rollback safety net.
-- Runtime queries no longer use them; a later migration may remove them after
-- the BM25 path has been exercised in the target environment.
