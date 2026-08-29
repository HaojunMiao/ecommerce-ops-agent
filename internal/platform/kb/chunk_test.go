package kb

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunkTextUsesRuneBudgetForChinese(t *testing.T) {
	chunks := chunkText(strings.Repeat("退", 600), 500, 100)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(chunks))
	}
	if got := utf8.RuneCountInString(chunks[0]); got != 500 {
		t.Fatalf("first chunk runes = %d, want 500", got)
	}
	if got := utf8.RuneCountInString(chunks[1]); got != 200 {
		t.Fatalf("second chunk runes = %d, want 200", got)
	}
	if chunks[0][len(chunks[0])-len(strings.Repeat("退", 100)):] != chunks[1][:len(strings.Repeat("退", 100))] {
		t.Fatal("second chunk does not preserve the configured overlap")
	}
}

func TestChunkTextPacksChineseParagraphsByRunes(t *testing.T) {
	text := strings.Repeat("甲", 240) + "\n\n" + strings.Repeat("乙", 240)
	chunks := chunkText(text, 500, 100)
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want one 481-rune chunk", len(chunks))
	}
	if got := utf8.RuneCountInString(chunks[0]); got != 481 {
		t.Fatalf("chunk runes = %d, want 481", got)
	}
}
