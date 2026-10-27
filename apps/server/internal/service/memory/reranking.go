package memory

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/observability"
)

// RerankDiagnostics distinguishes an absent reranker from failed inference and
// genuine zero relevance. Reasons are fixed codes, never provider error bodies.
type RerankDiagnostics struct {
	InputBytes       int     `json:"input_bytes"`
	MaxDocumentBytes int     `json:"max_document_bytes"`
	ScorePolicy      string  `json:"score_policy"`
	Status           string  `json:"status"`
	Reason           string  `json:"reason,omitempty"`
	Candidates       int     `json:"candidates"`
	Scored           int     `json:"scored"`
	Excluded         int     `json:"excluded"`
	Selection        string  `json:"selection,omitempty"`
	Millis           float64 `json:"millis"`
}

func (e *Engine) rerankCandidates(ctx context.Context, input domain.SearchInput, candidates []searchCandidate) (scores map[string]float64, ranks map[string]int, diagnostic RerankDiagnostics) {
	scores, ranks = map[string]float64{}, map[string]int{}
	diagnostic.Status, diagnostic.ScorePolicy = "disabled", "hybrid_v1"
	ctx, span := observability.TraceSpan(ctx, "memory.rerank")
	start := time.Now()
	defer func() {
		diagnostic.Millis = float64(time.Since(start).Microseconds()) / 1000
		span.SetAttributes(
			attribute.String("memory.rerank.status", diagnostic.Status),
			attribute.String("memory.rerank.reason", diagnostic.Reason),
			attribute.String("memory.rerank.score_policy", diagnostic.ScorePolicy),
			attribute.Int("memory.rerank.candidates", diagnostic.Candidates),
			attribute.Int("memory.rerank.scored", diagnostic.Scored),
		)
		span.End()
	}()
	if e == nil {
		diagnostic.Reason = "no_engine"
		return
	}
	cfg := e.RerankConfig()
	diagnostic.Selection = cfg.CandidateSelection
	if !cfg.enabled() {
		diagnostic.Reason = "configuration"
		return
	}
	if e.reranker == nil {
		diagnostic.Status, diagnostic.Reason = "degraded", "unavailable"
		return
	}
	diagnostic.Status = "skipped"
	if strings.TrimSpace(input.Query) == "" {
		diagnostic.Reason = "empty_query"
		return
	}
	if input.TopN > cfg.MaxCandidates {
		diagnostic.Reason = "candidate_budget_below_top_n"
		return
	}
	ordered := append([]searchCandidate{}, candidates...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.PreliminaryScore != b.PreliminaryScore {
			return a.PreliminaryScore > b.PreliminaryScore
		}
		if !a.Document.UpdatedAt.Equal(b.Document.UpdatedAt) {
			return a.Document.UpdatedAt.After(b.Document.UpdatedAt)
		}
		return a.Document.NodeID < b.Document.NodeID
	})
	limit := min(cfg.candidateLimit(input.TopN), len(ordered))
	if cfg.CandidateSelection == "balanced" {
		ordered = balanceRerankChannels(ordered, limit)
	}
	documents := make([]domain.RerankDocument, 0, limit)
	allowed := map[string]bool{}
	for _, candidate := range ordered {
		if len(documents) >= limit {
			break
		}
		id, text := candidate.Document.NodeID, rerankDocumentText(candidate.Document)
		if id == "" || text == "" || allowed[id] {
			continue
		}
		allowed[id] = true
		documents = append(documents, domain.RerankDocument{ID: id, Text: text})
		diagnostic.InputBytes += len(text)
		diagnostic.MaxDocumentBytes = max(diagnostic.MaxDocumentBytes, len(text))
	}
	diagnostic.Candidates = len(documents)
	if len(documents) < 2 {
		diagnostic.Reason = "insufficient_candidates"
		return
	}
	results, err := e.reranker.Rerank(ctx, domain.RerankInput{
		UserID: input.UserID, Query: input.Query, Model: cfg.Model,
		TopN: len(documents), Documents: documents,
	})
	diagnostic.Status = "degraded"
	if err != nil {
		diagnostic.Reason = "provider_error"
		return
	}
	// The engine requests every submitted document. Applying a partial batch
	// mixes reranked and unranked candidates without a common score scale.
	if len(results) != len(documents) {
		diagnostic.Reason = "incomplete_response"
		return
	}
	seen := map[string]bool{}
	for _, item := range results {
		if !allowed[item.DocumentID] || seen[item.DocumentID] || math.IsNaN(item.Score) || math.IsInf(item.Score, 0) || item.Score < 0 || item.Score > 1 {
			diagnostic.Reason = "invalid_response"
			return
		}
		seen[item.DocumentID] = true
	}
	// Work on our own slice: implementations may cache or reuse their results.
	results = append([]domain.RerankResult(nil), results...)
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].DocumentID < results[j].DocumentID
		}
		return results[i].Score > results[j].Score
	})
	for i, item := range results {
		scores[item.DocumentID], ranks[item.DocumentID] = item.Score, i+1
	}
	diagnostic.Status, diagnostic.Reason, diagnostic.Scored = "applied", "", len(results)
	diagnostic.ScorePolicy = "relevance_v1"
	diagnostic.Excluded = len(candidates) - len(results)
	return
}

