package grpctransport

import (
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestMemoryDocumentDoesNotExposeLLMCheckpoints(t *testing.T) {
	metadata := map[string]any{"topic_label": "项目", "_llm_summary_checkpoint": map[string]any{"private": "raw result"}, "_llm_analysis_checkpoint": map[string]any{}, "_llm_keymemory_checkpoint": map[string]any{}}
	result := toProtoMemoryDocument(domain.MemoryDocument{ID: "summary", Metadata: metadata})
	fields := result.Metadata.AsMap()
	if len(fields) != 1 || fields["topic_label"] != "项目" {
		t.Fatal(fields)
	}
	if len(metadata) != 4 {
		t.Fatal("serialization mutated storage metadata")
	}
}
