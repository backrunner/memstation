package memory

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/memoryllm"
)

func TestQuestionCoveragePoolPreservesEachComplementarySource(t *testing.T) {
	docs, err := AssembleEvidenceCandidates(coreFixture("a", "b", "c"), [][]domain.MemoryDocument{coreFixture("novel-first"), coreFixture("novel-second")}, 3)
	if err != nil || len(docs) != 3 || docs[0].ID != "a" || docs[1].ID != "novel-first" || docs[2].ID != "novel-second" {
		t.Fatal("initial duplicates displaced complementary channels", docs, err)
	}
}

func TestEvidenceSelectorUsesOnlySubmittedSourcesAndRejectsInventedSupport(t *testing.T) {
	pool := coreFixture("repeated", "event", "bridge")
	pool[0].ID = "SAMPLE_ID_SENTINEL"
	pool[0].ConversationID = "OWNER_ID_SENTINEL"
	pool[0].Metadata = map[string]any{"answer": "GOLD_SENTINEL", "secret": "SECRET_SENTINEL"}
	options := RefinementOptions{TopN: 2, PlanBytes: 2048}
	want := EvidenceSelection{SelectedIDs: []string{"event", "bridge"}, Checks: []EvidenceCheck{{Aspect: "event and attribution", EvidenceIDs: []string{"event", "bridge"}}}}
	generator := planGeneratorFunc(func(_ context.Context, req memoryllm.Request) (json.RawMessage, error) {
		data, _ := json.Marshal(req.Input)
		if req.Task != "evidence.select" || len(data) > options.PlanBytes || strings.Contains(string(data), "GOLD_SENTINEL") || strings.Contains(string(data), "SECRET_SENTINEL") || strings.Contains(string(data), "SAMPLE_ID_SENTINEL") || strings.Contains(string(data), "OWNER_ID_SENTINEL") {
			t.Fatal("unsafe selector input", req.Task)
		}
		wire := want
		wire.SelectedIDs = nil
		wire.Checks = []EvidenceCheck{{Aspect: "event and attribution"}}
		for _, source := range req.Input.(map[string]any)["memories"].([]planMemory) {
			if strings.HasSuffix(source.Text, "\nevent") || strings.HasSuffix(source.Text, "\nbridge") {
				wire.SelectedIDs = append(wire.SelectedIDs, source.ID)
				wire.Checks[0].EvidenceIDs = append(wire.Checks[0].EvidenceIDs, source.ID)
			}
		}
		return json.Marshal(wire)
	})
	docs, got, submitted, err := selectEvidenceSet(context.Background(), domain.SearchInput{UserID: "owner", Query: "Which event happened?"}, []string{"events"}, pool, generator, options)
	if err != nil || submitted != 3 || len(docs) != 2 || docs[0].ID != "event" || docs[1].ID != "bridge" || !reflect.DeepEqual(got, want) {
		t.Fatal(docs, got, err)
	}
	allowed := map[string]domain.MemoryDocument{"event": pool[1], "bridge": pool[2]}
	for _, invalid := range []EvidenceSelection{
		{SelectedIDs: []string{"foreign"}, Checks: want.Checks},
		{SelectedIDs: []string{"event", "event"}, Checks: want.Checks},
		{SelectedIDs: []string{"event"}, Checks: want.Checks},
		{SelectedIDs: []string{"event"}, Checks: []EvidenceCheck{{Aspect: "event", Gap: "unknown"}}},
		{Checks: []EvidenceCheck{{Aspect: "event"}}},
	} {
		if validEvidenceSelection(invalid, allowed, 2) {
			t.Fatal("accepted invented, duplicate or uncited evidence", invalid)
		}
	}
	if !validEvidenceSelection(EvidenceSelection{Checks: []EvidenceCheck{{Aspect: "event", Gap: "No evidence"}}}, allowed, 2) {
		t.Fatal("honest absence of evidence rejected")
	}
	pool[0].Content = strings.Repeat("large passage ", 500)
	_, _, submitted, err = selectEvidenceSet(context.Background(), domain.SearchInput{UserID: "owner", Query: "question"}, []string{"events"}, pool, generator, options)
	if err != nil || submitted != 2 {
		t.Fatal("oversized source was truncated or blocked later whole sources", err, submitted)
	}
}

type workspacePlanVectors struct {
	ports.VectorStore
	workspace string
	partition string
	calls     []domain.VectorSearchInput
}

func (v *workspacePlanVectors) Search(ctx context.Context, in domain.VectorSearchInput) ([]domain.VectorSearchResult, error) {
	workspace, err := security.DataScope(ctx, in.UserID, in.WorkspaceID)
	if err != nil || workspace != v.workspace || !reflect.DeepEqual(security.Partitions(ctx), []string{v.partition}) {
		return nil, domain.ErrAccessDenied
	}
	v.calls = append(v.calls, in)
	return nil, nil
}

