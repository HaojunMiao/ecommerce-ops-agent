package retriever

import (
	"strings"
	"sync"
	"unicode"

	"github.com/go-ego/gse"
)

// 中文关键词索引与查询必须使用同一套分词逻辑，否则相同概念会被 PostgreSQL
// 解析成不同词元。GSE 的简体中文词典通过 go:embed 随二进制发布，不要求容器
// 额外挂载词典，也不引入 CGO。
var (
	segmenterOnce sync.Once
	segmenter     *gse.Segmenter
	segmenterErr  error
)

var searchStopWords = map[string]struct{}{
	"的": {}, "了": {}, "吗": {}, "呢": {}, "啊": {}, "呀": {},
	"和": {}, "与": {}, "及": {}, "或": {}, "是": {}, "在": {},
	"我": {}, "你": {}, "他": {}, "它": {}, "请": {}, "请问": {},
	"怎么": {}, "怎样": {}, "如何": {}, "什么": {}, "哪些": {},
	"一下": {}, "相关": {}, "可以": {}, "能否": {}, "是否": {},
}

const ecommerceDictionary = `跨境电商 100000 n
履约 100000 n
库存调拨 100000 n
退款 100000 n
结算对账 100000 n
结算申诉 100000 n
知识库 100000 n
最晚发货时间 100000 n
幂等键 100000 n`

func defaultSegmenter() (*gse.Segmenter, error) {
	segmenterOnce.Do(func() {
		// alpha 让订单号、SKU、SLA 等英文和数字连续串保持为可检索词元。
		seg, err := gse.NewEmbed("zh_s", "alpha")
		if err != nil {
			segmenterErr = err
			return
		}
		if err := seg.LoadDictEmbed(ecommerceDictionary); err != nil {
			segmenterErr = err
			return
		}
		segmenter = &seg
	})
	return segmenter, segmenterErr
}

// tokenize 使用 GSE 搜索模式产生中文词及其有意义的子词，并过滤问句中的虚词。
// 它同时服务于内存 BM25、本地测试嵌入器和 PostgreSQL 关键词检索。
func tokenize(text string) []string {
	seg, err := defaultSegmenter()
	if err != nil || seg == nil {
		return fallbackTokenize(text)
	}
	raw := seg.CutSearch(strings.ToLower(text), true)
	if len(raw) == 0 {
		raw = seg.Cut(strings.ToLower(text), true)
	}
	return normalizeTokens(raw)
}

func normalizeTokens(raw []string) []string {
	tokens := make([]string, 0, len(raw))
	for _, item := range raw {
		var normalized strings.Builder
		for _, r := range strings.TrimSpace(strings.ToLower(item)) {
			if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' {
				normalized.WriteRune(r)
			}
		}
		token := strings.Trim(normalized.String(), "-_")
		if token == "" {
			continue
		}
		if _, stopped := searchStopWords[token]; stopped {
			continue
		}
		tokens = append(tokens, token)
	}
	return tokens
}

// lexicalDocument 是写入 kb_chunks.search_text 的空格分隔文本。PostgreSQL 的
// simple 配置只负责标准化这些已经切好的词，不再负责中文分词。
func lexicalDocument(text string) string {
	return strings.Join(tokenize(text), " ")
}

// lexicalQuery 为 websearch_to_tsquery 构造 OR 查询。使用 OR 而不是隐式 AND，
// 避免“申请退款”因为文档缺少“申请”或自然语言问句带虚词而整条召回失败。
func lexicalQuery(text string) string {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(tokens))
	unique := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		unique = append(unique, token)
	}
	return strings.Join(unique, " OR ")
}

// fallbackTokenize 仅在嵌入词典初始化异常时兜底，保证服务仍可启动。测试会直接
// 校验 GSE 的中文词元，因此正常构建不会静默退化而不被发现。
func fallbackTokenize(text string) []string {
	text = strings.ToLower(text)
	var raw []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			raw = append(raw, current.String())
			current.Reset()
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' {
			current.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return normalizeTokens(raw)
}
