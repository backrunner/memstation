package memory

import (
	"context"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/observability"
	"memstation/apps/server/internal/ports"
)

type Engine struct {
	repo    ports.MemoryRepository
	vectors ports.VectorStore
}

type DebugSearchResult struct {
	Result     domain.SearchResult        `json:"result"`
	Variant    SearchVariant              `json:"variant"`
	Components map[string]ScoreComponents `json:"components"`
	BaseScores map[string]float64         `json:"base_scores"`
}

func NewEngine(repo ports.MemoryRepository, vectors ports.VectorStore) *Engine {
	return &Engine{repo: repo, vectors: vectors}
}

func (e *Engine) Inject(ctx context.Context, req domain.UnifiedRequest) (domain.UnifiedRequest, domain.InjectionResult, error) {
	ctx, span := observability.TraceSpan(ctx, "memory.inject",
		attribute.String("memory.user_id", req.UserID),
		attribute.String("memory.mode", string(req.Mode)),
	)
	defer span.End()

	if req.Mode != domain.ModeInject {
		return req, domain.InjectionResult{}, nil
	}

	query := lastUserMessage(req.Messages)
	generalResult, err := e.Search(ctx, domain.SearchInput{
		UserID:              req.UserID,
		Query:               query,
		TopN:                8,
		IncludeConversation: false,
		IncludeSegments:     true,
		MinScore:            0.0,
	})
	if err != nil {
		return req, domain.InjectionResult{}, err
	}
	keyMemoryResult, err := e.Search(ctx, domain.SearchInput{
		UserID:              req.UserID,
		Query:               query,
		TopN:                3,
		IncludeConversation: false,
		IncludeSegments:     true,
		MemoryTypes:         []domain.MemoryType{domain.MemoryTypeKeyMemory},
		MinScore:            0.0,
	})
	if err != nil {
		return req, domain.InjectionResult{}, err
	}
	injectedMemories := packInjectionMemories(mergeInjectionMemories(generalResult.Memories, keyMemoryResult.Memories), req.MaxInjectionTokens)
	result := domain.SearchResult{
		Memories:             injectedMemories,
		Total:                len(injectedMemories),
		QueryEmbeddingTokens: max(generalResult.QueryEmbeddingTokens, keyMemoryResult.QueryEmbeddingTokens),
	}

	if req.InjectContext && len(result.Memories) > 0 {
		summaryLines := make([]string, 0, len(result.Memories))
		for _, memory := range result.Memories {
			prefix := string(memory.Type)
			if memory.Type == domain.MemoryTypeKeyMemory {
				if memory.Metadata == nil {
					memory.Metadata = map[string]any{}
				}
				memory.Metadata["injection_bucket"] = "key_memory_topk"
			}
			summaryLines = append(summaryLines, "- ["+prefix+"] "+memory.Content)
		}
		req.Messages = append([]domain.UnifiedMessage{{
			Role: domain.RoleSystem,
			Text: "MemStation injected memory context:\n" + strings.Join(summaryLines, "\n"),
			Blocks: []domain.ContentBlock{{
				Type: domain.ContentBlockText,
				Text: "MemStation injected memory context",
			}},
		}}, req.Messages...)
	}

	if req.InjectTools {
		req.Tools = append(req.Tools,
			domain.ToolDefinition{
				Name:        "recall_memory",
				Description: "Read relevant memories for the active user.",
				ReadOnly:    true,
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Semantic query used to search memory.",
						},
						"top_n": map[string]any{
							"type":        "integer",
							"description": "Maximum number of memory items to return.",
							"minimum":     1,
							"maximum":     10,
						},
						"types": map[string]any{
							"type":        "array",
							"description": "Optional memory type filters.",
							"items": map[string]any{
								"type": "string",
								"enum": []string{"summary", "segment", "impression", "fact", "key_memory"},
							},
						},
						"date_from": map[string]any{
							"type":        "string",
							"description": "Optional RFC3339 start time filter against memory activity_at.",
						},
						"date_to": map[string]any{
							"type":        "string",
							"description": "Optional RFC3339 end time filter against memory activity_at.",
						},
						"time_tiers": map[string]any{
							"type":        "array",
							"description": "Optional time tier filters.",
							"items": map[string]any{
								"type": "string",
								"enum": []string{"active", "short_term", "long_term", "archived"},
							},
						},
					},
					"required": []string{"query"},
				},
			},
			domain.ToolDefinition{
				Name:        "get_key_memory",
				Description: "Return stable user facts from key memory.",
				ReadOnly:    true,
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"key": map[string]any{
							"type":        "string",
							"description": "Optional single key to fetch.",
						},
						"limit": map[string]any{
							"type":        "integer",
							"description": "Maximum number of key memories to return when key is omitted.",
							"minimum":     1,
							"maximum":     20,
						},
						"query": map[string]any{
							"type":        "string",
							"description": "Optional semantic query used to rank key memories.",
						},
						"date_from": map[string]any{
							"type":        "string",
							"description": "Optional RFC3339 start time filter against key memory activity_at.",
						},
						"date_to": map[string]any{
							"type":        "string",
							"description": "Optional RFC3339 end time filter against key memory activity_at.",
						},
						"time_tiers": map[string]any{
							"type":        "array",
							"description": "Optional time tier filters.",
							"items": map[string]any{
								"type": "string",
								"enum": []string{"active", "short_term", "long_term", "archived"},
							},
						},
					},
				},
			},
		)
		if req.ToolWriteEnabled {
			req.Tools = append(req.Tools, domain.ToolDefinition{
				Name:        "write_key_memory",
				Description: "Write a whitelisted key memory value for the active user.",
				ReadOnly:    false,
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"key": map[string]any{
							"type":        "string",
							"description": "Whitelisted key memory key.",
						},
						"value": map[string]any{
							"type":        "string",
							"description": "Value to persist for the key.",
						},
						"category": map[string]any{
							"type":        "string",
							"description": "Optional category label.",
						},
					},
					"required": []string{"key", "value"},
				},
			})
		}
	}

	return req, domain.InjectionResult{Query: query, Search: result}, nil
}

