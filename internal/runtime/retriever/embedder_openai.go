package retriever

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	aclopenai "github.com/cloudwego/eino-ext/libs/acl/openai"
)

// NewEmbedder 按配置构造嵌入器。
//
//	kind="local"  → 离线确定性 LocalEmbedder(dim)，无需网络,make up 即开即用;
//	kind="openai" → 独立的 OpenAI 兼容 /embeddings 端点。
//
// dim 必须与 kb_chunks.embedding halfvec(N) 一致;不一致会在 platform.NewService 启动时 log.Fatal。
func NewEmbedder(kind string, dim int, baseURL, apiKey, model string) (Embedder, error) {
	switch kind {
	case "", "local":
		return NewLocalEmbedder(dim), nil
	case "openai":
		if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" || strings.TrimSpace(model) == "" {
			return nil, fmt.Errorf("openai embedder 需要 KBOT_EMBEDDER_BASE_URL、KBOT_EMBEDDER_API_KEY 与 KBOT_EMBEDDER_MODEL")
		}
		client, err := aclopenai.NewEmbeddingClient(context.Background(), &aclopenai.EmbeddingConfig{
			APIKey: apiKey, BaseURL: baseURL, Model: model,
			Dimensions: &dim,
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
		})
		if err != nil {
			return nil, fmt.Errorf("create Eino OpenAI embedder: %w", err)
		}
		return &OpenAIEmbedder{
			client: client, dim: dim,
			model:    model,
			identity: "openai:" + strings.TrimRight(baseURL, "/") + ":" + model,
		}, nil
	default:
		return nil, fmt.Errorf("未知 KBOT_EMBEDDER=%q(应为 local|openai)", kind)
	}
}

// OpenAIEmbedder 通过 Eino OpenAI ACL 的 EmbeddingClient 调用兼容端点。
type OpenAIEmbedder struct {
	client   *aclopenai.EmbeddingClient
	dim      int
	model    string
	identity string
}

func (e *OpenAIEmbedder) Dim() int { return e.dim }

func (e *OpenAIEmbedder) ModelID() string { return e.model }

func (e *OpenAIEmbedder) Identity() string {
	return fmt.Sprintf("%s:%d", e.identity, e.dim)
}

// 采用保守批次兼容不同的 OpenAI-compatible Embeddings 服务；ingest 不需要
// 感知供应商限制，并保持返回顺序与输入严格一致。
const embeddingBatchSize = 64

func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	vecs := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += embeddingBatchSize {
		end := min(start+embeddingBatchSize, len(texts))
		embeddings, err := e.client.EmbedStrings(ctx, texts[start:end])
		if err != nil {
			return nil, fmt.Errorf("embeddings request batch %d: %w", start/embeddingBatchSize+1, err)
		}
		if len(embeddings) != end-start {
			return nil, fmt.Errorf("embeddings batch %d: 期望 %d 条,返回 %d 条", start/embeddingBatchSize+1, end-start, len(embeddings))
		}
		for _, embedding := range embeddings {
			if len(embedding) != e.dim {
				return nil, fmt.Errorf("embeddings: 维度 %d 与配置 %d 不符", len(embedding), e.dim)
			}
			vec := make([]float32, len(embedding))
			for j, value := range embedding {
				vec[j] = float32(value)
			}
			vecs = append(vecs, vec)
		}
	}
	return vecs, nil
}
