package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestUTF8ByteBudgets(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "ASCII text", "中英文混合abc", "🙂项目 café"} {
		for budget := -1; budget <= len(value)+1; budget++ {
			prefix, suffix := PrefixBytes(value, budget), SuffixBytes(value, budget)
			if !utf8.ValidString(prefix) || !utf8.ValidString(suffix) {
				t.Fatalf("invalid UTF-8 for %q with budget %d", value, budget)
			}
			if len(prefix) > max(0, budget) || len(suffix) > max(0, budget) {
				t.Fatalf("byte budget exceeded for %q with budget %d", value, budget)
			}
			if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) {
				t.Fatalf("text changed for %q with budget %d", value, budget)
			}
			if len(value) <= budget && (prefix != value || suffix != value) {
				t.Fatalf("unnecessary truncation for %q", value)
			}
		}
	}
}