func (e *Engine) Search(ctx context.Context, input domain.SearchInput) (domain.SearchResult, error) {
	debug, err := e.search(ctx, input, SearchVariantFull)
	if err != nil {
		return domain.SearchResult{}, err
	}
	return debug.Result, nil
}

func (e *Engine) SearchWithVariant(ctx context.Context, input domain.SearchInput, variant SearchVariant) (DebugSearchResult, error) {
	return e.search(ctx, input, variant)
}

func (e *Engine) search(ctx context.Context, input domain.SearchInput, variant SearchVariant) (DebugSearchResult, error) {
	ctx, span := observability.TraceSpan(ctx, "memory.search",
		attribute.String("memory.user_id", input.UserID),
		attribute.Int("memory.query_length", len(strings.TrimSpace(input.Query))),
		attribute.Int("memory.top_n", input.TopN),
		attribute.String("memory.variant", string(variant)),
	)
	defer span.End()

	cfg := configForVariant(variant)
	if input.TopN <= 0 {
		input.TopN = 10
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return e.searchWithoutVectors(ctx, input, variant)
	}

	directScores := map[string]float64{}
	keywordMatches := map[string]domain.KeywordMatch{}
	keywordResults, err := e.repo.SearchKeywordMatches(ctx, input.UserID, query, max(input.TopN*4, 10))
	if err != nil {
		return DebugSearchResult{}, err
	}
	for _, item := range keywordResults {
		if item.Score > directScores[item.NodeID] {
			directScores[item.NodeID] = item.Score
		}
		keywordMatches[item.NodeID] = item
	}

	if e.vectors != nil {
		vectorResults, vectorErr := e.vectors.Search(ctx, domain.VectorSearchInput{
			UserID:           input.UserID,
			Query:            query,
			TopK:             max(input.TopN*4, 10),
			AllowedTypes:     vectorAllowedTypes(input),
			EmbeddingVersion: 1,
		})
		if vectorErr == nil {
			for _, item := range vectorResults {
				if item.Score > directScores[item.NodeID] {
					directScores[item.NodeID] = item.Score
				}
			}
		}
	}
	if len(directScores) == 0 {
		return e.searchWithoutVectors(ctx, input, variant)
	}

	baseNodeIDs := make([]string, 0, len(directScores))
	for nodeID := range directScores {
		baseNodeIDs = append(baseNodeIDs, nodeID)
	}
	scoreMap := cloneScoreMap(directScores)
	relatedMap := map[string][]domain.RelatedMemory{}
	if cfg.useRelations {
		relations, relErr := e.repo.ListRelations(ctx, input.UserID, baseNodeIDs, 2)
		if relErr != nil {
			return DebugSearchResult{}, relErr
		}
		scoreMap, relatedMap = expandRelationScores(directScores, relations, 2)
	}

	candidateNodeIDs := make([]string, 0, len(scoreMap))
	for nodeID := range scoreMap {
		candidateNodeIDs = append(candidateNodeIDs, nodeID)
	}
	docs, err := e.repo.ListDocumentsByNodeIDs(ctx, input.UserID, candidateNodeIDs)
	if err != nil {
		return DebugSearchResult{}, err
	}

	memories := make([]domain.MemoryDocument, 0, len(docs))
	componentsByID := map[string]ScoreComponents{}
	for _, doc := range docs {
		if !matchesTemporalSearchInput(doc, input) {
			continue
		}
		semanticScore := scoreMap[doc.NodeID]
		if semanticScore == 0 {
			continue
		}
		baseScore := directScores[doc.NodeID]
		relationExpansion := maxFloat(semanticScore-baseScore, 0)
		components := scoreDocument(doc, baseScore, relationExpansion, cfg)
		if match, ok := keywordMatches[doc.NodeID]; ok {
			components.KeywordScore = match.Score
			doc.Metadata = mergeSearchMetadata(doc.Metadata, map[string]any{
				"matched_keywords": match.Keywords,
			})
		}
		doc.Score = components.SemanticScore
		doc.EffectiveScore = components.EffectiveScore
		if cfg.useRelations {
			doc.Related = relatedMap[doc.NodeID]
		}
		if !matchesSearchDocument(doc, input) {
			continue
		}
		if doc.EffectiveScore >= input.MinScore {
			memories = append(memories, doc)
			componentsByID[doc.ID] = components
		}
	}

	sort.Slice(memories, func(i, j int) bool {
		if memories[i].EffectiveScore == memories[j].EffectiveScore {
			return memories[i].UpdatedAt.After(memories[j].UpdatedAt)
		}
		return memories[i].EffectiveScore > memories[j].EffectiveScore
	})
	if len(memories) > input.TopN {
		memories = memories[:input.TopN]
	}
	return DebugSearchResult{
		Result: domain.SearchResult{
			Memories:             memories,
			Total:                len(memories),
			QueryEmbeddingTokens: max(len(strings.Fields(input.Query))*3, len(strings.TrimSpace(input.Query))/4),
		},
		Variant:    variant,
		Components: componentsByID,
		BaseScores: directScores,
	}, nil
}

