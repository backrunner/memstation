package memory

import (
	"context"
	"math"
	"sort"
	"strings"

	"memstation/apps/server/internal/domain"
)

func expandRelationPaths(base map[string]float64, relations []domain.MemoryRelation, cfg RelationRecallConfig) (map[string]float64, map[string][]domain.RelatedMemory, map[string]domain.RecallPath) {
	cfg = cfg.normalized()
	budget := (domain.RelationQuery{MaxNodes: cfg.MaxNodes, MaxEdges: cfg.MaxEdges, MaxEdgesPerNode: cfg.MaxEdgesPerNode}).Normalized()
	scores := cloneScoreMap(base)
	related := map[string][]domain.RelatedMemory{}
	paths := map[string]domain.RecallPath{}
	graph := map[string][]domain.MemoryRelation{}
	maxSeed := 0.0
	for id, v := range base {
		maxSeed = math.Max(maxSeed, v)
		paths[id] = domain.RecallPath{SeedNodeID: id, NodeIDs: []string{id}, Contribution: v}
	}
	for _, r := range relations {
		if isTemporalRelationType(r.RelationType) || r.Weight <= 0 || domain.RelationConfidence(r) <= 0 {
			continue
		}
		if r.RelationType == domain.MemoryRelationSupersedes || r.RelationType == domain.MemoryRelationReplaces || (r.RelationType == domain.MemoryRelationContradicts && cfg.Mode != "conflicts") {
			continue
		}
		for _, from := range []string{r.SrcNodeID, r.DstNodeID} {
			to := domain.RelationDestination(r, from, cfg.Mode)
			if to == "" {
				continue
			}
			graph[from] = append(graph[from], r)
			related[from] = append(related[from], domain.RelatedMemory{NodeID: to, RelationType: r.RelationType, Weight: r.Weight})
		}
	}
	frontier := cloneScoreMap(base)
	for hop := 0; hop < cfg.MaxHops && len(frontier) > 0; hop++ {
		next := map[string]float64{}
		ids := make([]string, 0, len(frontier))
		for id := range frontier {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			if frontier[ids[i]] != frontier[ids[j]] {
				return frontier[ids[i]] > frontier[ids[j]]
			}
			return ids[i] < ids[j]
		})
		for _, id := range ids {
			edges := graph[id]
			sort.Slice(edges, func(i, j int) bool {
				a, b := edges[i], edges[j]
				sa, sb := a.Weight*domain.RelationConfidence(a), b.Weight*domain.RelationConfidence(b)
				if sa != sb {
					return sa > sb
				}
				return a.ID+a.SrcNodeID+a.DstNodeID < b.ID+b.SrcNodeID+b.DstNodeID
			})
			hubPenalty := 1.0 / math.Sqrt(math.Max(1, float64(len(edges))/8))
			for _, r := range edges[:min(len(edges), budget.MaxEdgesPerNode)] {
				dst := domain.RelationDestination(r, id, cfg.Mode)
				if containsPathNode(paths[id].NodeIDs, dst) {
					continue
				}
				factor := relationBoostFactor(r.RelationType)
				if cfg.Mode == "conflicts" && r.RelationType == domain.MemoryRelationContradicts {
					factor = 0.35
				}
				score := frontier[id] * factor * r.Weight * domain.RelationConfidence(r) * cfg.hopPenalty(hop) * hubPenalty
				if _, direct := base[dst]; !direct {
					score = min(score, maxSeed*0.95)
				}
				if score <= scores[dst] {
					continue
				}
				if _, exists := scores[dst]; !exists && len(scores) >= budget.MaxNodes {
					continue
				}
				scores[dst] = score
				p := paths[id]
				p.NodeIDs = append(append([]string{}, p.NodeIDs...), dst)
				p.RelationIDs = append(append([]string{}, p.RelationIDs...), r.ID)
				p.RelationTypes = append(append([]string{}, p.RelationTypes...), r.RelationType)
				p.Contribution = score
				paths[dst] = p
				if _, direct := base[dst]; !direct {
					next[dst] = score
				}
			}
		}
		frontier = next
	}
	for id := range base {
		delete(paths, id)
	}
	return scores, related, paths
}

