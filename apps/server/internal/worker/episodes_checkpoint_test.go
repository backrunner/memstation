package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/analysis"
	"memstation/apps/server/internal/service/memoryllm"
)

func TestEpisodePolicyDoesNotReuseLegacyAnalysisCheckpoint(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	summary := domain.MemoryDocument{ID: "summary", NodeID: "summary-node", UserID: "owner", ConversationID: "c", Type: domain.MemoryTypeSummary, Content: "Alice cancelled the visit"}
	if err := repo.StoreDocument(ctx, summary); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetDocument(ctx, "owner", summary.ID, summary.Type)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	generator := planGeneratorForEpisodes(func(_ context.Context, req memoryllm.Request) (json.RawMessage, error) {
		calls++
		if !strings.Contains(req.Instruction, "Additionally return episodes") {
			return json.RawMessage(`{"facts":[],"items":[],"impression":null}`), nil
		}
		return json.Marshal(analysis.Extraction{Facts: []analysis.Fact{}, Items: []analysis.GenericItem{}, Episodes: []analysis.SemanticEpisode{{Key: "visit", Title: "Cancelled visit", Status: "cancelled", Participants: []string{"Alice"}, Item: analysis.Item{Content: "Alice cancelled the visit.", Subject: "Alice", Importance: 1, DecayFactor: 1, Confidence: 1, Quotes: []analysis.Quote{{SourceID: "message-1", Text: "Alice cancelled the visit."}}}}}})
	})
	in := analysis.Input{UserID: "owner", ConversationID: "c", Summary: stored, Conversation: domain.ConversationRecord{UserID: "owner", ConversationID: "c", Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "Alice cancelled the visit."}}, AgentID: "fixture-agent"}}
	services := Services{Memories: repo, Analysis: analysis.NewService(generator), TaskAuthority: fixtureTaskAuthority{}}
	if _, err = analyzeWithCheckpoint(ctx, services, in); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	services.Analysis = analysis.NewServiceWithOptions(generator, analysis.Options{SemanticEpisodes: true})
	result, err := analyzeWithCheckpoint(ctx, services, in)
	if err != nil || calls != 2 || len(result.GenericItems) != 1 || result.GenericItems[0].Type != domain.MemoryTypeEpisode {
		t.Fatal("legacy checkpoint hid episodes", err, calls)
	}
	result, err = analyzeWithCheckpoint(ctx, services, in)
	if err != nil || calls != 2 || len(result.GenericItems) != 1 {
		t.Fatal("episode retry called model again", err, calls)
	}
}

type planGeneratorForEpisodes func(context.Context, memoryllm.Request) (json.RawMessage, error)

func (f planGeneratorForEpisodes) Generate(ctx context.Context, req memoryllm.Request) (json.RawMessage, error) {
	return f(ctx, req)
}

type policyEpisodeGenerator struct {
	planGeneratorForEpisodes
	policy string
}

func (g policyEpisodeGenerator) GenerationPolicyID() string { return g.policy }