func (e *Engine) searchWithoutVectors(ctx context.Context, input domain.SearchInput, variant SearchVariant) (DebugSearchResult, error) {
	repoInput := input
	repoInput.MemoryTypes = repoSearchTypes(input)
	result, err := e.repo.Search(ctx, repoInput)
	if err != nil {
		return DebugSearchResult{}, err
	}
	componentsByID := make(map[string]ScoreComponents, len(result.Memories))
	cfg := configForVariant(variant)
	for idx, doc := range result.Memories {
		if !matchesTemporalSearchInput(doc, input) || !matchesSearchDocument(doc, input) {
			continue
		}
		components := scoreDocument(doc, doc.Score, 0, cfg)
		doc.Score = components.SemanticScore
		doc.EffectiveScore = components.EffectiveScore
		result.Memories[idx] = doc
		componentsByID[doc.ID] = components
	}
	filtered := make([]domain.MemoryDocument, 0, len(result.Memories))
	for _, doc := range result.Memories {
		if doc.ID == "" || !matchesTemporalSearchInput(doc, input) || !matchesSearchDocument(doc, input) {
			continue
		}
		filtered = append(filtered, doc)
	}
	result.Memories = filtered
	result.Total = len(filtered)
	return DebugSearchResult{
		Result:     result,
		Variant:    variant,
		Components: componentsByID,
		BaseScores: map[string]float64{},
	}, nil
}

