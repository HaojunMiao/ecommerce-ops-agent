package retriever

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
)

// Embedder 把文本编码为向量。生产用 eino-ext 的 OpenAI embedding 组件，
// 通过独立配置调用 Embeddings API。
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dim() int
	ModelID() string
	Identity() string
}

// LocalEmbedder 是离线 / 开发 / 测试用的确定性嵌入器：把词袋哈希到固定维度并做
// L2 归一化。它没有语义理解能力，但"共享词越多余弦越高"足以驱动检索链路跑通、
// 让单元测试无需联网。换成真实 embedding 模型只需替换本接口的实现。
type LocalEmbedder struct {
	dim int
}

// NewLocalEmbedder 创建本地嵌入器。数据库路径应传与 embedding 列一致的维度。
func NewLocalEmbedder(dim int) *LocalEmbedder {
	if dim <= 0 {
		dim = 256
	}
	return &LocalEmbedder{dim: dim}
}

func (e *LocalEmbedder) Dim() int { return e.dim }

func (e *LocalEmbedder) ModelID() string { return "local" }

func (e *LocalEmbedder) Identity() string { return fmt.Sprintf("local:%d:gse-v1", e.dim) }

func (e *LocalEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.embedOne(t)
	}
	return out, nil
}

func (e *LocalEmbedder) embedOne(text string) []float32 {
	vec := make([]float32, e.dim)
	for _, tok := range tokenize(text) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		idx := int(h.Sum32()) % e.dim
		if idx < 0 {
			idx += e.dim
		}
		vec[idx] += 1
	}
	// L2 归一化，便于用点积当余弦。
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range vec {
			vec[i] = float32(float64(vec[i]) / norm)
		}
	}
	return vec
}

// cosine 计算两个等长向量的余弦相似度（假定已归一化时等价于点积）。
func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
