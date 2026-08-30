package retriever

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Searcher 是 KB 检索/索引的抽象:内存版(*Retriever)与 pgvector 版(*PgvectorRetriever)都实现它。
// platform.NewService 按 db 是否存在选用:db != nil → pgvector(跨进程共享 + 重启持久);否则内存。
type Searcher interface {
	Index(ctx context.Context, kbID string, chunks []Chunk) error
	RemoveDocument(ctx context.Context, kbID, documentID string) error
	Search(ctx context.Context, kbID, query string, k int) ([]Passage, error)
	Embedder() Embedder
}

// VersionedIndexer 把“校验文档指纹、替换 chunks、标记 processed”放进同一个事务。
// 持久化检索器实现它，避免旧异步任务在新版本之后完成并覆盖索引。
type VersionedIndexer interface {
	IndexIfCurrent(
		ctx context.Context,
		kbID, documentID, fingerprint, embeddingIdentity string,
		chunks []Chunk,
	) (indexed bool, err error)
}

var _ Searcher = (*Retriever)(nil)         // 内存版
var _ Searcher = (*PgvectorRetriever)(nil) // PG 版

// PgvectorRetriever 把 chunk 落 kb_chunks(embedding halfvec + 中文分词 tsv),检索在 SQL 里做
// 全文关键词 + 向量(<=> 余弦) + RRF(k=60) 合并。跨进程闭环:worker ingest 写库,server 检索读库。
type PgvectorRetriever struct {
	db       *pgxpool.Pool
	embedder Embedder
	rrfK     int // 默认 60
}

// NewPgvectorRetriever 创建 SQL-backed 检索器。
func NewPgvectorRetriever(db *pgxpool.Pool, embedder Embedder) *PgvectorRetriever {
	return &PgvectorRetriever{db: db, embedder: embedder, rrfK: 60}
}

// beginVectorSearch 为带 kb_id 等过滤条件的 HNSW 查询开启迭代扫描。
// 默认 HNSW 先取全局近邻再应用过滤；多个 KB 共用索引时可能因候选均属于
// 其他 KB 而少返回甚至返回空。strict_order 会继续扫描直到过滤后结果足够。
func (r *PgvectorRetriever) beginVectorSearch(ctx context.Context) (pgx.Tx, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin vector search: %w", err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL hnsw.iterative_scan = strict_order`); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("enable filtered hnsw iterative scan: %w", err)
	}
	// 多个 KB 中存在相同或近重复文档时，默认 40 个入口候选和较小扫描
	// 内存仍可能在命中目标 KB 前停止。提高过滤查询的探索与扫描上限；设置
	// 仅在当前只读事务内生效，不影响写入和其他连接。
	for _, setting := range []string{
		`SET LOCAL hnsw.ef_search = 200`,
		`SET LOCAL hnsw.max_scan_tuples = 100000`,
		`SET LOCAL hnsw.scan_mem_multiplier = 4`,
	} {
		if _, err := tx.Exec(ctx, setting); err != nil {
			_ = tx.Rollback(ctx)
			return nil, fmt.Errorf("configure filtered hnsw scan: %w", err)
		}
	}
	return tx, nil
}

// Embedder 暴露嵌入器,供 ingest 的 embed 阶段复用同一实现。
func (r *PgvectorRetriever) Embedder() Embedder { return r.embedder }

// RemoveDocument 删除文档的持久化索引。控制面随后删除 kb_documents；外键级联是
// 第二道保障。拆成该方法是为了让内存与 PostgreSQL 检索器共用同一同步流程。
func (r *PgvectorRetriever) RemoveDocument(ctx context.Context, kbID, documentID string) error {
	kbUUID, err := uuid.Parse(kbID)
	if err != nil {
		return fmt.Errorf("parse kb id: %w", err)
	}
	docUUID, err := uuid.Parse(documentID)
	if err != nil {
		return fmt.Errorf("parse doc id: %w", err)
	}
	if _, err := r.db.Exec(ctx, `DELETE FROM kb_chunks WHERE kb_id = $1 AND doc_id = $2`, kbUUID, docUUID); err != nil {
		return fmt.Errorf("delete document chunks: %w", err)
	}
	return nil
}

