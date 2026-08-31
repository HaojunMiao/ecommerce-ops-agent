package retriever

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSiliconFlowRerankerPreservesPassageIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rerank" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected request: path=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var req rerankRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "Qwen/Qwen3-Reranker-4B" || req.Query != "退款规则" || req.TopN != 2 {
			t.Fatalf("unexpected payload: %+v", req)
		}
		_, _ = w.Write([]byte(`{"results":[{"index":2,"relevance_score":0.91},{"index":0,"relevance_score":0.72}]}`))
	}))
	defer server.Close()

	reranker, err := NewSiliconFlowReranker(server.URL+"/v1", "test-key", "Qwen/Qwen3-Reranker-4B")
	if err != nil {
		t.Fatalf("NewSiliconFlowReranker: %v", err)
	}
	passages := []Passage{
		{ChunkID: "c0", Text: "退款"},
		{ChunkID: "c1", Text: "物流"},
		{ChunkID: "c2", Text: "退款审批"},
	}
	got, err := reranker.Rerank(context.Background(), "退款规则", passages, 2)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(got) != 2 || got[0].ChunkID != "c2" || got[0].Score != 0.91 || got[1].ChunkID != "c0" {
		t.Fatalf("unexpected reranked passages: %+v", got)
	}
}

type stubSearcher struct {
	passages   []Passage
	requestedK int
}

func (s *stubSearcher) Index(context.Context, string, []Chunk) error         { return nil }
func (s *stubSearcher) RemoveDocument(context.Context, string, string) error { return nil }
func (s *stubSearcher) Embedder() Embedder                                   { return NewLocalEmbedder(8) }
func (s *stubSearcher) Search(_ context.Context, _, _ string, k int) ([]Passage, error) {
	s.requestedK = k
	return append([]Passage(nil), s.passages...), nil
}

type stubReranker struct {
	err error
}

func (s stubReranker) Rerank(_ context.Context, _ string, passages []Passage, topK int) ([]Passage, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := append([]Passage(nil), passages...)
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return firstPassages(out, topK), nil
}

func TestRerankingSearcherExpandsCandidates(t *testing.T) {
	base := &stubSearcher{passages: []Passage{{ChunkID: "1"}, {ChunkID: "2"}, {ChunkID: "3"}}}
	searcher := NewRerankingSearcher(base, stubReranker{}, 10)
	got, err := searcher.Search(context.Background(), "kb", "query", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if base.requestedK != 10 || len(got) != 2 || got[0].ChunkID != "3" {
		t.Fatalf("candidate expansion/rerank mismatch: requested=%d got=%+v", base.requestedK, got)
	}
}

func TestRerankingSearcherFallsBackToRRFOrder(t *testing.T) {
	base := &stubSearcher{passages: []Passage{{ChunkID: "1"}, {ChunkID: "2"}, {ChunkID: "3"}}}
	searcher := NewRerankingSearcher(base, stubReranker{err: errors.New("temporary failure")}, 10)
	got, err := searcher.Search(context.Background(), "kb", "query", 2)
	if err != nil {
		t.Fatalf("Search fallback: %v", err)
	}
	if len(got) != 2 || got[0].ChunkID != "1" || got[1].ChunkID != "2" {
		t.Fatalf("fallback changed RRF order: %+v", got)
	}
}
