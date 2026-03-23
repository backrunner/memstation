package conversation

import (
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
)

func TestBuildSegmentsCreatesOrderedOverlappingDialogueWindows(t *testing.T) {
	t.Parallel()

	messages := []domain.UnifiedMessage{
		{Role: domain.RoleSystem, Text: "ignore this injected system context"},
		{Role: domain.RoleUser, Text: "u1"},
		{Role: domain.RoleAssistant, Text: "a1"},
		{Role: domain.RoleUser, Text: "u2"},
		{Role: domain.RoleAssistant, Text: "a2"},
		{Role: domain.RoleUser, Text: "u3"},
		{Role: domain.RoleAssistant, Text: "a3"},
		{Role: domain.RoleUser, Text: "u4"},
		{Role: domain.RoleAssistant, Text: "a4"},
	}

	segments := BuildSegments("user-1", "conv-1", messages, time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC), SegmentOptions{
		MaxMessages:   4,
		Overlap:       1,
		MaxCharacters: 1024,
	})
	if len(segments) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segments))
	}
	if segments[0].SegmentIndex != 0 || segments[1].SegmentIndex != 1 || segments[2].SegmentIndex != 2 {
		t.Fatalf("expected ordered segment indices, got %+v", segments)
	}
	if segments[0].StartSequence != 1 || segments[0].EndSequence != 4 {
		t.Fatalf("unexpected first segment span: %+v", segments[0])
	}
	if segments[1].StartSequence != 4 || segments[1].OverlapFromSequence != 4 || segments[1].OverlapMessageCount != 1 {
		t.Fatalf("expected second segment to overlap from sequence 4, got %+v", segments[1])
	}
	if segments[2].StartSequence != 7 || segments[2].OverlapFromSequence != 7 || segments[2].OverlapMessageCount != 1 {
		t.Fatalf("expected third segment overlap metadata, got %+v", segments[2])
	}
	if !strings.Contains(segments[0].Content, "user: u1") || !strings.Contains(segments[0].Content, "assistant: a2") {
		t.Fatalf("unexpected segment content: %q", segments[0].Content)
	}
	if got := SummarizeSegments(segments); got == "" {
		t.Fatal("expected segment summary to be non-empty")
	}
}
