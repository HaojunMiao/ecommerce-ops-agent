//go:build integration

package retriever

// PgvectorRetriever 集成测试:chunk 落 kb_chunks(halfvec + GSE 词元),BM25/RRF 检索读回。
// 需 Docker(或 KBOT_TEST_DATABASE_URL)。

import (
	"context"
	"strings"
	"testing"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres/testpg"
)

func TestPgvectorRetriever_IndexAndSearch(t *testing.T) {
	pool := testpg.Start(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE kbs, kb_documents, kb_chunks CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	emb := NewLocalEmbedder(2048) // 与 kb_chunks.embedding halfvec(2048) 同维
	r := NewPgvectorRetriever(pool, emb)
	var bm25IndexMethod string
	if err := pool.QueryRow(ctx, `
		SELECT am.amname
		FROM pg_class idx
		JOIN pg_am am ON am.oid = idx.relam
		WHERE idx.relname = 'kb_chunks_search_bm25'`).Scan(&bm25IndexMethod); err != nil {
		t.Fatalf("inspect BM25 index: %v", err)
	}
	if bm25IndexMethod != "bm25" {
		t.Fatalf("expected bm25 access method, got %q", bm25IndexMethod)
	}

	var kbID, docID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO kbs (id, workspace_id, name, embedding_model, status)
		 VALUES (gen_random_uuid(), 'ws', 'faq', $1, 'active') RETURNING id::text`, emb.Identity()).Scan(&kbID); err != nil {
		t.Fatalf("insert kb: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO kb_documents (id, kb_id, source_type, hash, embedding_identity, status)
		 VALUES (gen_random_uuid(), $1, 'upload', 'v1', $2, 'pending') RETURNING id::text`, kbID, emb.Identity()).Scan(&docID); err != nil {
		t.Fatalf("insert doc: %v", err)
	}

	texts := []string{
		"退款政策:七天内可申请退款,款项原路退回。",
		"登录方式:使用企业邮箱与密码登录控制台。",
	}
	vecs, err := emb.Embed(ctx, texts)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	chunks := make([]Chunk, len(texts))
	for i, txt := range texts {
		chunks[i] = Chunk{ID: "x", DocID: docID, Ordinal: i, Content: txt, Embedding: vecs[i]}
	}
	indexed, err := r.IndexIfCurrent(ctx, kbID, docID, "v1", emb.Identity(), chunks)
	if err != nil || !indexed {
		t.Fatalf("IndexIfCurrent: indexed=%v err=%v", indexed, err)
	}

	// 检索「怎么退款」应把退款那条排在前面。
	passages, err := r.Search(ctx, kbID, "怎么退款", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(passages) == 0 {
		t.Fatal("expected passages, got none")
	}
	if !strings.Contains(passages[0].Text, "退款") {
		t.Fatalf("expected refund chunk on top, got: %q", passages[0].Text)
	}

	// 关键词单路也必须能处理自然语言中文问句，不能依赖向量路线兜底。
	keywordPassages, err := r.SearchMode(ctx, kbID, "怎么申请退款", 5, ModeBM25)
	if err != nil {
		t.Fatalf("keyword SearchMode: %v", err)
	}
	if len(keywordPassages) == 0 || !strings.Contains(keywordPassages[0].Text, "退款") {
		t.Fatalf("expected Chinese keyword hit, got: %+v", keywordPassages)
	}

	// 只有停用词时，关键词分支为空但 Hybrid 仍应由向量分支正常返回。
	stopWordPassages, err := r.Search(ctx, kbID, "怎么", 5)
	if err != nil || len(stopWordPassages) == 0 {
		t.Fatalf("stop-word-only hybrid query should fall back to vector: %v / %+v", err, stopWordPassages)
	}

	// 幂等:再 Index 同一 doc 不应翻倍。
	indexed, err = r.IndexIfCurrent(ctx, kbID, docID, "v1", emb.Identity(), chunks)
	if err != nil || indexed {
		t.Fatalf("completed version should be a no-op: indexed=%v err=%v", indexed, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kb_chunks WHERE kb_id = $1::uuid`, kbID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 chunks after re-index (idempotent), got %d", n)
	}
}

func TestPgvectorRetriever_BM25TracksReindexAndDelete(t *testing.T) {
	pool := testpg.Start(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE kbs, kb_documents, kb_chunks CASCADE`); err != nil {
		t.Fatal(err)
	}
	emb := NewLocalEmbedder(2048)
	r := NewPgvectorRetriever(pool, emb)
	var kbID, docID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO kbs (id, workspace_id, name, embedding_model, status)
		 VALUES (gen_random_uuid(), 'ws', 'updates', $1, 'active') RETURNING id::text`, emb.Identity()).Scan(&kbID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO kb_documents (id, kb_id, source_type, hash, embedding_identity, status)
		 VALUES (gen_random_uuid(), $1, 'upload', 'v1', $2, 'pending') RETURNING id::text`, kbID, emb.Identity()).Scan(&docID); err != nil {
		t.Fatal(err)
	}

	indexText := func(fingerprint, text string) {
		t.Helper()
		vecs, err := emb.Embed(ctx, []string{text})
		if err != nil {
			t.Fatal(err)
		}
		indexed, err := r.IndexIfCurrent(ctx, kbID, docID, fingerprint, emb.Identity(), []Chunk{{
			DocID: docID, Content: text, Embedding: vecs[0],
		}})
		if err != nil || !indexed {
			t.Fatalf("index %s: indexed=%v err=%v", fingerprint, indexed, err)
		}
	}
	assertHits := func(query string, want int) {
		t.Helper()
		got, err := r.SearchMode(ctx, kbID, query, 5, ModeBM25)
		if err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
		if len(got) != want {
			t.Fatalf("search %q: expected %d hits, got %+v", query, want, got)
		}
	}

	indexText("v1", "旧版到账规则")
	assertHits("到账", 1)
	if _, err := pool.Exec(ctx, `
		UPDATE kb_documents
		SET hash = 'v2', status = 'pending'
		WHERE id = $1::uuid`, docID); err != nil {
		t.Fatal(err)
	}
	indexText("v2", "新版拒付规则")
	assertHits("到账", 0)
	assertHits("拒付", 1)

	if err := r.RemoveDocument(ctx, kbID, docID); err != nil {
		t.Fatal(err)
	}
	assertHits("拒付", 0)
}

func TestPgvectorRetriever_StaleVersionCannotReplaceNewerDocument(t *testing.T) {
	pool := testpg.Start(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE kbs, kb_documents, kb_chunks CASCADE`); err != nil {
		t.Fatal(err)
	}
	emb := NewLocalEmbedder(2048)
	r := NewPgvectorRetriever(pool, emb)
	var kbID, docID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO kbs (id, workspace_id, name, embedding_model, status)
		 VALUES (gen_random_uuid(), 'ws', 'faq', $1, 'active') RETURNING id::text`, emb.Identity()).Scan(&kbID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO kb_documents (id, kb_id, source_type, hash, embedding_identity, status)
		 VALUES (gen_random_uuid(), $1, 'upload', 'new', $2, 'pending') RETURNING id::text`, kbID, emb.Identity()).Scan(&docID); err != nil {
		t.Fatal(err)
	}
	vecs, _ := emb.Embed(ctx, []string{"旧退款规则"})
	chunks := []Chunk{{DocID: docID, Content: "旧退款规则", Embedding: vecs[0]}}
	indexed, err := r.IndexIfCurrent(ctx, kbID, docID, "old", emb.Identity(), chunks)
	if err != nil {
		t.Fatal(err)
	}
	if indexed {
		t.Fatal("stale fingerprint replaced current document")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM kb_chunks WHERE doc_id = $1::uuid`, docID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("stale task wrote %d chunks", count)
	}
}