func lastUserMessage(messages []domain.UnifiedMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == domain.RoleUser {
			if messages[i].Text != "" {
				return messages[i].Text
			}
			if len(messages[i].Blocks) > 0 {
				return messages[i].Blocks[0].Text
			}
		}
	}
	return ""
}

func mergeSearchMetadata(current map[string]any, extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return current
	}
	merged := make(map[string]any, len(current)+len(extra))
	for key, value := range current {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

func matchesRequestedTypes(value domain.MemoryType, filters []domain.MemoryType) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		if filter == value {
			return true
		}
	}
	return false
}

func matchesSearchDocument(doc domain.MemoryDocument, input domain.SearchInput) bool {
	if !matchesRequestedTypes(doc.Type, input.MemoryTypes) {
		return false
	}
	if doc.Type == domain.MemoryTypeSegment && !searchAllowsSegments(input) {
		return false
	}
	return true
}

func searchAllowsSegments(input domain.SearchInput) bool {
	if input.IncludeSegments {
		return true
	}
	for _, memoryType := range input.MemoryTypes {
		if memoryType == domain.MemoryTypeSegment {
			return true
		}
	}
	return false
}

func repoSearchTypes(input domain.SearchInput) []domain.MemoryType {
	if len(input.MemoryTypes) == 0 {
		if input.IncludeSegments {
			return []domain.MemoryType{
				domain.MemoryTypeSummary,
				domain.MemoryTypeSegment,
				domain.MemoryTypeImpression,
				domain.MemoryTypeFact,
				domain.MemoryTypeKeyMemory,
			}
		}
		return nil
	}
	return vectorAllowedTypes(input)
}

func vectorAllowedTypes(input domain.SearchInput) []domain.MemoryType {
	if len(input.MemoryTypes) == 0 {
		return nil
	}
	allowed := append([]domain.MemoryType{}, input.MemoryTypes...)
	if containsMemoryType(allowed, domain.MemoryTypeSegment) {
		return allowed
	}
	for _, memoryType := range input.MemoryTypes {
		if memoryType != domain.MemoryTypeKeyMemory {
			allowed = append(allowed, domain.MemoryTypeSegment)
			break
		}
	}
	return allowed
}

func containsMemoryType(items []domain.MemoryType, target domain.MemoryType) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func cloneScoreMap(items map[string]float64) map[string]float64 {
	cloned := make(map[string]float64, len(items))
	for key, value := range items {
		cloned[key] = value
	}
	return cloned
}

