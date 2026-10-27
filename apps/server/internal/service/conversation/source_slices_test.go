package conversation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
)

func TestSegmentsPreserveCompleteSourcesAndBoundRenderedBytes(t *testing.T) {
	text := "  " + strings.Repeat("检索来源不能丢失。🙂 ", 240) + "最后确认：会议取消，不能按原计划举行。\n"
	part := domain.MessagePart{PartID: "p", SequenceNum: 7, Role: domain.RoleUser, TextContent: text}
	artifact := domain.MessagePartArtifact{PartID: "p", ArtifactID: "a", TextContent: strings.Repeat("OCR ", 200) + "TAIL-OCR"}
	for _, budget := range []int{1, 16, 97, 1200, 100000} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			opts := SegmentOptions{MaxBytes: budget, MaxMessages: 6, Overlap: 2}
			segments := BuildSegmentsFromTimeline("u", "c", nil, []domain.MessagePart{part}, []domain.MessagePartArtifact{artifact}, time.Unix(1, 0), opts)
			assertSliceCoverage(t, segments, map[string]string{"part.text_content:p:": text, "artifact.text_content:p:a": artifact.TextContent}, min(65536, max(16, budget)))
			if !reflect.DeepEqual(segments, BuildSegmentsFromTimeline("u", "c", nil, []domain.MessagePart{part}, []domain.MessagePartArtifact{artifact}, time.Unix(1, 0), opts)) {
				t.Fatal("retry changed source slices")
			}
		})
	}
}

func assertSliceCoverage(t *testing.T, segments []domain.ConversationSegment, expected map[string]string, budget int) {
	t.Helper()
	type span struct{ start, end int }
	seen := map[string]map[span]bool{}
	for _, segment := range segments {
		if len(segment.Content) > budget || !utf8.ValidString(segment.Content) {
			t.Fatal("invalid bounded text", len(segment.Content), budget)
		}
		raw, _ := json.Marshal(segment.Metadata["source_slices"])
		var slices []domain.SegmentSourceSlice
		if err := json.Unmarshal(raw, &slices); err != nil || len(slices) == 0 {
			t.Fatal("missing serializable provenance", err)
		}
		for _, s := range slices {
			key := s.Field + ":" + s.PartID + ":" + s.ArtifactID
			source, ok := expected[key]
			if !ok {
				t.Fatal("unexpected source", key)
			}
			hash := sha256.Sum256([]byte(source))
			if s.SourceSHA256 != hex.EncodeToString(hash[:]) || s.ByteStart < 0 || s.ByteEnd > len(source) || s.ByteEnd <= s.ByteStart {
				t.Fatal("invalid source identity", s)
			}
			if source[s.ByteStart:s.ByteEnd] != segment.Content[s.ContentByteStart:s.ContentByteEnd] {
				t.Fatal("slice no longer equals original")
			}
			if seen[key] == nil {
				seen[key] = map[span]bool{}
			}
			seen[key][span{s.ByteStart, s.ByteEnd}] = true
		}
	}
	for key, source := range expected {
		var spans []span
		for s := range seen[key] {
			spans = append(spans, s)
		}
		sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
		position := 0
		for _, s := range spans {
			if s.start != position {
				t.Fatal("gap or overlapping source chunks", key, position, s)
			}
			position = s.end
		}
		if position != len(source) {
			t.Fatal("source tail was discarded", key, position, len(source))
		}
	}
}

func TestSegmentsFallbackKeepsAllBlocksAndToolResults(t *testing.T) {
	messages := []domain.UnifiedMessage{{Role: domain.RoleSystem, Text: "SYSTEM_SECRET"}, {Role: domain.RoleUser, Blocks: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "first"}, {Type: domain.ContentBlockText, Text: strings.Repeat("later ", 100) + "BLOCK_TAIL"}}}, {Role: domain.RoleTool, ToolResults: []domain.ToolResult{{Output: map[string]any{"result": strings.Repeat("output ", 100) + "TOOL_TAIL"}}}}}
	segments := BuildSegments("u", "c", messages, time.Unix(1, 0), DefaultSegmentOptions())
	var text strings.Builder
	for _, s := range segments {
		text.WriteString(s.Content)
		if len(s.Content) > 1200 {
			t.Fatal("unbounded tool output")
		}
	}
	if !strings.Contains(text.String(), "BLOCK_TAIL") || !strings.Contains(text.String(), "TOOL_TAIL") || strings.Contains(text.String(), "SYSTEM_SECRET") {
		t.Fatal("lost block/tool output or indexed injected system")
	}
}

func TestSegmentOverlapMetadataReflectsActualPreviousWindow(t *testing.T) {
	messages := []domain.UnifiedMessage{{Role: domain.RoleUser, Text: strings.Repeat("a", 80)}, {Role: domain.RoleUser, Text: strings.Repeat("b", 80)}, {Role: domain.RoleUser, Text: "c"}}
	segments := BuildSegments("u", "c", messages, time.Unix(1, 0), SegmentOptions{MaxBytes: 100, MaxMessages: 6, Overlap: 2})
	if len(segments) != 2 || segments[1].OverlapMessageCount != 0 {
		t.Fatal("reported overlap which never existed", segments)
	}
}
