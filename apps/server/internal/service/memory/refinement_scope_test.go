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
		if calls == 1 {
			return decisionFixture([]string{"m1"}, "project signal"), nil
		}
		return decisionFixture(nil), nil
	})
	input := domain.SearchInput{UserID: "owner", WorkspaceID: "project", Query: "project events"}
	docs, diag, err := engine.RefineEvidence(ctx, input, seedForTest([]domain.MemoryDocument{base}), generator, RefinementOptions{TopN: 5})
	if err != nil || calls != 2 || len(docs) != 2 || diag.Core == nil || len(vectors.calls) != 1 || docs[0].WorkspaceID != "project" || docs[0].PartitionID != "private-agent" {
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
		return decisionFixture([]string{"m1"}), nil
	})
	docs, _, err := engine.RefineEvidence(context.Background(), domain.SearchInput{UserID: "owner", Query: "preferences"}, seedForTest(base), generator, RefinementOptions{})
	if err != nil || len(docs) != 1 || docs[0].GenerationID != "" || calls != 1 {
		t.Fatal("formal generationless key memory excluded", docs, err, calls)
	}
	base[0].WorkspaceID = ""
	if _, _, err = engine.RefineEvidence(context.Background(), domain.SearchInput{UserID: "owner", Query: "preferences"}, seedForTest(base), generator, RefinementOptions{}); !errors.Is(err, domain.ErrAccessDenied) || calls != 1 {
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

func TestRefinementEmptyWorkspaceDoesNotInventSourcesOrCallTheModel(t *testing.T) {
	calls := 0
	generator := planGeneratorFunc(func(context.Context, memoryllm.Request) (json.RawMessage, error) {
		calls++
		return nil, nil
	})
	docs, diag, err := NewEngine(memorystore.NewInMemoryMemoryRepository(), nil).RefineEvidence(context.Background(), domain.SearchInput{UserID: "owner", Query: "requested event"}, EvidenceSeed{}, generator, RefinementOptions{})
	if err != nil || len(docs) != 0 || calls != 0 || diag.Status != "no_evidence" {
		t.Fatal("empty workspace was treated as a model failure", docs, diag, err)
	}
}
