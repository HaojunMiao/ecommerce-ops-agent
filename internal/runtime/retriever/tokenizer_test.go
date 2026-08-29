package retriever

import (
	"strings"
	"testing"
)

func TestChineseTokenizerSplitsRefundQuestion(t *testing.T) {
	segmenter, err := defaultSegmenter()
	if err != nil || segmenter == nil {
		t.Fatalf("load GSE segmenter: %v", err)
	}

	tokens := tokenize("怎么申请退款？")
	joined := strings.Join(tokens, ",")
	for _, want := range []string{"申请", "退款"} {
		if !containsToken(tokens, want) {
			t.Fatalf("expected token %q in %q", want, joined)
		}
	}
	if containsToken(tokens, "怎么") {
		t.Fatalf("question stop word should be removed: %q", joined)
	}
}

func TestLexicalQueryUsesORAndKeepsBusinessIdentifiers(t *testing.T) {
	query := lexicalQuery("请查询 SKU-BLACK-M-01 的退款规则")
	if !strings.Contains(query, " OR ") {
		t.Fatalf("expected OR query, got %q", query)
	}
	if !strings.Contains(strings.ToLower(query), "sku") || !strings.Contains(query, "退款") {
		t.Fatalf("expected identifier and refund terms, got %q", query)
	}
}

func containsToken(tokens []string, want string) bool {
	for _, token := range tokens {
		if token == want {
			return true
		}
	}
	return false
}
