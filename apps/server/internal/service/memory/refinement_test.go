package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/memoryllm"
)

type planGeneratorFunc func(context.Context, memoryllm.Request) (json.RawMessage, error)

func seedForTest(docs []domain.MemoryDocument) EvidenceSeed {
	return EvidenceSeed{Initial: docs, Candidates: docs}
}

type seedOnlyRepository struct{ ports.MemoryRepository }

func (r seedOnlyRepository) Search(ctx context.Context, input domain.SearchInput) (domain.SearchResult, error) {
	if input.Query == "unrelated" {
		return domain.SearchResult{}, nil
	}
	return r.MemoryRepository.Search(ctx, input)
}

func (f planGeneratorFunc) Generate(ctx context.Context, r memoryllm.Request) (json.RawMessage, error) {
	return f(ctx, r)
}

func decisionFixture(core []string, next ...string) json.RawMessage {
	if core == nil {
		core = []string{}
	}
	if next == nil {
		next = []string{}
	}
	raw, _ := json.Marshal(evidenceDecision{Core: core, NextQueries: next})
	return raw
}

func blocksOf(t *testing.T, req memoryllm.Request) []planMemory {
	t.Helper()
	raw, _ := json.Marshal(req.Input.(map[string]any)["blocks"])
	var blocks []struct {
		Candidates []planMemory `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	out := []planMemory{}
	for _, b := range blocks {
		out = append(out, b.Candidates...)
	}
	return out
}

func labelOf(t *testing.T, req memoryllm.Request, content string) string {
	t.Helper()
	for _, m := range blocksOf(t, req) {
		if strings.HasSuffix(m.Text, "\n"+content) {
			return m.ID
		}
	}
	t.Fatalf("%q was not shown to the model", content)
	return ""
}

func TestEvidenceDecisionValidation(t *testing.T) {
	labels := map[string]domain.MemoryDocument{"m1": {ID: "a"}, "m2": {ID: "b"}}
	for _, good := range []evidenceDecision{{}, {Core: []string{"m1", "m2"}}, {Core: []string{"m1"}, NextQueries: []string{"Alice 2023", "Bob", "Carol"}}} {
		if !validEvidenceDecision(good, labels) {
			t.Fatal("rejected", good)
		}
	}
	for _, bad := range []evidenceDecision{
		{Core: []string{"foreign"}},
		{Core: []string{"m1", "m1"}},
		{NextQueries: []string{" "}},
		{NextQueries: []string{"x", "X "}},
		{NextQueries: []string{"a", "b", "c", "d"}},
		{NextQueries: []string{strings.Repeat("x", 513)}},
	} {
		if validEvidenceDecision(bad, labels) {
			t.Fatal("accepted", bad)
		}
	}
}

type planVectors struct {
	ports.VectorStore
	calls []domain.VectorSearchInput
	fail  error
}

func (v *planVectors) Search(ctx context.Context, in domain.VectorSearchInput) ([]domain.VectorSearchResult, error) {
	v.calls = append(v.calls, in)
	if _, err := security.PersonalScope(ctx, "owner", ""); err != nil {
		return nil, err
	}
	return nil, v.fail
}

func TestRefinementPreservesScopeDeadlineAndBudgets(t *testing.T) {
	repo := memorystore.NewInMemoryMemoryRepository()
	ctx := context.Background()
	for _, id := range []string{"base", "other"} {
		if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: id, UserID: "owner", Type: domain.MemoryTypeFact, Content: "Alice business event " + id}); err != nil {
			t.Fatal(err)
		}
	}
	base, err := repo.GetDocument(ctx, "owner", "base", domain.MemoryTypeFact)
	if err != nil {
		t.Fatal(err)
	}
	vectors := &planVectors{}
	engine := NewEngine(repo, vectors)
	calls := 0
	generator := planGeneratorFunc(func(ctx context.Context, r memoryllm.Request) (json.RawMessage, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("no shared deadline")
		}
		raw, _ := json.Marshal(r.Input)
		if len(raw) > 1024 || strings.Contains(string(raw), "SECRET_SENTINEL") || r.MaxOutputTokens != 2048 || r.Role != "analysis_model" {
			t.Fatal("unsafe decision request")
		}
		if calls == 1 {
			return decisionFixture([]string{"m1"}, "Alice business", "Alice event"), nil
		}
		return decisionFixture(nil), nil
	})
	base.Metadata["secret"] = "SECRET_SENTINEL"
	input := domain.SearchInput{UserID: "owner", Query: "Alice events", MemoryTypes: []domain.MemoryType{domain.MemoryTypeFact}}
	docs, diag, err := engine.RefineEvidence(ctx, input, seedForTest([]domain.MemoryDocument{base}), generator, RefinementOptions{TopN: 5, PlanBytes: 1024})
	if err != nil || calls != 2 || len(diag.Steps) != 2 || len(vectors.calls) != 2 || docs[0].ID != base.ID {
		t.Fatalf("refinement %+v %v", diag, err)
	}
	for _, in := range vectors.calls {
		if in.UserID != "owner" || in.WorkspaceID != domain.PersonalWorkspaceID("owner") || in.GenerationID != base.GenerationID || len(in.AllowedTypes) != 2 || in.AllowedTypes[0] != domain.MemoryTypeFact {
			t.Fatalf("scope/filter changed %+v", in)
		}
	}
	calls = 2
	mixed := base
	mixed.GenerationID = "other-generation"
	if _, _, err = engine.RefineEvidence(ctx, input, seedForTest([]domain.MemoryDocument{base, mixed}), generator, RefinementOptions{}); !errors.Is(err, domain.ErrAccessDenied) || calls != 2 {
		t.Fatal("mixed generations reached the model", err)
	}
	foreign := base
	foreign.UserID = "foreign"
	if _, _, err = engine.RefineEvidence(ctx, input, seedForTest([]domain.MemoryDocument{foreign}), generator, RefinementOptions{}); !errors.Is(err, domain.ErrAccessDenied) || calls != 2 {
		t.Fatal("foreign source sent to the model", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err = engine.RefineEvidence(canceled, input, EvidenceSeed{}, generator, RefinementOptions{}); !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatal("cancel did not stop the model", err)
	}
	calls = 0
	vectors.fail = domain.ErrAccessDenied
	if _, diag, err = engine.RefineEvidence(ctx, input, seedForTest([]domain.MemoryDocument{base}), generator, RefinementOptions{}); !errors.Is(err, domain.ErrAccessDenied) || len(diag.Steps) != 1 {
		t.Fatal("denied search continued", err)
	}
}

func TestRefinementShowsOpaqueEvidenceAndStopsWhenTheModelDoes(t *testing.T) {
	engine := NewEngine(memorystore.NewInMemoryMemoryRepository(), nil)
	base := coreFixture("base")
	base[0].ID = "STORED_ID_SENTINEL"
	base[0].Metadata = map[string]any{"answer": "GOLD_SENTINEL"}
	calls := 0
	generator := planGeneratorFunc(func(_ context.Context, req memoryllm.Request) (json.RawMessage, error) {
		calls++
		payload := req.Input.(map[string]any)
		raw, _ := json.Marshal(payload)
		if req.Instruction != EvidenceSearchInstruction || len(payload) != 3 || payload["question"] != "question" || strings.Contains(string(raw), "GOLD_SENTINEL") || strings.Contains(string(raw), "STORED_ID_SENTINEL") {
			t.Fatalf("model saw more than the question and opaque evidence: %s", raw)
		}
		return decisionFixture(nil), nil
	})
	docs, diag, err := engine.RefineEvidence(context.Background(), domain.SearchInput{UserID: "owner", Query: "question"}, seedForTest(base), generator, RefinementOptions{})
	if err != nil || calls != 1 || diag.Status != "refined" || len(docs) != 1 || len(diag.Steps) != 0 || diag.Baseline == nil || diag.SelectionPolicy != "core_first_v1" {
		t.Fatal(docs, diag, err, calls)
	}
}

func TestRefinementRejectsInvalidDecisionsAndEnforcesTheDeadline(t *testing.T) {
	engine := NewEngine(memorystore.NewInMemoryMemoryRepository(), nil)
	base := coreFixture("base")
	for _, reply := range []string{`{"sufficient":true}`, `{"core":["m9"],"next_queries":[]}`} {
		_, diag, err := engine.RefineEvidence(context.Background(), domain.SearchInput{UserID: "owner", Query: "question"}, seedForTest(base), planGeneratorFunc(func(context.Context, memoryllm.Request) (json.RawMessage, error) {
			return json.RawMessage(reply), nil
		}), RefinementOptions{})
		if err == nil || diag.Status != "decision_failed" {
			t.Fatal("invalid decision advanced", reply, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, err := engine.RefineEvidence(ctx, domain.SearchInput{UserID: "owner", Query: "question"}, seedForTest(base), planGeneratorFunc(func(ctx context.Context, _ memoryllm.Request) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}), RefinementOptions{Timeout: time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("deadline not enforced", err)
	}
}

// A record kept from below the final page must lead the result, or a full context
// silently drops what the search was run to find.
func TestRefinementPutsKeptEvidenceFirstAndItSurvivesAFullContext(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	for i := range 40 {
		doc := domain.MemoryDocument{ID: fmt.Sprintf("event-%02d", i), UserID: "owner", Type: domain.MemoryTypeFact, Content: fmt.Sprintf("signal event %02d", i), CreatedAt: time.Unix(int64(i+1), 0), UpdatedAt: time.Unix(int64(i+1), 0)}
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	engine := NewEngine(seedOnlyRepository{MemoryRepository: repo}, nil)
	input := domain.SearchInput{UserID: "owner", Query: "signal", TopN: 20}
	initial, err := engine.SearchWithVariant(ctx, input, SearchVariantFull)
	if err != nil || len(initial.Result.Memories) != 20 || len(initial.Candidates) != 40 {
		t.Fatal("discovery pool was prematurely paginated", len(initial.Candidates), err)
	}
	target := initial.Candidates[30]
	generator := planGeneratorFunc(func(_ context.Context, req memoryllm.Request) (json.RawMessage, error) {
		return decisionFixture([]string{labelOf(t, req, target.Content)}), nil
	})
	docs, diag, err := engine.RefineEvidence(ctx, input, EvidenceSeed{Initial: initial.Result.Memories, Candidates: initial.Candidates}, generator, RefinementOptions{TopN: 20})
	if err != nil || docs[0].ID != target.ID || diag.Added != 1 || len(diag.Baseline.OmittedIDs) != 0 || len(docs) != 40 {
		t.Fatal("kept evidence does not lead or the fill was lost", len(docs), diag, err)
	}
	base, err := engine.PrepareEvidence(ctx, input, initial.Result.Memories, EvidenceOptions{MaxBytes: 16000})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := engine.PrepareEvidence(ctx, input, docs, EvidenceOptions{MaxBytes: len(base.Text), RequiredIDs: diag.Core.RequestedIDs})
	if err != nil || len(MeasureEvidenceCoverage(diag.Core.RequestedIDs, packet.Memories).OmittedIDs) != 0 {
		t.Fatal("full context dropped kept evidence", err)
	}
}

func TestRefinementFollowsQueriesKeepsEachAspectAndStopsWhenSaturated(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	for _, id := range []string{"seed", "bridge", "other", "third"} {
		if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: id, UserID: "owner", Type: domain.MemoryTypeFact, Content: "signal " + id}); err != nil {
			t.Fatal(err)
		}
	}
	engine := NewEngine(repo, nil)
	seed, err := engine.SearchWithVariant(ctx, domain.SearchInput{UserID: "owner", Query: "signal seed", TopN: 1}, SearchVariantFull)
	if err != nil {
		t.Fatal(err)
	}
	round := 0
	generator := planGeneratorFunc(func(_ context.Context, req memoryllm.Request) (json.RawMessage, error) {
		round++
		if round == 1 {
			return decisionFixture([]string{labelOf(t, req, "signal seed")}, "signal bridge"), nil
		}
		for _, m := range blocksOf(t, req) {
			if strings.HasSuffix(m.Text, "\nsignal seed") {
				t.Fatal("a record already shown was offered again")
			}
		}
		return decisionFixture(nil, "another angle"), nil
	})
	docs, diag, err := engine.RefineEvidence(ctx, domain.SearchInput{UserID: "owner", Query: "signal seed", TopN: 1}, EvidenceSeed{Initial: seed.Result.Memories, Candidates: seed.Candidates}, generator, RefinementOptions{TopN: 5})
	if err != nil || round != 2 || len(diag.Rounds) != 2 || diag.Status != "refined" || docs[0].Content != "signal seed" {
		t.Fatal("search did not stop when a follow-up kept nothing", round, diag, docs, err)
	}
}

func TestRefinementSeedScopeAndBaselineIdentityAreValidatedBeforeTheModel(t *testing.T) {
	base := coreFixture("base")
	foreign := base[0]
	foreign.ID, foreign.UserID = "foreign", "another-owner"
	changed := base[0]
	changed.Revision++
	for _, seed := range []EvidenceSeed{
		{Initial: base, Candidates: append(append([]domain.MemoryDocument{}, base...), foreign)},
		{Initial: base},
		{Initial: base, Candidates: []domain.MemoryDocument{changed}},
	} {
		calls := 0
		generator := planGeneratorFunc(func(context.Context, memoryllm.Request) (json.RawMessage, error) {
			calls++
			return nil, nil
		})
		_, _, err := NewEngine(memorystore.NewInMemoryMemoryRepository(), nil).RefineEvidence(context.Background(), domain.SearchInput{UserID: "owner", Query: "event"}, seed, generator, RefinementOptions{})
		if err == nil || calls != 0 {
			t.Fatal("foreign, missing or revised seed reached model", err, calls)
		}
	}
}
