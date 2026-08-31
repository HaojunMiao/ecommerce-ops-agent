package retriever

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// Reranker 根据 query 与候选片段全文的相关性重新排序。
// 它只改变召回结果顺序，不参与向量化或索引写入。
type Reranker interface {
	Rerank(ctx context.Context, query string, passages []Passage, topK int) ([]Passage, error)
}

// RerankingSearcher 在原 Searcher 外增加“扩大候选集 -> 模型重排 -> Top-K”。
// 索引相关方法全部透传，因此不会改变写入链路或历史向量。
type RerankingSearcher struct {
	base       Searcher
	reranker   Reranker
	candidateK int
}

var _ Searcher = (*RerankingSearcher)(nil)
var _ ModeSearcher = (*RerankingSearcher)(nil)
var _ VersionedIndexer = (*RerankingSearcher)(nil)

func NewRerankingSearcher(base Searcher, reranker Reranker, candidateK int) *RerankingSearcher {
	if candidateK <= 0 {
		candidateK = 10
	}
	return &RerankingSearcher{base: base, reranker: reranker, candidateK: candidateK}
}

func (r *RerankingSearcher) Embedder() Embedder { return r.base.Embedder() }

func (r *RerankingSearcher) Index(ctx context.Context, kbID string, chunks []Chunk) error {
	return r.base.Index(ctx, kbID, chunks)
}

func (r *RerankingSearcher) RemoveDocument(ctx context.Context, kbID, documentID string) error {
	return r.base.RemoveDocument(ctx, kbID, documentID)
}

func (r *RerankingSearcher) IndexIfCurrent(
	ctx context.Context,
	kbID, documentID, fingerprint, embeddingIdentity string,
	chunks []Chunk,
) (bool, error) {
	if indexer, ok := r.base.(VersionedIndexer); ok {
		return indexer.IndexIfCurrent(ctx, kbID, documentID, fingerprint, embeddingIdentity, chunks)
	}
	return false, fmt.Errorf("reranking searcher requires a versioned base indexer")
}

func (r *RerankingSearcher) Search(ctx context.Context, kbID, query string, topK int) ([]Passage, error) {
	if topK <= 0 {
		topK = 5
	}
	candidateK := max(topK, r.candidateK)
	passages, err := r.base.Search(ctx, kbID, query, candidateK)
	if err != nil || len(passages) == 0 || r.reranker == nil {
		return passages, err
	}
	reranked, err := r.reranker.Rerank(ctx, query, passages, topK)
	if err != nil {
		// Reranker 是排序增强项，供应商瞬时故障不应让知识库完全不可用。
		log.Printf("reranker failed, fallback to equal-RRF order: %v", err)
		return firstPassages(passages, topK), nil
	}
	return reranked, nil
}

// SearchMode 保留评测/管理页的纯关键词与纯向量语义；只对 hybrid 应用重排。
func (r *RerankingSearcher) SearchMode(
	ctx context.Context, kbID, query string, topK int, mode string,
) ([]Passage, error) {
	if strings.EqualFold(strings.TrimSpace(mode), "hybrid") || strings.TrimSpace(mode) == "" {
		return r.Search(ctx, kbID, query, topK)
	}
	if modeSearcher, ok := r.base.(ModeSearcher); ok {
		return modeSearcher.SearchMode(ctx, kbID, query, topK, mode)
	}
	return r.base.Search(ctx, kbID, query, topK)
}

func firstPassages(passages []Passage, topK int) []Passage {
	if len(passages) <= topK {
		return passages
	}
	return passages[:topK]
}