func TestEpisodeToggleAndReasoningPolicyDoNotShareCheckpoints(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	summary := domain.MemoryDocument{ID: "s", UserID: "u", ConversationID: "c", Type: domain.MemoryTypeSummary, Content: "summary"}
	if err := repo.StoreDocument(ctx, summary); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetDocument(ctx, "u", "s", domain.MemoryTypeSummary)
	if err != nil {
		t.Fatal(err)
	}
	in := analysis.Input{UserID: "u", ConversationID: "c", Summary: stored, Conversation: domain.ConversationRecord{UserID: "u", ConversationID: "c", Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "Source statement."}}, AgentID: "fixture-agent"}}
	calls := 0
	g := planGeneratorForEpisodes(func(context.Context, memoryllm.Request) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"facts":[],"items":[],"episodes":[],"impression":null}`), nil
	})
	for i, setting := range []struct {
		enabled bool
		effort  string
	}{{false, "low"}, {true, "low"}, {true, "none"}} {
		services := Services{Memories: repo, Analysis: analysis.NewServiceWithOptions(policyEpisodeGenerator{planGeneratorForEpisodes: g, policy: setting.effort}, analysis.Options{SemanticEpisodes: setting.enabled}), TaskAuthority: fixtureTaskAuthority{}}
		if _, err := analyzeWithCheckpoint(ctx, services, in); err != nil || calls != i+1 {
			t.Fatal("incompatible settings shared a checkpoint", err, calls, setting)
		}
		if _, err := analyzeWithCheckpoint(ctx, services, in); err != nil || calls != i+1 {
			t.Fatal("identical setting regenerated", err, calls)
		}
	}
}

func TestUnchangedTextAtNewRevisionRebuildsEvidenceWithoutRegeneratingBatch(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	summary := domain.MemoryDocument{ID: "s", UserID: "u", ConversationID: "c", Type: domain.MemoryTypeSummary, Content: "Alice enjoys painting."}
	if err := repo.StoreDocument(ctx, summary); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetDocument(ctx, "u", "s", domain.MemoryTypeSummary)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	g := planGeneratorForEpisodes(func(context.Context, memoryllm.Request) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"facts":[{"key":"hobby","category":"profile","subject":"Alice","content":"Alice enjoys painting.","confidence":1,"importance":1,"decay_factor":1,"supersedes":[],"quotes":[{"source_id":"message-1","text":"Alice enjoys painting."}]}],"items":[],"episodes":[],"impression":null}`), nil
	})
	services := Services{Memories: repo, Analysis: analysis.NewService(g), TaskAuthority: fixtureTaskAuthority{}}
	in := analysis.Input{UserID: "u", ConversationID: "c", Summary: stored, Conversation: domain.ConversationRecord{UserID: "u", ConversationID: "c", Revision: 1, Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "Alice enjoys painting."}}, AgentID: "fixture-agent"}}
	first, err := analyzeWithCheckpoint(ctx, services, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Conversation.Revision = 2
	second, err := analyzeWithCheckpoint(ctx, services, in)
	if err != nil || calls != 1 || intValue(first.Fact.Metadata, "source_conversation_revision") != 1 || intValue(second.Fact.Metadata, "source_conversation_revision") != 2 {
		t.Fatal("stale evidence revision reused", err, calls, first.Fact.Metadata, second.Fact.Metadata)
	}
}

func TestValidatedBatchesSurviveLaterFailureAndChangedSources(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	summary := domain.MemoryDocument{ID: "s", UserID: "u", ConversationID: "c", Type: domain.MemoryTypeSummary, Content: "summary"}
	if err := repo.StoreDocument(ctx, summary); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetDocument(ctx, "u", "s", domain.MemoryTypeSummary)
	if err != nil {
		t.Fatal(err)
	}
	in := analysis.Input{UserID: "u", ConversationID: "c", Summary: stored, Conversation: domain.ConversationRecord{UserID: "u", ConversationID: "c", AgentID: "fixture-agent"}}
	for range 39 {
		in.Conversation.Messages = append(in.Conversation.Messages, domain.UnifiedMessage{Role: domain.RoleUser, Text: "A small original statement."})
	}
	calls := 0
	g := planGeneratorForEpisodes(func(context.Context, memoryllm.Request) (json.RawMessage, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("transient failure in second batch")
		}
		return json.RawMessage(`{"facts":[],"items":[],"episodes":[],"impression":null}`), nil
	})
	services := Services{Memories: repo, Analysis: analysis.NewServiceWithOptions(g, analysis.Options{SemanticEpisodes: true}), TaskAuthority: fixtureTaskAuthority{}}
	if result, err := analyzeWithCheckpoint(ctx, services, in); err == nil || len(result.Facts) != 0 || calls != 2 {
		t.Fatal("partial batch was published or failure hidden", err, calls)
	}
	if _, err := analyzeWithCheckpoint(ctx, services, in); err != nil || calls != 5 {
		t.Fatal("successful batch regenerated after failure", err, calls)
	}
	if _, err := analyzeWithCheckpoint(ctx, services, in); err != nil || calls != 5 {
		t.Fatal("completed analysis regenerated", err, calls)
	}
	in.Conversation.Messages[0].Text = "An edited original statement."
	if _, err := analyzeWithCheckpoint(ctx, services, in); err != nil || calls != 6 {
		t.Fatal("changed source reused stale batch or unchanged batches regenerated", err, calls)
	}
	stored, err = repo.GetDocument(ctx, "u", "s", domain.MemoryTypeSummary)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(stored)
	if strings.Contains(string(public), "_llm_") {
		t.Fatal("batch checkpoints exposed in API")
	}
}
