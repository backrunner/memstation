package eval

import (
	"context"
	"fmt"
	"sort"
	"time"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
	sqlstore "memstation/apps/server/internal/store/sql"
	vectorstore "memstation/apps/server/internal/store/vector"
)

type RunOptions struct {
	Variants []memoryservice.SearchVariant `json:"variants,omitempty"`
	TopN     int                           `json:"top_n,omitempty"`
}

type BenchmarkReport struct {
	Dataset      string              `json:"dataset"`
	Description  string              `json:"description,omitempty"`
	GeneratedAt  time.Time           `json:"generated_at"`
	Variants     []VariantReport     `json:"variants"`
	Comparisons  []VariantComparison `json:"comparisons,omitempty"`
	Failures     []FailureSample     `json:"failures,omitempty"`
	Performance  *PerformanceReport  `json:"performance,omitempty"`
	Expectations SmokeExpectations   `json:"expectations,omitempty"`
}

type VariantReport struct {
	Variant   memoryservice.SearchVariant `json:"variant"`
	Aggregate AggregateMetrics            `json:"aggregate"`
	Cases     []CaseReport                `json:"cases"`
}

type CaseReport struct {
	CaseID  string                      `json:"case_id"`
	UserID  string                      `json:"user_id"`
	Query   string                      `json:"query"`
	Variant memoryservice.SearchVariant `json:"variant"`
	TopN    int                         `json:"top_n"`
	Metrics CaseMetrics                 `json:"metrics"`
	Results []RankedResult              `json:"results"`
	Notes   string                      `json:"notes,omitempty"`
}

type RankedResult struct {
	Rank           int                           `json:"rank"`
	Relevant       bool                          `json:"relevant"`
	Negative       bool                          `json:"negative"`
	Stale          bool                          `json:"stale"`
	Document       domain.MemoryDocument         `json:"document"`
	ScoreBreakdown memoryservice.ScoreComponents `json:"score_breakdown"`
}

type FailureSample struct {
	Variant string   `json:"variant"`
	CaseID  string   `json:"case_id"`
	Reasons []string `json:"reasons"`
}

type harness struct {
	engine *memoryservice.Engine
}

func RunBenchmark(ctx context.Context, dataset Dataset, options RunOptions) (BenchmarkReport, error) {
	if err := dataset.Validate(); err != nil {
		return BenchmarkReport{}, err
	}
	variants := options.Variants
	if len(variants) == 0 {
		variants = []memoryservice.SearchVariant{
			memoryservice.SearchVariantFull,
			memoryservice.SearchVariantFullNoRelations,
			memoryservice.SearchVariantSemanticOnly,
			memoryservice.SearchVariantSemanticImportance,
		}
	}
	bench, err := newHarness(ctx, dataset)
	if err != nil {
		return BenchmarkReport{}, err
	}

	report := BenchmarkReport{
		Dataset:      dataset.Name,
		Description:  dataset.Description,
		GeneratedAt:  time.Now().UTC(),
		Expectations: dataset.SmokeExpectations,
	}
	for _, variant := range variants {
		variantReport, failures, err := runVariant(ctx, bench, dataset, variant, options.TopN)
		if err != nil {
			return BenchmarkReport{}, err
		}
		report.Variants = append(report.Variants, variantReport)
		report.Failures = append(report.Failures, failures...)
	}

	report.Comparisons = buildComparisons(report.Variants)
	return report, nil
}

func newHarness(ctx context.Context, dataset Dataset) (*harness, error) {
	repo := sqlstore.NewInMemoryMemoryRepository()
	vectors := vectorstore.NewPGVectorStore(nil)
	for _, item := range dataset.Documents {
		doc := item.ToDomain()
		if err := repo.StoreDocument(ctx, doc); err != nil {
			return nil, err
		}
		if err := vectors.Upsert(ctx, domain.VectorUpsertRequest{
			UserID:           doc.UserID,
			NodeID:           doc.NodeID,
			NodeType:         string(doc.Type),
			EmbeddingVersion: 1,
			EmbeddingModel:   "benchmark-local-deterministic",
			Content:          doc.Content,
		}); err != nil {
			return nil, err
		}
	}
	for _, item := range dataset.Relations {
		if err := repo.LinkDocuments(ctx, item.ToDomain()); err != nil {
			return nil, err
		}
	}
	return &harness{engine: memoryservice.NewEngine(repo, vectors)}, nil
}

func runVariant(ctx context.Context, bench *harness, dataset Dataset, variant memoryservice.SearchVariant, topN int) (VariantReport, []FailureSample, error) {
	cases := dataset.SortedQueries()
	report := VariantReport{
		Variant: variant,
		Cases:   make([]CaseReport, 0, len(cases)),
	}
	failures := make([]FailureSample, 0)
	for _, query := range cases {
		limit := query.TopN
		if limit <= 0 && topN > 0 {
			limit = topN
		}
		if limit <= 0 {
			limit = 10
		}
		debug, err := bench.engine.SearchWithVariant(ctx, domain.SearchInput{
			UserID:      query.UserID,
			Query:       query.Query,
			TopN:        limit,
			MemoryTypes: query.MemoryTypes,
		}, variant)
		if err != nil {
			return VariantReport{}, nil, fmt.Errorf("run case %s: %w", query.CaseID, err)
		}
		results := rankResults(query, debug)
		caseReport := CaseReport{
			CaseID:  query.CaseID,
			UserID:  query.UserID,
			Query:   query.Query,
			Variant: variant,
			TopN:    limit,
			Metrics: computeCaseMetrics(query, results),
			Results: results,
			Notes:   query.Notes,
		}
		report.Cases = append(report.Cases, caseReport)
		if sample := buildFailure(caseReport); sample != nil {
			failures = append(failures, *sample)
		}
	}
	report.Aggregate = summarizeCases(report.Cases)
	return report, failures, nil
}