func TestRefinementPreservesNonPersonalWorkspaceAndPrivatePartition(t *testing.T) {
	ctx, err := security.WithDataScope(context.Background(), "owner", "project", "private-agent")
	if err != nil {
		t.Fatal(err)
	}
	repo := memorystore.NewInMemoryMemoryRepository()
	for _, id := range []string{"base", "other"} {
		if err = repo.StoreDocument(ctx, domain.MemoryDocument{ID: id, UserID: "owner", WorkspaceID: "project", Type: domain.MemoryTypeFact, Content: "project signal " + id}); err != nil {
			t.Fatal(err)
		}
	}
	base, err := repo.GetDocument(ctx, "owner", "base", domain.MemoryTypeFact)
	if err != nil {
		t.Fatal(err)
	}
	vectors := &workspacePlanVectors{workspace: "project", partition: "private-agent"}
	engine := NewEngine(repo, vectors)
	calls := 0
	generator := planGeneratorFunc(func(ctx context.Context, req memoryllm.Request) (json.RawMessage, error) {
		calls++
		if security.WorkspaceID(ctx, "owner") != "project" || !security.CanReadPartition(ctx, "private-agent") {
			t.Fatal("model call lost bound scope")
		}
		if req.Task == "evidence.plan" {
			return planFixture([]EvidenceQuery{{Query: "project signal", Reason: "missing event"}}), nil
		}
		return json.Marshal(EvidenceSelection{SelectedIDs: []string{"m2", "m1"}, Checks: []EvidenceCheck{{Aspect: "events", EvidenceIDs: []string{"m2", "m1"}}}})
	})
	input := domain.SearchInput{UserID: "owner", WorkspaceID: "project", Query: "project events"}
	docs, diag, err := engine.RefineEvidence(ctx, input, seedForTest([]domain.MemoryDocument{base}), generator, RefinementOptions{TopN: 5})
	if err != nil || calls != 2 || len(docs) != 2 || diag.Selection == nil || len(vectors.calls) != 1 || docs[0].WorkspaceID != "project" || docs[0].PartitionID != "private-agent" {
		t.Fatal("workspace or partition refinement failed", docs, diag, err)
	}
	foreign := base
	foreign.PartitionID = "another-agent"
	if _, _, err = engine.RefineEvidence(ctx, input, seedForTest([]domain.MemoryDocument{foreign}), generator, RefinementOptions{}); !errors.Is(err, domain.ErrAccessDenied) || calls != 2 {
		t.Fatal("foreign partition reached planner", calls, err)
	}
}

func TestRefinementRequiresMaterializedWorkspaceAndKeepsGenerationlessKeyMemory(t *testing.T) {
	engine := NewEngine(memorystore.NewInMemoryMemoryRepository(), nil)
	base := coreFixture("profile")
	base[0].Type = domain.MemoryTypeKeyMemory
	base[0].GenerationID = ""
	calls := 0
	generator := planGeneratorFunc(func(_ context.Context, req memoryllm.Request) (json.RawMessage, error) {
		calls++
		if req.Task == "evidence.plan" {
			return planFixture([]EvidenceQuery{{Query: "preferences", Reason: "find requested preference"}}), nil
		}
		return json.Marshal(EvidenceSelection{SelectedIDs: []string{"m1"}, Checks: []EvidenceCheck{{Aspect: "preference", EvidenceIDs: []string{"m1"}}}})
	})
	docs, _, err := engine.RefineEvidence(context.Background(), domain.SearchInput{UserID: "owner", Query: "preferences"}, seedForTest(base), generator, RefinementOptions{})
	if err != nil || len(docs) != 1 || docs[0].GenerationID != "" || calls != 2 {
		t.Fatal("formal generationless key memory excluded", docs, err, calls)
	}
	base[0].WorkspaceID = ""
	if _, _, err = engine.RefineEvidence(context.Background(), domain.SearchInput{UserID: "owner", Query: "preferences"}, seedForTest(base), generator, RefinementOptions{}); !errors.Is(err, domain.ErrAccessDenied) || calls != 2 {
		t.Fatal("unmaterialized source reached model", err, calls)
	}
}

func TestEvidenceContextBudgetsExcludeStorageIdentitiesAndKeepSourceMapping(t *testing.T) {
	docs := coreFixture("first", "second")
	for i := range docs {
		docs[i].ID = "SAMPLE_ID_SENTINEL_" + strings.Repeat("x", 1024) + string(rune('a'+i))
		docs[i].ConversationID = "OWNER_ID_SENTINEL_" + strings.Repeat("y", 1024)
		docs[i].Content = "A whole source observation."
	}
	packet, err := NewEngine(nil, nil).PrepareEvidence(context.Background(), domain.SearchInput{UserID: "owner"}, docs, EvidenceOptions{MaxBytes: 1024})
	if err != nil || len(packet.Memories) != 2 || len(packet.Text) > 1024 || strings.Contains(packet.Text, "SENTINEL") || len(packet.SourceIDs) != 2 || packet.SourceIDs["memory-1"] != docs[0].ID || packet.SourceIDs["memory-2"] != docs[1].ID || packet.Memories[0].ID != docs[0].ID {
		t.Fatal("identity framing consumed evidence budget or lost provenance", packet, err)
	}
}

func TestRefinementEmptyWorkspaceDoesNotInventSourcesOrFailSelection(t *testing.T) {
	calls := 0
	generator := planGeneratorFunc(func(_ context.Context, req memoryllm.Request) (json.RawMessage, error) {
		calls++
		if req.Task != "evidence.plan" {
			t.Fatal("empty retrieval invoked selector")
		}
		return planFixture([]EvidenceQuery{{Query: "requested event", Reason: "find supporting sources"}}), nil
	})
	docs, diag, err := NewEngine(memorystore.NewInMemoryMemoryRepository(), nil).RefineEvidence(context.Background(), domain.SearchInput{UserID: "owner", Query: "requested event"}, EvidenceSeed{}, generator, RefinementOptions{})
	if err != nil || len(docs) != 0 || calls != 1 || diag.Status != "no_evidence" {
		t.Fatal("empty workspace was treated as a model failure", docs, diag, err)
	}
}
