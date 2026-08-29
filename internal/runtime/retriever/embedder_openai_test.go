package retriever

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOpenAIEmbedderBatchesAndPreservesOrder(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("path = %q, want /embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != "embedding-test" {
			t.Fatalf("model = %q", body.Model)
		}
		if len(body.Input) > embeddingBatchSize {
			t.Fatalf("batch size = %d", len(body.Input))
		}
		batch := requests.Add(1)
		data := make([]map[string]any, len(body.Input))
		for i := range body.Input {
			data[i] = map[string]any{
				"object": "embedding", "index": i,
				"embedding": []float64{float64(batch), float64(i), 1},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list", "model": body.Model, "data": data,
			"usage": map[string]int{"prompt_tokens": len(body.Input), "total_tokens": len(body.Input)},
		})
	}))
	defer server.Close()

	embedder, err := NewEmbedder("openai", 3, server.URL, "test-key", "embedding-test")
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	texts := make([]string, embeddingBatchSize+44)
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}
	got, err := embedder.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != len(texts) || requests.Load() != 2 {
		t.Fatalf("vectors=%d requests=%d", len(got), requests.Load())
	}
	if got[0][0] != 1 || got[embeddingBatchSize][0] != 2 {
		t.Fatalf("batch order was not preserved")
	}
}

func TestOpenAIEmbedderRejectsUnexpectedDimension(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[1,2]}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer server.Close()
	embedder, err := NewEmbedder("openai", 3, server.URL, "test-key", "embedding-test")
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	if _, err := embedder.Embed(context.Background(), []string{"text"}); err == nil {
		t.Fatal("dimension mismatch must be rejected")
	}
}
