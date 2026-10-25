package analysis

import (
	"strings"
	"testing"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
)

func TestSourceChunksPreserveUnicodeOffsets(t *testing.T) {
	text := strings.Repeat("记忆", 3000) + "末尾原话"
	batches, err := sourceBatches([]Source{{ID: "source", Text: text, Ref: &domain.SourceRef{WorkspaceID: "group", MessageID: "message", Revision: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	end := 0
	for _, batch := range batches {
		for _, s := range batch {
			if s.Ref.Span.Start != end || s.Ref.Span.End != end+utf8.RuneCountInString(s.Text) {
				t.Fatalf("invalid offset: %+v", s.Ref)
			}
			if string([]rune(text)[s.Ref.Span.Start:s.Ref.Span.End]) != s.Text {
				t.Fatal("span does not identify original chunk")
			}
			end = s.Ref.Span.End
		}
	}
	if end != utf8.RuneCountInString(text) {
		t.Fatal("lost source suffix")
	}
}
