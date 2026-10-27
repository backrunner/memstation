package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	analysisservice "memstation/apps/server/internal/service/analysis"
	conversationservice "memstation/apps/server/internal/service/conversation"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	"memstation/apps/server/internal/store/vector"
	"memstation/apps/server/internal/testutil/memoryllmfixture"
)

func TestSegmentEvidenceUsesExactSliceAndRejectsChangedSource(t *testing.T) {
	part := domain.MessagePart{PartID: "p", UserID: "u", ConversationID: "c", Role: domain.RoleUser, TextContent: strings.Repeat("prefix ", 360) + "TAIL-CANCELLED"}
	parts := []domain.MessagePart{part}
	segments := conversationservice.BuildSegmentsFromTimeline("u", "c", nil, parts, nil, time.Unix(1, 0), conversationservice.DefaultSegmentOptions())
	doc := segmentDocument(segments[len(segments)-1])
	doc.Metadata["source_conversation_revision"] = int64(7)
	evidence, err := segmentNodeEvidences(doc, parts, nil, nil)
	if err != nil || len(evidence) != 1 || !strings.Contains(evidence[0].ExcerptText, "TAIL-CANCELLED") || evidence[0].Metadata["source_conversation_revision"] != int64(7) {
		t.Fatal(evidence, err)
	}
	firstDoc := segmentDocument(segments[0])
	firstDoc.Metadata["source_conversation_revision"] = int64(7)
	first, err := segmentNodeEvidences(firstDoc, parts, nil, nil)
	if err != nil || first[0].Metadata["source_id"] != evidence[0].Metadata["source_id"] {
		t.Fatal("slices of same source counted as independent corroboration", err)
	}
	parts[0].TextContent += "changed"
	if _, err := segmentNodeEvidences(doc, parts, nil, nil); err == nil {
		t.Fatal("accepted changed source revision")
	}
	parts[0] = part
	parts[0].UserID = "foreign"
	if _, err := segmentNodeEvidences(doc, parts, nil, nil); err == nil {
		t.Fatal("accepted foreign part")
	}
	parts[0] = part
	raw, _ := json.Marshal(doc.Metadata["source_slices"])
	var slices []domain.SegmentSourceSlice
	if err = json.Unmarshal(raw, &slices); err != nil {
		t.Fatal(err)
	}
	slices[0].ContentByteEnd = len(doc.Content) + 1
	doc.Metadata["source_slices"] = slices
	if _, err := segmentNodeEvidences(doc, parts, nil, nil); err == nil {
		t.Fatal("accepted invalid rendered offsets")
	}
}

func TestLosslessSlicesSurviveGenerationRebuild(t *testing.T) {
	ctx := context.Background()
	text := strings.Repeat("source context ", 180) + "REBUILD-TAIL"
	record := domain.ConversationRecord{UserID: "u", ConversationID: "c", Revision: 9, UpdatedAt: time.Now().UTC(), Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: text}}, Parts: []domain.MessagePart{{PartID: "p", UserID: "u", ConversationID: "c", Role: domain.RoleUser, TextContent: text}}, AgentID: "fixture-agent"}
	gens := memorygenservice.NewService(nil, "fixture")
	snapshot, _, err := gens.CaptureSourceSnapshot(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	gen, _, err := gens.CreateRebuildGeneration(ctx, memorygenservice.CreateRebuildInput{UserID: "u", Scope: "user"})
	if err != nil {
		t.Fatal(err)
	}
	repo := memorystore.NewInMemoryMemoryRepository()
	svc := Services{Generations: gens, Memories: repo, Vectors: vector.NewPGVectorStore(nil), Publisher: &recordingPublisher{}, Analysis: analysisservice.NewService(memoryllmfixture.Generator{}), TaskAuthority: fixtureTaskAuthority{}}
	task := newTask(t, TypeMemoryRebuildConversation, MemoryRebuildConversationPayload{UserID: "u", GenerationID: gen.GenerationID, SnapshotID: snapshot.SnapshotID})
	handler := handleMemoryRebuildConversation(nilLogger(), svc)
	if err = handler(ctx, task); err != nil {
		t.Fatal(err)
	}
	docs, err := repo.ListDocumentsInGeneration(ctx, "u", gen.GenerationID, []domain.MemoryType{domain.MemoryTypeSegment}, 100)
	if err != nil || len(docs) < 3 {
		t.Fatal("rebuild omitted source chunks", len(docs), err)
	}
	found := false
	for _, d := range docs {
		if d.GenerationID != gen.GenerationID || d.Metadata["source_snapshot_id"] != snapshot.SnapshotID || d.Metadata["source_slices"] == nil {
			t.Fatal("rebuild lost scope/source provenance")
		}
		if strings.Contains(d.Content, "REBUILD-TAIL") {
			found = true
		}
	}
	if !found {
		t.Fatal("rebuild lost tail")
	}
	if err = handler(ctx, task); err != nil {
		t.Fatal("rebuild retry", err)
	}
	again, err := repo.ListDocumentsInGeneration(ctx, "u", gen.GenerationID, []domain.MemoryType{domain.MemoryTypeSegment}, 100)
	if err != nil || len(again) != len(docs) {
		t.Fatal("rebuild retry duplicated chunks", err)
	}
}

func TestSegmentArtifactViewsShareOneCorroborationSource(t *testing.T) {
	part := domain.MessagePart{PartID: "p", Role: domain.RoleUser, TextContent: "original"}
	artifacts := []domain.MessagePartArtifact{{PartID: "p", ArtifactID: "ocr", TextContent: "recognized text"}, {PartID: "p", ArtifactID: "caption", TextContent: "image description"}}
	segments := conversationservice.BuildSegmentsFromTimeline("u", "c", nil, []domain.MessagePart{part}, artifacts, time.Unix(1, 0), conversationservice.DefaultSegmentOptions())
	evidence, err := segmentNodeEvidences(segmentDocument(segments[0]), []domain.MessagePart{part}, artifacts, nil)
	if err != nil || len(evidence) != 3 {
		t.Fatal(evidence, err)
	}
	for _, e := range evidence {
		if e.Metadata["source_id"] != evidence[0].Metadata["source_id"] {
			t.Fatal("derived views inflated independent source count")
		}
	}
}

func TestSegmentMessageSlicesValidateOriginalWithoutMaterializedParts(t *testing.T) {
	messages := []domain.UnifiedMessage{{Role: domain.RoleUser, Blocks: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "first"}, {Type: domain.ContentBlockText, Text: "second"}}}}
	segments := conversationservice.BuildSegments("u", "c", messages, time.Unix(1, 0), conversationservice.DefaultSegmentOptions())
	doc := segmentDocument(segments[0])
	if _, err := segmentNodeEvidences(doc, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	messages[0].Blocks[1].Text = "edited after splitting"
	if _, err := segmentNodeEvidences(doc, nil, nil, messages); err == nil {
		t.Fatal("fallback source change was not rejected")
	}
}
