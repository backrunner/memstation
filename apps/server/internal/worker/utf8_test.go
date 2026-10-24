package worker

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSummaryChunksPreserveChineseAndEmojiUTF8(t *testing.T) {
	t.Parallel()
	chunk := "start " + strings.Repeat("中文🙂", 300) + " end"
	if result := truncate(chunk, 160); !utf8.ValidString(result) || len(result) > 163 {
		t.Fatalf("invalid worker summary: %q", result)
	}
}
