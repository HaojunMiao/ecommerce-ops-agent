package retriever

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// SiliconFlowReranker 调用 Cohere 兼容的 /rerank 接口。
type SiliconFlowReranker struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
}

func NewSiliconFlowReranker(baseURL, apiKey, model string) (*SiliconFlowReranker, error) {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" || strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("reranker 需要 base URL、API key 与 model")
	}
	return &SiliconFlowReranker{
		endpoint: strings.TrimRight(baseURL, "/") + "/rerank",
		apiKey:   apiKey,
		model:    model,
		client:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

type rerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n"`
	ReturnDocuments bool     `json:"return_documents"`
}

type rerankResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
}

func (r *SiliconFlowReranker) Rerank(
	ctx context.Context, query string, passages []Passage, topK int,
) ([]Passage, error) {
	if len(passages) == 0 {
		return []Passage{}, nil
	}
	if topK <= 0 || topK > len(passages) {
		topK = len(passages)
	}
	documents := make([]string, len(passages))
	for i := range passages {
		documents[i] = passages[i].Text
	}
	payload, err := json.Marshal(rerankRequest{
		Model: r.model, Query: query, Documents: documents,
		TopN: topK, ReturnDocuments: false,
	})
	if err != nil {
		return nil, fmt.Errorf("encode rerank request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create rerank request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ecommerce-ops-agent/1.0")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rerank request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read rerank response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rerank HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded rerankResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode rerank response: %w", err)
	}
	if len(decoded.Results) == 0 {
		return nil, fmt.Errorf("rerank response contains no results")
	}
	seen := make(map[int]struct{}, len(decoded.Results))
	out := make([]Passage, 0, min(topK, len(decoded.Results)))
	// 正常接口已按相关性降序返回；显式排序使客户端不依赖供应商顺序细节。
	sort.SliceStable(decoded.Results, func(i, j int) bool {
		return decoded.Results[i].RelevanceScore > decoded.Results[j].RelevanceScore
	})
	for _, result := range decoded.Results {
		if result.Index < 0 || result.Index >= len(passages) {
			return nil, fmt.Errorf("rerank result index %d out of range", result.Index)
		}
		if _, duplicate := seen[result.Index]; duplicate {
			return nil, fmt.Errorf("rerank result contains duplicate index %d", result.Index)
		}
		seen[result.Index] = struct{}{}
		passage := passages[result.Index]
		passage.Score = result.RelevanceScore
		out = append(out, passage)
		if len(out) == topK {
			break
		}
	}
	if len(out) != topK {
		return nil, fmt.Errorf("rerank returned %d results, expected %d", len(out), topK)
	}
	return out, nil
}
