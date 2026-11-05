package analysis

import (
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/processing"
)

func TestLightweightRepeatedMentionPreservesRuneAndByteOffsets(t *testing.T) {
	source := Source{ID: "request-local-source", Text: "😀小林和小林", ByteOffset: 200, Role: domain.RoleUser, Ref: &domain.SourceRef{WorkspaceID: "workspace", MessageID: "message", Revision: 1, PartIndex: 0, Span: &domain.TextSpan{Start: 10, End: 16}}}
	doc, err := LightweightDocument("owner", "conversation", source, processing.Span{Start: 4, End: 6, Text: "小林", Label: "person", Confidence: .9}, "policy")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.SubjectActorIDs) != 0 || doc.Type != domain.MemoryTypeEntity || doc.Metadata["interpretation"] != "source_mention" {
		t.Fatal("mention became inferred actor/fact")
	}
	if len(doc.SourceRefs) != 1 || doc.SourceRefs[0].Span.Start != 14 || doc.SourceRefs[0].Span.End != 16 {
		t.Fatalf("wrong code-point span %+v", doc.SourceRefs)
	}
	quotes := doc.Metadata["source_quotes"].([]any)
	quote := quotes[0].(map[string]any)
	if quote["start_byte"] != 213 || quote["end_byte"] != 219 {
		t.Fatalf("wrong repeated mention byte span %+v", quote)
	}
	shorter, err := LightweightDocument("owner", "conversation", source, processing.Span{Start: 4, End: 5, Text: "小", Label: "person", Confidence: .9}, "policy")
	if err != nil || shorter.ID == doc.ID {
		t.Fatal("different mention boundaries collided")
	}
	source.Role = domain.RoleAssistant
	if _, err = LightweightDocument("owner", "conversation", source, processing.Span{Start: 4, End: 6, Text: "小林", Label: "person", Confidence: .9}, "policy"); err == nil {
		t.Fatal("assistant suggestion became user evidence")
	}
}
