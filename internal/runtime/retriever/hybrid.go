// Package retriever 负责 Agent 的知识检索。
package retriever

import (
	"context"
	"fmt"
	"strings"

	einoretriever "github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/flow/retriever/router"
	"github.com/cloudwego/eino/schema"
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

// Hybrid 同时执行关键词和向量检索，并交给 Eino Retriever Router
// 使用 RRF（倒数排名融合）合并两路结果。
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

	// Router 接收的是 Eino Retriever，因此先用 searcherRetriever 适配项目接口。
	// Router 每次固定选择两路检索器；其合并逻辑按排名而非原始分数做 RRF。
	hybrid, err := router.NewRetriever(ctx, &router.Config{
		Retrievers: map[string]einoretriever.Retriever{
			"keyword": &searcherRetriever{searcher: h.keyword, workspaceID: workspaceID},
			"vector":  &searcherRetriever{searcher: h.vector, workspaceID: workspaceID},
		},
		Router: func(context.Context, string) ([]string, error) {
			return []string{"keyword", "vector"}, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create Eino retriever router: %w", err)
	}

	// 每路多取一倍候选，给融合排序留下余量，最终再截取用户需要的数量。
	documents, err := hybrid.Retrieve(ctx, query, einoretriever.WithTopK(limit*2))
	if err != nil {
		return nil, fmt.Errorf("hybrid retrieve: %w", err)
	}
	out := make([]Result, 0, len(documents))
	for _, document := range documents {
		sourceURI, _ := document.MetaData["source_uri"].(string)
		out = append(out, Result{
			ID: document.ID, SourceURI: sourceURI, Text: document.Content,
		})
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// searcherRetriever 将项目的 Searcher 适配成 Eino Retriever。
type searcherRetriever struct {
	searcher    Searcher
	workspaceID string
}

func (r *searcherRetriever) Retrieve(
	ctx context.Context, query string, opts ...einoretriever.Option,
) ([]*schema.Document, error) {
	topK := 10
	if configured := einoretriever.GetCommonOptions(nil, opts...).TopK; configured != nil && *configured > 0 {
		topK = *configured
	}
	results, err := r.searcher.Search(ctx, r.workspaceID, query, topK)
	if err != nil {
		return nil, err
	}

	documents := make([]*schema.Document, 0, len(results))
	for _, result := range results {
		documents = append(documents, &schema.Document{
			ID: result.ID, Content: result.Text,
			// 原始分数只作为元数据携带；RRF 使用的是两路排名，不直接相加异构分数。
			MetaData: map[string]any{
				"source_uri": result.SourceURI,
				"raw_score":  result.Score,
			},
		})
	}
	return documents, nil
}