// Index 把一份文档的全部 chunk 写入 kb_chunks。先删该 doc 旧 chunk 再插(幂等),整体在一个事务里。
func (r *PgvectorRetriever) Index(ctx context.Context, kbID string, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	kbUUID, err := uuid.Parse(kbID)
	if err != nil {
		return fmt.Errorf("parse kb id: %w", err)
	}
	docUUID, err := uuid.Parse(chunks[0].DocID)
	if err != nil {
		return fmt.Errorf("parse doc id: %w", err)
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // commit 成功后是 no-op

	if _, err := tx.Exec(ctx, `DELETE FROM kb_chunks WHERE doc_id = $1`, docUUID); err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}
	for _, c := range chunks {
		if _, err := tx.Exec(ctx,
			`INSERT INTO kb_chunks (id, kb_id, doc_id, ordinal, content, search_text, embedding, embedding_identity, classification)
			 VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6::halfvec, $7, 'internal')`,
			kbUUID, docUUID, c.Ordinal, c.Content, lexicalDocument(c.Content), vectorLiteral(c.Embedding), r.embedder.Identity()); err != nil {
			return fmt.Errorf("insert chunk %d: %w", c.Ordinal, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// IndexIfCurrent 用行锁把版本校验、索引替换和 processed 状态提交为一个原子操作。
// 指纹或嵌入身份已变化时返回 indexed=false，调用方把它当作过期任务正常丢弃。
func (r *PgvectorRetriever) IndexIfCurrent(
	ctx context.Context,
	kbID, documentID, fingerprint, embeddingIdentity string,
	chunks []Chunk,
) (bool, error) {
	kbUUID, err := uuid.Parse(kbID)
	if err != nil {
		return false, fmt.Errorf("parse kb id: %w", err)
	}
	docUUID, err := uuid.Parse(documentID)
	if err != nil {
		return false, fmt.Errorf("parse doc id: %w", err)
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin versioned index: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // commit 成功后是 no-op

	var exists bool
	err = tx.QueryRow(ctx, `
		SELECT true
		FROM kb_documents
		WHERE id = $1 AND kb_id = $2 AND hash = $3
		  AND embedding_identity = $4 AND status IN ('pending', 'error')
		FOR UPDATE`, docUUID, kbUUID, fingerprint, embeddingIdentity).Scan(&exists)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("lock current document: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM kb_chunks WHERE doc_id = $1`, docUUID); err != nil {
		return false, fmt.Errorf("delete old chunks: %w", err)
	}
	for _, c := range chunks {
		if _, err := tx.Exec(ctx,
			`INSERT INTO kb_chunks (id, kb_id, doc_id, ordinal, content, search_text, embedding, embedding_identity, classification)
			 VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6::halfvec, $7, 'internal')`,
			kbUUID, docUUID, c.Ordinal, c.Content, lexicalDocument(c.Content), vectorLiteral(c.Embedding), embeddingIdentity); err != nil {
			return false, fmt.Errorf("insert chunk %d: %w", c.Ordinal, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE kb_documents
		SET status = 'processed', ingested_at = now()
		WHERE id = $1 AND hash = $2 AND embedding_identity = $3`,
		docUUID, fingerprint, embeddingIdentity); err != nil {
		return false, fmt.Errorf("mark document processed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit versioned index: %w", err)
	}
	return true, nil
}

// Search 在 SQL 里跑中文关键词 + 向量 + RRF 合并,返回前 topK。
func (r *PgvectorRetriever) Search(ctx context.Context, kbID, query string, topK int) ([]Passage, error) {
	if topK <= 0 {
		topK = 5
	}
	kbUUID, err := uuid.Parse(kbID)
	if err != nil {
		return nil, fmt.Errorf("parse kb id: %w", err)
	}
	embs, err := r.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	qvec := vectorLiteral(embs[0])
	keywordQuery := lexicalQuery(query)

	tx, err := r.beginVectorSearch(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		WITH keyword AS (
			SELECT c.id, ROW_NUMBER() OVER (
				ORDER BY ts_rank_cd(c.tsv, websearch_to_tsquery('simple', $1)) DESC,
					d.hash, c.ordinal, c.id
			) AS rk
			FROM kb_chunks c
			JOIN kb_documents d ON d.id = c.doc_id
			JOIN kbs k ON k.id = c.kb_id
			WHERE $1 <> '' AND c.kb_id = $2 AND k.status = 'active'
			  AND k.embedding_model = $6 AND d.status = 'processed'
			  AND d.embedding_identity = $6 AND c.embedding_identity = $6
			  AND c.tsv @@ websearch_to_tsquery('simple', $1)
			ORDER BY ts_rank_cd(c.tsv, websearch_to_tsquery('simple', $1)) DESC,
				d.hash, c.ordinal, c.id
			LIMIT 50
		),
		vec AS (
			SELECT c.id, ROW_NUMBER() OVER (ORDER BY c.embedding <=> $3::halfvec) AS rk
			FROM kb_chunks c
			JOIN kb_documents d ON d.id = c.doc_id
			JOIN kbs k ON k.id = c.kb_id
			WHERE c.kb_id = $2 AND k.status = 'active' AND k.embedding_model = $6
			  AND d.status = 'processed' AND d.embedding_identity = $6
			  AND c.embedding_identity = $6 AND c.embedding IS NOT NULL
			ORDER BY c.embedding <=> $3::halfvec
			LIMIT 50
		),
		fused AS (
			SELECT id, SUM(1.0 / ($4::float + rk)) AS rrf_score
			FROM (SELECT id, rk FROM keyword UNION ALL SELECT id, rk FROM vec) u
			GROUP BY id
		),
		merged AS (
			SELECT f.id, f.rrf_score
			FROM fused f
			JOIN kb_chunks c ON c.id = f.id
			JOIN kb_documents d ON d.id = c.doc_id
			ORDER BY f.rrf_score DESC, d.hash, c.ordinal, c.id
			LIMIT $5
		)
		SELECT c.id::text, c.doc_id::text, c.content, m.rrf_score
		FROM merged m JOIN kb_chunks c ON c.id = m.id
		JOIN kb_documents d ON d.id = c.doc_id
		ORDER BY m.rrf_score DESC, d.hash, c.ordinal, c.id`,
		keywordQuery, kbUUID, qvec, r.rrfK, topK, r.embedder.Identity())
	if err != nil {
		return nil, fmt.Errorf("rrf query: %w", err)
	}
	out, err := scanPassages(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit vector search: %w", err)
	}
	return out, nil
}

// vectorLiteral 把 []float32 编码成 pgvector 文本字面量 "[v1,v2,...]",配合 $n::halfvec 入库/检索,
// 无需注册 pgx 向量 codec。
func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
