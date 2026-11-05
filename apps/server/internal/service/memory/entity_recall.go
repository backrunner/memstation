package memory

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

func (e *Engine) SetEntityRecall(extractor ports.QueryEntityExtractor, timeout time.Duration) {
	e.configMu.Lock()
	defer e.configMu.Unlock()
	e.entityExtractor = extractor
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 200 * time.Millisecond
	}
	e.entityTimeout = timeout
}

type entityRecall struct {
	scores      map[string]float64
	sources     map[string]float64
	names       []string
	matched     map[string][]string
	diagnostics *domain.EntityRecallDiagnostics
}

// Providers and ledger finalizers can outlive a cancelled HTTP call. Bound the
// entity search channel independently; its late result cannot alter this search.
// includeEntities is controlled only by evaluation ablations; runtime Search uses full recall.
func (e *Engine) recallEntitiesWithinBudget(ctx context.Context, input domain.SearchInput, includeEntities bool) (entityRecall, error) {
	e.configMu.RLock()
	extractor, timeout := e.entityExtractor, e.entityTimeout
	e.configMu.RUnlock()
	if extractor == nil || !includeEntities {
		return e.recallEntitySeeds(ctx, input, includeEntities)
	}
	started := time.Now()
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type response struct {
		result entityRecall
		err    error
	}
	done := make(chan response, 1)
	go func() {
		result, err := e.recallEntitySeeds(child, input, includeEntities)
		done <- response{result, err}
	}()
	select {
	case result := <-done:
		if child.Err() == nil {
			return result.result, result.err
		}
	case <-child.Done():
	}
	if ctx.Err() != nil {
		return entityRecall{}, ctx.Err()
	}
	return entityRecall{scores: map[string]float64{}, matched: map[string][]string{}, diagnostics: &domain.EntityRecallDiagnostics{Status: "timed_out", LatencyMS: time.Since(started).Milliseconds()}}, nil
}

func (e *Engine) recallEntitySeeds(ctx context.Context, input domain.SearchInput, includeEntities bool) (out entityRecall, resultErr error) {
	out = entityRecall{scores: map[string]float64{}, sources: map[string]float64{}, matched: map[string][]string{}, diagnostics: &domain.EntityRecallDiagnostics{Status: "unavailable"}}
	e.configMu.RLock()
	extractor, timeout := e.entityExtractor, e.entityTimeout
	e.configMu.RUnlock()
	if !includeEntities {
		out.diagnostics.Status = "ablation"
		return out, nil
	}
	if extractor == nil {
		return out, nil
	}
	started := time.Now()
	defer func() { out.diagnostics.LatencyMS = time.Since(started).Milliseconds() }()
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	query, err := extractor.ExtractQueryEntities(child, input.UserID, input.Query)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if errors.Is(err, domain.ErrAccessDenied) {
			return out, err
		}
		out.diagnostics.Status = "unavailable"
		if errors.Is(err, domain.ErrEntityQueryBudget) {
			out.diagnostics.Status = "budget_exceeded"
		}
		if child.Err() != nil {
			out.diagnostics.Status = "timed_out"
		}
		return out, nil
	}
	out.names = query.Names
	out.diagnostics.QueryEntities = len(query.Names)
	out.diagnostics.CacheHit = query.CacheHit
	if len(query.Names) == 0 {
		out.diagnostics.Status = "no_entities"
		return out, nil
	}
	matches, err := e.repo.MatchEntityNames(child, input.UserID, input.GenerationID, input.EntityScope, query.Names)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			out.diagnostics.Status = "timed_out"
			return out, nil
		}
		return out, err
	}
	for _, match := range matches {
		// Popular names get less weight. Multiple identical mentions never stack.
		score := .65 / math.Sqrt(1+math.Log1p(float64(max(0, match.Occurrences-1))))
		out.scores[match.NodeID] = math.Max(out.scores[match.NodeID], score)
		out.matched[match.NodeID] = append(out.matched[match.NodeID], match.Name)
	}
	// A literal mentions edge is the entity index's posting list. Fetch only
	// this relation type for one hop; no semantic or identity edges are followed.
	ids := make([]string, 0, len(out.scores))
	for id := range out.scores {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > 0 {
		edges, err := e.repo.RecallRelations(child, domain.RelationQuery{WorkspaceID: input.WorkspaceID, UserID: input.UserID, GenerationID: input.GenerationID, NodeIDs: ids, Mode: "entity_mentions", MaxHops: 1, MaxNodes: 256, MaxEdges: 256, MaxEdgesPerNode: 16, Timeout: timeout})
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				out.scores = map[string]float64{}
				out.matched = map[string][]string{}
				out.diagnostics.Status = "timed_out"
				return out, nil
			}
			return out, err
		}
		roots := map[string]float64{}
		for id, score := range out.scores {
			roots[id] = score
		}
		degrees := map[string]int{}
		for _, edge := range edges {
			if roots[edge.SrcNodeID] > 0 {
				degrees[edge.SrcNodeID]++
			}
			if roots[edge.DstNodeID] > 0 {
				degrees[edge.DstNodeID]++
			}
		}
		for _, edge := range edges {
			if edge.RelationType != domain.MemoryRelationMentionsEntity {
				continue
			}
			for _, id := range []string{edge.SrcNodeID, edge.DstNodeID} {
				if roots[id] <= 0 {
					continue
				}
				dst := domain.RelationDestination(edge, id, "entity_mentions")
				relevance := roots[id] * math.Min(1, edge.Weight) * domain.RelationConfidence(edge) / math.Sqrt(math.Max(1, float64(degrees[id])/4))
				if relevance > out.scores[dst] {
					out.scores[dst] = relevance
					out.matched[dst] = append([]string{}, out.matched[id]...)
				}
				if roots[dst] == 0 {
					out.sources[dst] = math.Max(out.sources[dst], relevance)
				}
			}
		}
	}
	out.diagnostics.Status = "complete"
	out.diagnostics.MatchedNodes = len(out.scores)
	return out, nil
}
