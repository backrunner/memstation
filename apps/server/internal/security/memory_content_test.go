package security

import (
	"context"
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestMemoryReadDoesNotGrantSourceBodies(t *testing.T) {
	ctx := WithAccess(context.Background(), domain.AccessContext{Capabilities: []string{domain.CapabilityMemoryRead}})
	doc := domain.MemoryDocument{Type: domain.MemoryTypeFact, Content: "derived fact", Metadata: map[string]any{"source_quotes": []string{"private raw text"}, "custom_payload": "raw", "semantic_score": 0.8}, SourceRefs: []domain.SourceRef{{MessageID: "source", Revision: 2}}, Evidences: []domain.MemoryEvidence{{ExcerptText: "raw text", Metadata: map[string]any{"original": "raw"}}}}
	visible, ok := MemoryContent(ctx, doc)
	if !ok || visible.Content != doc.Content || len(visible.SourceRefs) != 1 || visible.Metadata["semantic_score"] != 0.8 || len(visible.Metadata) != 1 || visible.Evidences[0].ExcerptText != "" || visible.Evidences[0].Metadata != nil {
		t.Fatalf("restricted response: %+v", visible)
	}
	if doc.Metadata["source_quotes"] == nil || doc.Evidences[0].ExcerptText == "" {
		t.Fatal("redaction mutated cached content")
	}
	doc.Type = domain.MemoryTypeSegment
	if _, ok := MemoryContent(ctx, doc); ok {
		t.Fatal("source segment exposed through memory read")
	}
	allowed := WithAccess(context.Background(), domain.AccessContext{Capabilities: []string{domain.CapabilitySourcesRead}})
	if restored, ok := MemoryContent(allowed, doc); !ok || restored.Evidences[0].ExcerptText == "" {
		t.Fatal("authorized source evidence removed")
	}
}