func rankResults(query QueryCase, debug memoryservice.DebugSearchResult) []RankedResult {
	positives := asSet(query.Positives)
	negatives := asSet(query.Negatives)
	stale := asSet(query.StaleDocumentIDs)
	out := make([]RankedResult, 0, len(debug.Result.Memories))
	for idx, doc := range debug.Result.Memories {
		components := debug.Components[doc.ID]
		_, relevant := positives[doc.ID]
		_, negative := negatives[doc.ID]
		_, staleHit := stale[doc.ID]
		out = append(out, RankedResult{
			Rank:           idx + 1,
			Relevant:       relevant,
			Negative:       negative,
			Stale:          staleHit,
			Document:       doc,
			ScoreBreakdown: components,
		})
	}
	return out
}

func buildFailure(report CaseReport) *FailureSample {
	reasons := make([]string, 0, 4)
	if len(report.Metrics.MissingPositiveIDs) > 0 {
		reasons = append(reasons, "missing_positives")
	}
	if len(report.Metrics.UnexpectedNegativeIDs) > 0 {
		reasons = append(reasons, "unexpected_negative")
	}
	if report.Metrics.WrongUserLeak {
		reasons = append(reasons, "wrong_user_leak")
	}
	if report.Metrics.StaleFactTop3Rate > 0 {
		reasons = append(reasons, "stale_fact_in_top3")
	}
	if len(reasons) == 0 {
		return nil
	}
	return &FailureSample{
		Variant: string(report.Variant),
		CaseID:  report.CaseID,
		Reasons: reasons,
	}
}

func buildComparisons(variants []VariantReport) []VariantComparison {
	if len(variants) < 2 {
		return nil
	}
	indexed := make(map[memoryservice.SearchVariant]VariantReport, len(variants))
	for _, variant := range variants {
		indexed[variant.Variant] = variant
	}
	pairs := [][2]memoryservice.SearchVariant{
		{memoryservice.SearchVariantFullNoRelations, memoryservice.SearchVariantFull},
		{memoryservice.SearchVariantSemanticOnly, memoryservice.SearchVariantSemanticImportance},
		{memoryservice.SearchVariantSemanticImportance, memoryservice.SearchVariantFull},
	}
	comparisons := make([]VariantComparison, 0, len(pairs))
	for _, pair := range pairs {
		baseline, ok := indexed[pair[0]]
		if !ok {
			continue
		}
		candidate, ok := indexed[pair[1]]
		if !ok {
			continue
		}
		comparisons = append(comparisons, compareVariants(baseline, candidate))
	}
	sort.Slice(comparisons, func(i, j int) bool {
		if comparisons[i].BaselineVariant == comparisons[j].BaselineVariant {
			return comparisons[i].CandidateVariant < comparisons[j].CandidateVariant
		}
		return comparisons[i].BaselineVariant < comparisons[j].BaselineVariant
	})
	return comparisons
}

func ValidateSmokeExpectations(report BenchmarkReport) error {
	if report.Expectations == (SmokeExpectations{}) {
		return nil
	}
	full, ok := report.findVariant(memoryservice.SearchVariantFull)
	if !ok {
		return fmt.Errorf("full variant is missing from benchmark report")
	}
	if full.Aggregate.RecallAt3 < report.Expectations.MinRecallAt3 {
		return fmt.Errorf("full recall@3 %.4f is below %.4f", full.Aggregate.RecallAt3, report.Expectations.MinRecallAt3)
	}
	if full.Aggregate.MRR < report.Expectations.MinMRR {
		return fmt.Errorf("full mrr %.4f is below %.4f", full.Aggregate.MRR, report.Expectations.MinMRR)
	}
	if full.Aggregate.NDCGAt5 < report.Expectations.MinNDCGAt5 {
		return fmt.Errorf("full ndcg@5 %.4f is below %.4f", full.Aggregate.NDCGAt5, report.Expectations.MinNDCGAt5)
	}
	if full.Aggregate.WrongUserLeakage > report.Expectations.MaxWrongUserLeakage {
		return fmt.Errorf("wrong-user leakage %.4f exceeds %.4f", full.Aggregate.WrongUserLeakage, report.Expectations.MaxWrongUserLeakage)
	}
	if full.Aggregate.StaleFactTop3Rate > report.Expectations.MaxStaleFactTop3Rate {
		return fmt.Errorf("stale fact top3 rate %.4f exceeds %.4f", full.Aggregate.StaleFactTop3Rate, report.Expectations.MaxStaleFactTop3Rate)
	}
	comparison, ok := report.findComparison(memoryservice.SearchVariantFullNoRelations, memoryservice.SearchVariantFull)
	if report.Expectations.MinRelationExpansionGain > 0 {
		if !ok {
			return fmt.Errorf("full vs full_no_relations comparison is missing")
		}
		if comparison.RelationPositiveGain < report.Expectations.MinRelationExpansionGain {
			return fmt.Errorf("relation expansion gain %.4f is below %.4f", comparison.RelationPositiveGain, report.Expectations.MinRelationExpansionGain)
		}
	}
	return nil
}

func (r BenchmarkReport) findVariant(variant memoryservice.SearchVariant) (VariantReport, bool) {
	for _, item := range r.Variants {
		if item.Variant == variant {
			return item, true
		}
	}
	return VariantReport{}, false
}

func (r BenchmarkReport) findComparison(baseline, candidate memoryservice.SearchVariant) (VariantComparison, bool) {
	for _, item := range r.Comparisons {
		if item.BaselineVariant == string(baseline) && item.CandidateVariant == string(candidate) {
			return item, true
		}
	}
	return VariantComparison{}, false
}

func asSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}
