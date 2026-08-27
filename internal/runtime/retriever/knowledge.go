package retriever

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/kb"
)

// DocumentSource 是检索层读取知识库文档所需的最小接口。
// kb.Service 已经具有相同签名，因此不需要额外适配。
type DocumentSource interface {
	Documents(ctx context.Context, workspaceID, kbID string) ([]kb.Document, error)
}

// Embedder 把一段文本转换成向量。课堂默认使用 HashEmbedder，
// 以后可以注入真实的 Embedding Provider 而不修改向量检索器。
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

// KnowledgeSearch 是知识库检索总入口，负责选定知识库、展开切片，
// 再根据 mode 路由到 BM25、向量或混合检索。
type KnowledgeSearch struct {
	source   DocumentSource
	embedder Embedder
}

func NewKnowledgeSearch(source DocumentSource, embedders ...Embedder) *KnowledgeSearch {
	// 可变参数让调用方可以省略 Embedder；课堂离线默认使用 256 维哈希向量。
	embedder := Embedder(HashEmbedder{Dimensions: 256})
	if len(embedders) > 0 && embedders[0] != nil {
		embedder = embedders[0]
	}
	return &KnowledgeSearch{source: source, embedder: embedder}
}

func (s *KnowledgeSearch) Search(
	ctx context.Context, workspaceID, kbID, query, mode string, limit int,
) ([]Result, error) {
	if s.source == nil {
		return nil, fmt.Errorf("knowledge document source is required")
	}
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(kbID) == "" ||
		strings.TrimSpace(query) == "" || limit <= 0 {
		return nil, fmt.Errorf("workspace, knowledge base, query and positive limit are required")
	}

	// 先由知识库服务完成 workspace + kbID 范围校验，再读取该库全部文档。
	documents, err := s.source.Documents(ctx, workspaceID, kbID)
	if err != nil {
		return nil, err
	}

	// 检索单位是 chunk，而不是整篇文档。docID:chunkIndex 构成稳定片段 ID。
	passages := make([]Result, 0)
	for _, document := range documents {
		for index, chunk := range document.Chunks {
			passages = append(passages, Result{
				ID:        fmt.Sprintf("%s:%d", document.ID, index),
				SourceURI: document.SourceURI,
				Text:      chunk,
			})
		}
	}

	// 课堂版每次查询都从当前切片重新构造两路检索器，便于理解但不适合大规模数据。
	keyword := newBM25Searcher(passages)
	vector := &vectorSearcher{embedder: s.embedder, passages: passages}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "bm25", "text":
		return keyword.Search(ctx, workspaceID, query, limit)
	case "vector":
		return vector.Search(ctx, workspaceID, query, limit)
	case "", "hybrid":
		return NewHybrid(keyword, vector).Search(ctx, workspaceID, query, limit)
	default:
		return nil, fmt.Errorf("unsupported retrieval mode %q", mode)
	}
}

// bm25Searcher 缓存当前语料的分词、文档频率和平均长度。
type bm25Searcher struct {
	passages []Result
	tokens   [][]string
	df       map[string]int
	avgDL    float64
}

func newBM25Searcher(passages []Result) *bm25Searcher {
	searcher := &bm25Searcher{
		passages: passages,
		tokens:   make([][]string, len(passages)),
		df:       make(map[string]int),
	}
	for index, passage := range passages {
		terms := tokenize(passage.Text)
		searcher.tokens[index] = terms
		searcher.avgDL += float64(len(terms))

		// df 统计“包含该词的文档数”，同一切片重复出现只能计一次。
		seen := make(map[string]struct{}, len(terms))
		for _, term := range terms {
			if _, ok := seen[term]; ok {
				continue
			}
			seen[term] = struct{}{}
			searcher.df[term]++
		}
	}
	if len(passages) > 0 {
		searcher.avgDL /= float64(len(passages))
	}
	return searcher
}

