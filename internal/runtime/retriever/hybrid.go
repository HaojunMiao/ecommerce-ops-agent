// Package retriever 负责 Agent 的知识检索。
package retriever

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Result 是一次检索命中的知识片段及其排序分数。
type Result struct {
	ID        string
	SourceURI string
	Text      string
	Score     float64
}

// Searcher 统一关键词检索、向量检索等不同召回方式。
type Searcher interface {
	Search(ctx context.Context, workspaceID, query string, limit int) ([]Result, error)
}

// Hybrid 并发执行关键词和向量检索，再使用 RRF（倒数排名融合）合并两路结果。
type Hybrid struct {
	keyword Searcher
	vector  Searcher
}

func NewHybrid(keyword, vector Searcher) *Hybrid {
	return &Hybrid{keyword: keyword, vector: vector}
}

func (h *Hybrid) Search(ctx context.Context, workspaceID, query string, limit int) ([]Result, error) {
	if h.keyword == nil || h.vector == nil {
		return nil, fmt.Errorf("keyword and vector searchers are required")
	}
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(query) == "" || limit <= 0 {
		return nil, fmt.Errorf("workspace, query and positive limit are required")
	}

	// 两路检索互不依赖，分别在 goroutine 中执行；带缓冲的 channel
	// 保证某一路先完成时不会因另一路尚未读取而阻塞。
	type response struct {
		results []Result
		err     error
	}
	keywordCh, vectorCh := make(chan response, 1), make(chan response, 1)
	go func() {
		results, err := h.keyword.Search(ctx, workspaceID, query, limit*2)
		keywordCh <- response{results, err}
	}()
	go func() {
		results, err := h.vector.Search(ctx, workspaceID, query, limit*2)
		vectorCh <- response{results, err}
	}()
	keyword, vector := <-keywordCh, <-vectorCh
	if keyword.err != nil {
		return nil, fmt.Errorf("keyword search: %w", keyword.err)
	}
	if vector.err != nil {
		return nil, fmt.Errorf("vector search: %w", vector.err)
	}

	// RRF 只使用各路排名，避免直接相加 BM25 与向量相似度这两种不同量纲的分数。
	const rrfK = 60.0
	merged := make(map[string]Result)
	add := func(results []Result) {
		seen := make(map[string]struct{}, len(results))
		for rank, result := range results {
			if _, duplicate := seen[result.ID]; duplicate {
				continue
			}
			seen[result.ID] = struct{}{}
			current := merged[result.ID]
			if current.ID == "" {
				current = Result{ID: result.ID, SourceURI: result.SourceURI, Text: result.Text}
			}
			current.Score += 1 / (rrfK + float64(rank+1))
			merged[result.ID] = current
		}
	}
	add(keyword.results)
	add(vector.results)
	out := make([]Result, 0, len(merged))
	for _, result := range merged {
		out = append(out, result)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].ID < out[j].ID
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