func containsPathNode(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func (e *Engine) addEntitySeeds(ctx context.Context, input domain.SearchInput, seeds map[string]float64) error {
	aliases, err := e.repo.ListEntityAliases(ctx, input.UserID, input.GenerationID)
	if err != nil {
		return err
	}
	scope := domain.NormalizeEntityAlias(input.EntityScope)
	q := domain.NormalizeEntityAlias(input.Query)
	// Only explicit, confirmed aliases participate. Candidate mappings are visible
	// to administrators but cannot contaminate retrieval.
	for _, a := range aliases {
		if a.Status != "confirmed" || a.Scope != scope {
			continue
		}
		if q == a.Alias {
			seeds[a.TargetNodeID] = math.Max(seeds[a.TargetNodeID], 0.9)
			if a.SourceNodeID != "" {
				seeds[a.SourceNodeID] = math.Max(seeds[a.SourceNodeID], 0.9)
			}
		}
	}
	// Preserve both source graphs. A merge is a reversible equivalence, not a
	// destructive rewrite; cap expansion even for a long chain of aliases.
	for hop := 0; hop < 4; hop++ {
		changed := false
		for _, a := range aliases {
			if a.Status != "confirmed" || a.Scope != scope || a.SourceNodeID == "" {
				continue
			}
			v := math.Max(seeds[a.SourceNodeID], seeds[a.TargetNodeID])
			if v <= 0 {
				continue
			}
			for _, id := range []string{a.SourceNodeID, a.TargetNodeID} {
				if seeds[id] < v && len(seeds) < 128 {
					seeds[id] = v
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return nil
}

func (s *Service) ListEntityAliases(ctx context.Context, u, g string) ([]domain.EntityAlias, error) {
	resolved, err := s.resolveGenerationID(ctx, u, g)
	if err != nil {
		return nil, err
	}
	return s.repo.ListEntityAliases(ctx, u, resolved)
}

func (s *Service) PutEntityAlias(ctx context.Context, a domain.EntityAlias) (domain.EntityAlias, error) {
	g, err := s.resolveGenerationID(ctx, a.UserID, a.GenerationID)
	if err != nil {
		return a, err
	}
	a.GenerationID = g
	return s.repo.PutEntityAlias(ctx, a)
}

// Historical records pass through the same scoring and reranking functions as
// live search. Mutable live facts and their lineage heads are never hydrated
// over an immutable snapshot.
func (e *Engine) searchHistoricalFacts(ctx context.Context, input domain.SearchInput, variant SearchVariant, embeddingVersion int, embeddingModel string) (DebugSearchResult, error) {
	docs, err := e.repo.SearchFactsAt(ctx, input)
	if err != nil {
		return DebugSearchResult{}, err
	}
	cfg := configForVariant(variant)
	now := e.now()
	if input.AsOf != nil {
		now = *input.AsOf
	}
	scores := map[string]float64{}
	if e.vectors != nil && input.KnownAt == nil && strings.TrimSpace(input.Query) != "" {
		vs, vectorErr := e.vectors.Search(ctx, domain.VectorSearchInput{UserID: input.UserID, GenerationID: input.GenerationID, Query: input.Query, TopK: 100, EmbeddingVersion: embeddingVersion, EmbeddingModel: embeddingModel, AllowedTypes: []domain.MemoryType{domain.MemoryTypeFact}})
		if vectorErr == nil {
			for _, v := range vs {
				scores[v.NodeID] = v.Score
			}
		}
	}
	candidates := []searchCandidate{}
	for _, d := range docs {
		if !matchesSearchDocument(d, input) || !matchesTemporalSearchInput(d, input) {
			continue
		}
		// Historical validity was checked in the store. Today's superseded flag is
		// retained for display but must not penalize a then-valid fact.
		scored := d
		scored.Metadata = mergeSearchMetadata(d.Metadata, map[string]any{"is_superseded": false, "superseded_by_id": "", "valid_to": nil})
		comp := scoreDocumentAt(scored, scores[d.NodeID], d.Score, 0, 0, 0, 0, 0, now, cfg)
		candidates = append(candidates, searchCandidate{Document: d, PreliminaryScore: comp.EffectiveScore, BaseSemanticScore: scores[d.NodeID], KeywordScore: d.Score})
	}
	rs, rr := e.rerankCandidates(ctx, input, candidates)
	out := []domain.MemoryDocument{}
	components := map[string]ScoreComponents{}
	for _, c := range candidates {
		d := c.Document
		scored := d
		scored.Metadata = mergeSearchMetadata(d.Metadata, map[string]any{"is_superseded": false, "superseded_by_id": "", "valid_to": nil})
		weight := 0.0
		if _, ok := rr[d.NodeID]; ok {
			weight = e.RerankConfig().BlendWeight
		}
		comp := scoreDocumentAt(scored, c.BaseSemanticScore, c.KeywordScore, 0, 0, 0, rs[d.NodeID], weight, now, cfg)
		d.Score = comp.SemanticScore
		d.EffectiveScore = comp.EffectiveScore
		d.Metadata = mergeSearchMetadata(d.Metadata, map[string]any{"historical_snapshot": true, "as_of": input.AsOf, "known_at": input.KnownAt})
		if d.EffectiveScore >= input.MinScore {
			out = append(out, d)
			components[d.ID] = comp
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EffectiveScore != out[j].EffectiveScore {
			return out[i].EffectiveScore > out[j].EffectiveScore
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > input.TopN {
		out = out[:input.TopN]
	}
	return DebugSearchResult{Result: domain.SearchResult{Memories: out, Total: len(out)}, Variant: variant, Components: components, BaseScores: scores}, nil
}
