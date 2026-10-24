package conversation

import (
	"strings"
	"testing"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
)

func TestMemoryPromptPreservesChineseAndEmojiUTF8(t *testing.T) {
	t.Parallel()
	message := domain.UnifiedMessage{
		Role: domain.RoleUser,
		Text: "我喜欢" + strings.Repeat("简洁的中文回复🙂", 100),
	}
	prompt := BuildMemoryPromptInput([]domain.UnifiedMessage{message})
	if !utf8.ValidString(prompt) || !strings.Contains(prompt, "我喜欢") {
		t.Fatalf("invalid memory prompt: %q", prompt)
	}
	encoded := `{"response":"` + strings.Repeat("项目🙂", 100) + `"}`
	compacted := truncateMiddle(encoded, 96)
	if !utf8.ValidString(compacted) || len(compacted) > 96 || !strings.HasSuffix(compacted, `"}`) {
		t.Fatalf("invalid compacted tool result: %q", compacted)
	}
}