func (r *bm25Searcher) Search(ctx context.Context, _ string, query string, limit int) ([]Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	queryTerms := uniqueTerms(tokenize(query))
	results := make([]Result, 0, len(r.passages))
	for index, passage := range r.passages {
		passage.Score = r.score(queryTerms, r.tokens[index])
		if passage.Score > 0 {
			results = append(results, passage)
		}
	}
	return rankResults(results, limit), nil
}

func (r *bm25Searcher) score(query, document []string) float64 {
	if len(query) == 0 || len(document) == 0 || r.avgDL == 0 {
		return 0
	}

	// tf：查询词在当前切片中的出现次数。
	frequencies := make(map[string]int, len(document))
	for _, term := range document {
		frequencies[term]++
	}
	const k1, b = 1.2, 0.75
	n := float64(len(r.passages))
	dl := float64(len(document))
	score := 0.0
	for _, term := range query {
		tf := float64(frequencies[term])
		if tf == 0 {
			continue
		}
		df := float64(r.df[term])
		// idf 越高，说明该词在语料中越少见、区分能力越强。
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		// k1 控制词频饱和，b 控制文档长度归一化。
		score += idf * (tf * (k1 + 1)) / (tf + k1*(1-b+b*dl/r.avgDL))
	}
	return score
}

type vectorSearcher struct {
	embedder Embedder
	passages []Result
}

func (r *vectorSearcher) Search(ctx context.Context, _ string, query string, limit int) ([]Result, error) {
	queryVector, err := r.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	results := make([]Result, 0, len(r.passages))
	for _, passage := range r.passages {
		// 课堂版查询时现算每个 passage 的向量；生产版应在 ingest 时提前计算并持久化。
		vector, err := r.embedder.Embed(ctx, passage.Text)
		if err != nil {
			return nil, fmt.Errorf("embed passage %s: %w", passage.ID, err)
		}
		passage.Score = cosine(queryVector, vector)
		if passage.Score > 0 {
			results = append(results, passage)
		}
	}
	return rankResults(results, limit), nil
}

// rankResults 按相关性从高到低排序；同分时按 ID 排序保证结果稳定。
func rankResults(results []Result, limit int) []Result {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].ID < results[j].ID
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// HashEmbedder 是课堂离线实现：把中英文 token 投影到固定维度并归一化。
// 它没有真实语义理解能力；生产环境可以注入真实 Embedding Provider。
type HashEmbedder struct{ Dimensions int }

func (e HashEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.Dimensions <= 0 {
		return nil, fmt.Errorf("embedding dimensions must be positive")
	}
	vector := make([]float64, e.Dimensions)
	for _, token := range tokenize(text) {
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(token))
		vector[int(hash.Sum64()%uint64(e.Dimensions))]++
	}

	// L2 归一化后，两个向量的点积就是余弦相似度。
	norm := math.Sqrt(dot(vector, vector))
	if norm > 0 {
		for index := range vector {
			vector[index] /= norm
		}
	}
	return vector, nil
}

func cosine(left, right []float64) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return 0
	}
	return dot(left, right)
}

func dot(left, right []float64) float64 {
	score := 0.0
	for index := range left {
		score += left[index] * right[index]
	}
	return score
}

// tokenize 是课堂用的轻量分词：英文和数字按连续单词处理；中文同时保留
// 单字和相邻双字，提升“库存”“调拨”等短语的关键词匹配概率。
func tokenize(value string) []string {
	value = strings.ToLower(strings.TrimSpace(value))
	terms := make([]string, 0, len(value))
	word := make([]rune, 0, 16)
	var previousHan rune
	flushWord := func() {
		if len(word) > 0 {
			terms = append(terms, string(word))
			word = word[:0]
		}
	}
	for _, current := range []rune(value) {
		if unicode.Is(unicode.Han, current) {
			flushWord()
			terms = append(terms, string(current))
			if previousHan != 0 {
				terms = append(terms, string([]rune{previousHan, current}))
			}
			previousHan = current
			continue
		}
		previousHan = 0
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			word = append(word, current)
		} else {
			flushWord()
		}
	}
	flushWord()
	return terms
}

func uniqueTerms(terms []string) []string {
	result := make([]string, 0, len(terms))
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		result = append(result, term)
	}
	return result
}
