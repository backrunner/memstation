package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
)

// Optional artifact replay verifies packing changes without another reader or
// embedding call. External benchmark content is never copied into the repo.
func TestPipelineEvidenceArtifactReplay(t *testing.T) {
	dir := os.Getenv("MEMSTATION_EVAL_EVIDENCE_REPLAY_DIR")
	dataset := os.Getenv("LOCOMO_DATASET_PATH")
	if dir == "" || dataset == "" {
		t.Skip("manual external evidence replay")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report LOCOMOPipelineReport
	if err = json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Answers) == 0 {
		t.Fatal("no answers")
	}
	samples, err := loadPipelineSamples(dataset, report.Answers[0].SampleID, 3)
	if err != nil {
		t.Fatal(err)
	}
	sample := samples[0]
	user := locomoUserID(sample.ID, "")
	ctx := context.Background()
	sources := conversationstore.NewInMemoryConversationRepository()
	repo := memorystore.NewInMemoryMemoryRepository()
	for _, session := range sample.Sessions {
		if err = sources.PersistExchange(ctx, domain.ConversationRecord{UserID: user, ConversationID: user + "-" + session.ID, Messages: session.Messages, Metadata: map[string]any{"source_session_time": session.Date}, AgentID: "fixture-agent"}); err != nil {
			t.Fatal(err)
		}
	}
	engine := memoryservice.NewEngine(repo, nil)
	engine.SetSourceContextReader(sources)
	for _, answer := range report.Answers {
		encoded, err := json.Marshal(answer.Retrieval)
		if err != nil {
			t.Fatal(err)
		}
		var retrieval memoryservice.DebugSearchResult
		if err = json.Unmarshal(encoded, &retrieval); err != nil {
			t.Fatal(err)
		}
		core := retrieval.Result.Memories
		for i := range core {
			core[i].Metadata["source_conversation_revision"] = int64(1)
			if err = repo.StoreDocument(ctx, core[i]); err != nil {
				t.Fatal(err)
			}
		}
		packet, err := engine.PrepareEvidence(ctx, domain.SearchInput{UserID: user}, core, memoryservice.EvidenceOptions{MaxBytes: 16000, NeighborTurns: 1})
		if err != nil || packet.Diagnostics.Unavailable != 0 || packet.Diagnostics.PackedSourceTurns == 0 {
			t.Fatalf("%s packet: %+v %v", answer.QuestionID, packet.Diagnostics, err)
		}
		if os.Getenv("MEMSTATION_EVAL_EVIDENCE_EXPECT_SAME") == "true" && packet.Text != answer.Context {
			t.Fatalf("context drift for %s", answer.QuestionID)
		}
		turns := map[string]bool{}
		for _, doc := range packet.Memories {
			if doc.Metadata["source_turn"] != true && doc.Metadata["source_context"] != true {
				continue
			}
			ref, _ := doc.Metadata["ref"].(string)
			if ref == "" || turns[ref] {
				t.Fatalf("empty or duplicate raw turn %s in %s", ref, answer.QuestionID)
			}
			turns[ref] = true
		}
		t.Logf("%s unique_turns=%d source_turns=%d bytes=%d", answer.QuestionID, len(turns), packet.Diagnostics.PackedSourceTurns, len(packet.Text))
	}
}
