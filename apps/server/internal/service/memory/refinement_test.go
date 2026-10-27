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

func planFixture(queries []EvidenceQuery) json.RawMessage {
	raw, _ := json.Marshal(EvidencePlan{Aspects: []string{"requested events"}, Queries: queries})
	return raw
}

func TestEvidencePlanValidation(t *testing.T) {
	good := planFixture([]EvidenceQuery{{Query: "Alice business events", Reason: "cover relevant events"}})
	var plan EvidencePlan
	if err := memoryllm.Decode(good, &plan); err != nil || !validEvidencePlan(plan) {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"aspects":[],"queries":[{"query":"events","reason":"event"}]}`,
		`{"aspects":["events"],"queries":[]}`,
		`{"aspects":["events"," EVENTS "],"queries":[{"query":"events","reason":"event"}]}`,
		`{"aspects":["events"],"queries":[{"query":"x","reason":"y"},{"query":"X ","reason":"y"}]}`,
		`{"sufficient":true,"aspects":["events"],"queries":[]}`,
	} {
		if err := memoryllm.Decode([]byte(raw), &plan); err == nil && validEvidencePlan(plan) {
			t.Fatal("accepted invalid or retired plan", raw)
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
		if r.Task == "evidence.select" {
			return json.Marshal(EvidenceSelection{SelectedIDs: []string{"m1", "m2"}, Checks: []EvidenceCheck{{Aspect: "distinct events", EvidenceIDs: []string{"m1", "m2"}}}})
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("no shared deadline")
		}
		raw, _ := json.Marshal(r.Input)
		if len(raw) > 1024 || strings.Contains(string(raw), "SECRET_SENTINEL") || r.MaxOutputTokens != 2048 || r.Role != "analysis_model" {
			t.Fatal("unsafe planner request")
		}
		return planFixture([]EvidenceQuery{{Query: "Alice business", Reason: "other events"}, {Query: "Alice event", Reason: "different events"}}), nil
	})
	base.Metadata["secret"] = "SECRET_SENTINEL"
	input := domain.SearchInput{UserID: "owner", Query: "Alice events", MemoryTypes: []domain.MemoryType{domain.MemoryTypeFact}}
	docs, diag, err := engine.RefineEvidence(ctx, input, seedForTest([]domain.MemoryDocument{base}), generator, RefinementOptions{TopN: 5, PlanBytes: 1024})
	if err != nil || calls != 2 || len(diag.Steps) != 2 || len(vectors.calls) != 2 || len(docs) > 5 || diag.Added != 1 {
		t.Fatalf("refinement %+v %v", diag, err)
	}
	for _, in := range vectors.calls {
		if in.UserID != "owner" || in.WorkspaceID != domain.PersonalWorkspaceID("owner") || in.GenerationID != base.GenerationID || len(in.AllowedTypes) != 2 || in.AllowedTypes[0] != domain.MemoryTypeFact || in.AllowedTypes[1] != domain.MemoryTypeSegment {
			t.Fatalf("scope/filter changed %+v", in)
		}
	}
	mixed := base
	mixed.GenerationID = "other-generation"
	if _, _, err = engine.RefineEvidence(ctx, input, seedForTest([]domain.MemoryDocument{base, mixed}), generator, RefinementOptions{}); !errors.Is(err, domain.ErrAccessDenied) || calls != 2 {
		t.Fatal("mixed generations reached planner", err)
	}
	foreign := base
	foreign.UserID = "foreign"
	if _, _, err = engine.RefineEvidence(ctx, input, seedForTest([]domain.MemoryDocument{foreign}), generator, RefinementOptions{}); !errors.Is(err, domain.ErrAccessDenied) || calls != 2 {
		t.Fatal("foreign source sent to planner", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err = engine.RefineEvidence(canceled, input, EvidenceSeed{}, generator, RefinementOptions{}); !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatal("cancel did not stop planner", err)
	}
	vectors.fail = domain.ErrAccessDenied
	if _, diag, err = engine.RefineEvidence(ctx, input, seedForTest([]domain.MemoryDocument{base}), generator, RefinementOptions{}); !errors.Is(err, domain.ErrAccessDenied) || len(diag.Steps) != 1 {
		t.Fatal("denied search continued", err)
	}
}

func TestRefinementAlwaysExploresAndRejectsInvalidPlans(t *testing.T) {
	repo := memorystore.NewInMemoryMemoryRepository()
	engine := NewEngine(repo, nil)
	base := coreFixture("base")
	for _, valid := range []bool{true, false} {
		calls := 0
		generator := planGeneratorFunc(func(_ context.Context, req memoryllm.Request) (json.RawMessage, error) {
			calls++
			if req.Task == "evidence.select" {
				return json.Marshal(EvidenceSelection{SelectedIDs: []string{"m1"}, Checks: []EvidenceCheck{{Aspect: "event", EvidenceIDs: []string{"m1"}}}})
			}
			payload := req.Input.(map[string]any)
			if len(payload) != 1 || payload["question"] != "question" {
				t.Fatal("planner saw source or evaluation hints", payload)
			}
			if !valid {
				return json.RawMessage(`{"sufficient":true}`), nil
			}
			return planFixture([]EvidenceQuery{{Query: "events", Reason: "cover requested event"}}), nil
		})
		docs, diag, err := engine.RefineEvidence(context.Background(), domain.SearchInput{UserID: "owner", Query: "question"}, seedForTest(base), generator, RefinementOptions{})
		if valid {
			if err != nil || calls != 2 || len(diag.Steps) != 1 || len(docs) != 1 || diag.SelectionPolicy != "additive_coverage_v2" {
				t.Fatal(docs, diag, err, calls)
			}
		} else if err == nil || calls != 1 {
			t.Fatal("retired stop plan advanced", err, calls)
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

func TestEvidenceCandidateAssemblyBalancesChannelsWithoutMixingQueryScores(t *testing.T) {
	base := coreFixture("a", "b", "c", "d", "e")
	got, err := AssembleEvidenceCandidates(base, [][]domain.MemoryDocument{coreFixture("a", "x", "y"), coreFixture("z")}, 5)
	if err != nil || len(got) != 5 || got[0].ID != "a" || got[1].ID != "x" || got[2].ID != "z" {
		t.Fatal(got, err)
	}
	got, err = AssembleEvidenceCandidates(base, [][]domain.MemoryDocument{coreFixture("a", "b")}, 5)
	if err != nil || len(got) != 5 || got[4].ID != "e" {
		t.Fatal("duplicate-only search lost initial candidates", got, err)
	}
	for _, limit := range []int{-1, 0, 151} {
		if _, err = AssembleEvidenceCandidates(base, nil, limit); err == nil {
			t.Fatal("unbounded pool accepted")
		}
	}
}

func TestRefinementPreservesBaselineAndDiscoversBelowTheFinalPage(t *testing.T) {
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
		if req.Task == "evidence.plan" {
			return planFixture([]EvidenceQuery{{Query: "unrelated", Reason: "check another aspect"}}), nil
		}
		for _, doc := range req.Input.(map[string]any)["memories"].([]planMemory) {
			if strings.HasSuffix(doc.Text, "\n"+target.Content) {
				return json.Marshal(EvidenceSelection{SelectedIDs: []string{doc.ID}, Checks: []EvidenceCheck{{Aspect: "event", EvidenceIDs: []string{doc.ID}}}})
			}
		}
		t.Fatal("evidence below the final page never reached selection")
		return nil, nil
	})
	docs, diag, err := engine.RefineEvidence(ctx, input, EvidenceSeed{Initial: initial.Result.Memories, Candidates: initial.Candidates}, generator, RefinementOptions{TopN: 20})
	if err != nil || len(docs) != 21 || docs[20].ID != target.ID || diag.Added != 1 || len(diag.Baseline.OmittedIDs) != 0 || diag.Steps[0].Returned != 0 {
		t.Fatal("narrow selection erased baseline or discoveries", len(docs), diag, err)
	}
	for i, original := range initial.Result.Memories {
		if docs[i].ID != original.ID || docs[i].Content != original.Content {
			t.Fatal("baseline order or content changed", i)
		}
	}
	base, err := engine.PrepareEvidence(ctx, input, initial.Result.Memories, EvidenceOptions{MaxBytes: 16000})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := engine.PrepareEvidence(ctx, input, docs, EvidenceOptions{MaxBytes: len(base.Text) + 10, RequiredIDs: diag.Baseline.RequestedIDs})
	coverage := MeasureEvidenceCoverage(diag.Baseline.RequestedIDs, packet.Memories)
	newCoverage := MeasureEvidenceCoverage([]string{target.ID}, packet.Memories)
	if err != nil || len(coverage.OmittedIDs) != 0 || len(newCoverage.OmittedIDs) != 1 || packet.Text != base.Text {
		t.Fatal("discovery displaced existing evidence under the same byte budget", coverage, newCoverage, err)
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

func TestEvidenceCandidateUnionDoesNotDropAFirstQueryBridge(t *testing.T) {
	channels := make([][]domain.MemoryDocument, 3)
	for c := range channels {
		for i := range 50 {
			channels[c] = append(channels[c], coreFixture(fmt.Sprintf("channel-%d-source-%d", c, i))...)
		}
	}
	pool, err := AssembleEvidenceCandidates(channels[0], channels[1:], 150)
	found := false
	for _, doc := range pool {
		found = found || doc.ID == channels[0][40].ID
	}
	if err != nil || len(pool) != 150 || !found {
		t.Fatal("already-scored first-query bridge removed before selector byte budget", len(pool), found, err)
	}
}