func expandRelationScores(base map[string]float64, relations []domain.MemoryRelation, depth int) (map[string]float64, map[string][]domain.RelatedMemory) {
	const relationBoostFactor = 0.01

	type edge struct {
		nodeID       string
		relationType string
		weight       float64
	}

	scoreMap := cloneScoreMap(base)
	relatedMap := map[string][]domain.RelatedMemory{}
	graph := map[string][]edge{}

	for _, relation := range relations {
		if isTemporalRelationType(relation.RelationType) {
			continue
		}
		graph[relation.SrcNodeID] = append(graph[relation.SrcNodeID], edge{
			nodeID:       relation.DstNodeID,
			relationType: relation.RelationType,
			weight:       relation.Weight,
		})
		graph[relation.DstNodeID] = append(graph[relation.DstNodeID], edge{
			nodeID:       relation.SrcNodeID,
			relationType: relation.RelationType,
			weight:       relation.Weight,
		})
		relatedMap[relation.SrcNodeID] = append(relatedMap[relation.SrcNodeID], domain.RelatedMemory{
			ID:     relation.DstNodeID,
			Type:   relation.RelationType,
			Weight: relation.Weight,
		})
		relatedMap[relation.DstNodeID] = append(relatedMap[relation.DstNodeID], domain.RelatedMemory{
			ID:     relation.SrcNodeID,
			Type:   relation.RelationType,
			Weight: relation.Weight,
		})
	}

	if depth <= 0 {
		depth = 1
	}
	frontier := cloneScoreMap(base)
	for hop := 0; hop < depth && len(frontier) > 0; hop++ {
		next := map[string]float64{}
		for nodeID, score := range frontier {
			for _, edge := range graph[nodeID] {
				if _, hasDirectScore := base[edge.nodeID]; hasDirectScore {
					continue
				}
				candidateScore := clamp(score+relationBoostFactor*clamp(edge.weight, 0.2, 1.0), 0, 1)
				if candidateScore <= scoreMap[edge.nodeID] {
					continue
				}
				scoreMap[edge.nodeID] = candidateScore
				if candidateScore > next[edge.nodeID] {
					next[edge.nodeID] = candidateScore
				}
			}
		}
		frontier = next
	}

	return scoreMap, relatedMap
}

func clamp(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

func mergeInjectionMemories(primary, keyMemories []domain.MemoryDocument) []domain.MemoryDocument {
	merged := make([]domain.MemoryDocument, 0, len(primary)+len(keyMemories))
	seen := map[string]int{}
	appendItem := func(item domain.MemoryDocument, bucket string) {
		if item.Metadata == nil {
			item.Metadata = map[string]any{}
		}
		item.Metadata["injection_bucket"] = bucket
		if index, ok := seen[item.NodeID]; ok {
			if item.EffectiveScore > merged[index].EffectiveScore {
				merged[index] = item
			}
			return
		}
		seen[item.NodeID] = len(merged)
		merged = append(merged, item)
	}
	for _, item := range primary {
		appendItem(item, "semantic_recall")
	}
	for _, item := range keyMemories {
		appendItem(item, "key_memory_topk")
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].EffectiveScore == merged[j].EffectiveScore {
			return merged[i].ActivityAt.After(merged[j].ActivityAt)
		}
		return merged[i].EffectiveScore > merged[j].EffectiveScore
	})
	return merged
}

func packInjectionMemories(memories []domain.MemoryDocument, maxTokens int) []domain.MemoryDocument {
	if len(memories) == 0 {
		return nil
	}
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	remaining := maxTokens
	selected := make([]domain.MemoryDocument, 0, len(memories))
	for _, item := range memories {
		tokens := estimateMemoryTokens(item)
		if len(selected) > 0 && tokens > remaining {
			continue
		}
		selected = append(selected, item)
		remaining -= tokens
		if remaining <= 0 {
			break
		}
	}
	if len(selected) == 0 {
		return memories[:1]
	}
	return selected
}

func estimateMemoryTokens(item domain.MemoryDocument) int {
	text := strings.TrimSpace(item.Content)
	if text == "" {
		text = item.ID
	}
	return max(len(strings.Fields(text))*2, len(text)/4)
}

func matchesTemporalSearchInput(doc domain.MemoryDocument, input domain.SearchInput) bool {
	return matchesTemporalFilter(doc.ActivityAt, doc.TimeTier, input.DateFrom, input.DateTo, input.TimeTiers)
}

func isTemporalRelationType(relationType string) bool {
	switch relationType {
	case "indexed_in_tier", "occurred_on_day", "day_in_month", "month_in_year":
		return true
	default:
		return false
	}
}