// Candidate collection uses the same bounded budget with reranking enabled or
// disabled, so toggling the model does not silently change recall coverage.
func (e *Engine) candidateRecallLimit(topN int) int {
	// Preserve the original four-times prefetch when it is wider than the
	// rerank pool, while allowing small final pages to collect enough evidence.
	return max(min(max(topN, 0), 50)*4, e.RerankConfig().candidateLimit(topN))
}

func (cfg RerankConfig) candidateLimit(topN int) int {
	cfg = cfg.normalized()
	// Cap before multiplication to avoid overflow on large internal requests.
	return min(max(min(max(topN, 0), cfg.MaxCandidates)*cfg.CandidateMultiplier, cfg.MinCandidates), cfg.MaxCandidates)
}

// A successful second stage defines the eligible pool. On any failure, retain
// the complete first-stage pool and scores instead of mixing score scales.
func rerankEligible(nodeID string, ranks map[string]int, diagnostic RerankDiagnostics) bool {
	if diagnostic.Status != "applied" {
		return true
	}
	_, ok := ranks[nodeID]
	return ok
}

// Reserve one third of the inference pool for each direct recall channel; fill
// the remainder by the existing preliminary score. A broad lexical match must
// not crowd every strong dense candidate out before the model can see it (or
// vice versa). This changes candidate admission, not the final ranking formula.
func balanceRerankChannels(ordered []searchCandidate, limit int) []searchCandidate {
	quota := limit / 3
	if quota == 0 || len(ordered) <= limit {
		return ordered
	}
	out := make([]searchCandidate, 0, len(ordered))
	seen := map[string]bool{}
	appendCandidate := func(c searchCandidate) bool {
		id := c.Document.NodeID
		if id == "" || seen[id] || rerankDocumentText(c.Document) == "" {
			return false
		}
		seen[id] = true
		out = append(out, c)
		return true
	}
	for _, signal := range []func(searchCandidate) float64{
		func(c searchCandidate) float64 { return c.BaseSemanticScore },
		func(c searchCandidate) float64 { return c.KeywordScore },
	} {
		channel := append([]searchCandidate(nil), ordered...)
		// Stable sorting preserves the preliminary score and deterministic ID
		// tie breaker within each channel.
		sort.SliceStable(channel, func(i, j int) bool { return signal(channel[i]) > signal(channel[j]) })
		selected := 0
		for _, c := range channel {
			if signal(c) <= 0 || selected >= quota {
				break
			}
			// A shared high-ranking candidate counts toward both reservations.
			if seen[c.Document.NodeID] || appendCandidate(c) {
				selected++
			}
		}
	}
	for _, c := range ordered {
		appendCandidate(c)
	}
	return out
}
