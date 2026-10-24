package memory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/observability"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
)

type Engine struct {
	repo        ports.MemoryRepository
	vectors     ports.VectorStore
	reranker    ports.Reranker
	generations *memorygenservice.Service

	configMu       sync.RWMutex
	relationRecall RelationRecallConfig
	rerank         RerankConfig
	clock          func() time.Time
}

type RelationRecallConfig struct {
	MaxEdgesPerNode  int
	MaxEdges         int
	MaxNodes         int
	Timeout          time.Duration
	Mode             string
	MaxHops          int
	HopDecayLambda   float64
	HopDecayExponent float64
}

type RerankConfig struct {
	Model               string
	CandidateMultiplier int
	MinCandidates       int
	MaxCandidates       int
	BlendWeight         float64
}

type DebugSearchResult struct {
	Result     domain.SearchResult        `json:"result"`
	Variant    SearchVariant              `json:"variant"`
	Components map[string]ScoreComponents `json:"components"`
	BaseScores map[string]float64         `json:"base_scores"`
}

type searchCandidate struct {
	Document          domain.MemoryDocument
	PreliminaryScore  float64
	BaseSemanticScore float64
	KeywordScore      float64
	RelationExpansion float64
	SupersessionBoost float64
	ConversationBoost float64
}

func NewEngine(repo ports.MemoryRepository, vectors ports.VectorStore) *Engine {
	return &Engine{
		repo:           repo,
		vectors:        vectors,
		relationRecall: DefaultRelationRecallConfig(),
		rerank:         DefaultRerankConfig(),
		clock:          utcNow,
	}
}

func utcNow() time.Time {
	return time.Now().UTC()
}

func (e *Engine) SetClock(clock func() time.Time) {
	e.configMu.Lock()
	defer e.configMu.Unlock()
	if clock == nil {
		e.clock = utcNow
		return
	}
	e.clock = clock
}

func (e *Engine) SetGenerationService(service *memorygenservice.Service) {
	e.generations = service
}

func (e *Engine) SetReranker(reranker ports.Reranker) {
	e.reranker = reranker
}

func DefaultRelationRecallConfig() RelationRecallConfig {
	return RelationRecallConfig{
		MaxHops:          2,
		HopDecayLambda:   0.35,
		HopDecayExponent: 1.25,
	}
}

func DefaultRerankConfig() RerankConfig {
	return RerankConfig{
		CandidateMultiplier: 6,
		MinCandidates:       20,
		MaxCandidates:       50,
		BlendWeight:         0.35,
	}
}

func (e *Engine) SetRelationRecallConfig(cfg RelationRecallConfig) {
	e.configMu.Lock()
	e.relationRecall = cfg.normalized()
	e.configMu.Unlock()
}

func (e *Engine) RelationRecallConfig() RelationRecallConfig {
	e.configMu.RLock()
	defer e.configMu.RUnlock()
	return e.relationRecall.normalized()
}

func (e *Engine) SetRerankConfig(cfg RerankConfig) {
	e.configMu.Lock()
	e.rerank = cfg.normalized()
	e.configMu.Unlock()
}

func (e *Engine) RerankConfig() RerankConfig {
	e.configMu.RLock()
	defer e.configMu.RUnlock()
	return e.rerank.normalized()
}

func (e *Engine) now() time.Time {
	e.configMu.RLock()
	clock := e.clock
	e.configMu.RUnlock()
	if clock == nil {
		return utcNow()
	}
	now := clock()
	if now.IsZero() {
		return utcNow()
	}
	return now.UTC()
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
		WorkspaceID:         req.WorkspaceID,
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
		WorkspaceID:         req.WorkspaceID,
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
		systemPrompt := buildInjectionSystemPrompt(result.Memories)
		req.Messages = append([]domain.UnifiedMessage{{
			Role: domain.RoleSystem,
			Text: systemPrompt,
			Blocks: []domain.ContentBlock{{
				Type: domain.ContentBlockText,
				Text: systemPrompt,
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
						"as_of":         map[string]any{"type": "string", "format": "date-time", "description": "Return facts valid at this instant (RFC3339)."},
						"known_at":      map[string]any{"type": "string", "format": "date-time", "description": "Return only fact versions recorded by this instant (RFC3339)."},
						"relation_mode": map[string]any{"type": "string", "enum": []string{"neighborhood", "evidence", "dependencies", "dependents", "conflicts"}},
						"entity_scope":  map[string]any{"type": "string"},
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
	bound, err := security.WithPersonalDataScope(ctx, input.UserID, input.WorkspaceID)
	if err != nil {
		return DebugSearchResult{}, err
	}
	ctx = bound
	input.WorkspaceID = domain.PersonalWorkspaceID(input.UserID)
	ctx, span := observability.TraceSpan(ctx, "memory.search",
		attribute.String("memory.user_id", input.UserID),
		attribute.Int("memory.query_length", len(strings.TrimSpace(input.Query))),
		attribute.Int("memory.top_n", input.TopN),
		attribute.String("memory.variant", string(variant)),
	)
	defer span.End()

	cfg := configForVariant(variant)
	if err := domain.ValidateRelationMode(input.RelationMode); err != nil {
		return DebugSearchResult{}, err
	}
	input.TopN = min(input.TopN, 100)
	now := e.now()
	if input.TopN <= 0 {
		input.TopN = 10
	}
	embeddingVersion := 1
	embeddingModel := ""
	if e.generations != nil && strings.TrimSpace(input.UserID) != "" {
		generation, err := e.generations.ResolveGeneration(ctx, input.UserID, input.GenerationID)
		if err != nil {
			return DebugSearchResult{}, err
		}
		input.GenerationID = generation.GenerationID
		if generation.EmbeddingVersion > 0 {
			embeddingVersion = generation.EmbeddingVersion
		}
		embeddingModel = generation.EmbeddingModel
	} else if strings.TrimSpace(input.GenerationID) == "" && strings.TrimSpace(input.UserID) != "" {
		input.GenerationID = memorygenservice.CurrentGenerationID(input.UserID)
	}
	if input.AsOf != nil || input.KnownAt != nil {
		return e.searchHistoricalFacts(ctx, input, variant, embeddingVersion, embeddingModel)
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return e.searchWithoutVectors(ctx, input, variant)
	}

	semanticScores := map[string]float64{}
	keywordScores := map[string]float64{}
	keywordMatches := map[string]domain.KeywordMatch{}
	keywordResults, err := e.repo.SearchKeywordMatches(ctx, input.UserID, input.GenerationID, query, max(input.TopN*4, 10))
	if err != nil {
		return DebugSearchResult{}, err
	}
	for _, item := range keywordResults {
		if item.Score > keywordScores[item.NodeID] {
			keywordScores[item.NodeID] = item.Score
			keywordMatches[item.NodeID] = item
		}
	}

	if e.vectors != nil {
		vectorResults, vectorErr := e.vectors.Search(ctx, domain.VectorSearchInput{
			WorkspaceID:      input.WorkspaceID,
			UserID:           input.UserID,
			GenerationID:     input.GenerationID,
			Query:            query,
			TopK:             max(input.TopN*4, 10),
			AllowedTypes:     vectorAllowedTypes(input),
			EmbeddingVersion: embeddingVersion,
			EmbeddingModel:   embeddingModel,
		})
		if errors.Is(vectorErr, domain.ErrAccessDenied) {
			return DebugSearchResult{}, vectorErr
		}
		if vectorErr == nil {
			for _, item := range vectorResults {
				if item.Score > semanticScores[item.NodeID] {
					semanticScores[item.NodeID] = item.Score
				}
			}
		}
	}
	seedScores := make(map[string]float64, len(semanticScores)+len(keywordScores))
	for nodeID, score := range semanticScores {
		seedScores[nodeID] = combineDirectSignal(score, keywordScores[nodeID])
	}
	for nodeID, score := range keywordScores {
		if _, ok := seedScores[nodeID]; ok {
			continue
		}
		seedScores[nodeID] = combineDirectSignal(semanticScores[nodeID], score)
	}
	if err := e.addEntitySeeds(ctx, input, seedScores); err != nil {
		return DebugSearchResult{}, err
	}
	for id, score := range seedScores {
		if score > combineDirectSignal(semanticScores[id], keywordScores[id]) {
			semanticScores[id] = 0
			keywordScores[id] = score
		}
	}
	if len(seedScores) == 0 {
		return e.searchWithoutVectors(ctx, input, variant)
	}

	baseNodeIDs := make([]string, 0, len(seedScores))
	for nodeID := range seedScores {
		baseNodeIDs = append(baseNodeIDs, nodeID)
	}
	sort.Slice(baseNodeIDs, func(i, j int) bool {
		if seedScores[baseNodeIDs[i]] != seedScores[baseNodeIDs[j]] {
			return seedScores[baseNodeIDs[i]] > seedScores[baseNodeIDs[j]]
		}
		return baseNodeIDs[i] < baseNodeIDs[j]
	})

	seedBudget := (domain.RelationQuery{MaxNodes: e.RelationRecallConfig().MaxNodes}).Normalized().MaxNodes
	if len(baseNodeIDs) > seedBudget {
		for _, id := range baseNodeIDs[seedBudget:] {
			delete(seedScores, id)
		}
		baseNodeIDs = baseNodeIDs[:seedBudget]
	}
	scoreMap := cloneScoreMap(seedScores)
	relationScores := cloneScoreMap(seedScores)
	relatedMap := map[string][]domain.RelatedMemory{}
	var relations []domain.MemoryRelation
	paths := map[string]domain.RecallPath{}
	graphDegraded := false
	if cfg.useRelations {
		relationConfig := e.RelationRecallConfig()
		relationConfig.Mode = input.RelationMode
		var relErr error
		relations, relErr = e.repo.RecallRelations(ctx, domain.RelationQuery{UserID: input.UserID, WorkspaceID: input.WorkspaceID, GenerationID: input.GenerationID, NodeIDs: baseNodeIDs, MaxHops: relationConfig.MaxHops, MaxEdgesPerNode: relationConfig.MaxEdgesPerNode, MaxEdges: relationConfig.MaxEdges, MaxNodes: relationConfig.MaxNodes, Timeout: relationConfig.Timeout, Mode: input.RelationMode})
		if relErr != nil {
			if ctx.Err() != nil {
				return DebugSearchResult{}, ctx.Err()
			}
			if !errors.Is(relErr, context.DeadlineExceeded) {
				return DebugSearchResult{}, relErr
			}
			graphDegraded = true
		}
		relationScores, relatedMap, paths = expandRelationPaths(seedScores, relations, relationConfig)
		scoreMap = cloneScoreMap(relationScores)
	}

	candidateNodeIDs := make([]string, 0, len(scoreMap))
	for nodeID := range scoreMap {
		candidateNodeIDs = append(candidateNodeIDs, nodeID)
	}
	docs, err := e.repo.ListDocumentsByNodeIDsInGeneration(ctx, input.UserID, input.GenerationID, candidateNodeIDs)
	if err != nil {
		return DebugSearchResult{}, err
	}
	conversationBoosts := map[string]float64{}
	if input.IncludeConversation {
		scoreMap, conversationBoosts, _, err = e.expandConversationCandidates(ctx, input, scoreMap, docs)
		if err != nil {
			return DebugSearchResult{}, err
		}
	}
	scoreMap, supersessionBoosts, supersessionLineage, err := e.canonicalizeSupersededScores(ctx, input, scoreMap, relations)
	if err != nil {
		return DebugSearchResult{}, err
	}
	candidateNodeIDs = candidateNodeIDs[:0]
	for nodeID := range scoreMap {
		candidateNodeIDs = append(candidateNodeIDs, nodeID)
	}
	docs, err = e.repo.ListDocumentsByNodeIDsInGeneration(ctx, input.UserID, input.GenerationID, candidateNodeIDs)
	if err != nil {
		return DebugSearchResult{}, err
	}

	candidates := make([]searchCandidate, 0, len(docs))
	for _, doc := range docs {
		if !matchesTemporalSearchInput(doc, input) || !matchesSearchDocument(doc, input) {
			continue
		}
		semanticScore := scoreMap[doc.NodeID]
		if semanticScore == 0 {
			continue
		}
		baseScore := seedScores[doc.NodeID]
		relationExpansion := maxFloat(relationScores[doc.NodeID]-baseScore, 0)
		supersessionBoost := supersessionBoosts[doc.NodeID]
		conversationBoost := conversationBoosts[doc.NodeID]
		components := scoreDocumentAt(doc, semanticScores[doc.NodeID], keywordScores[doc.NodeID], relationExpansion, supersessionBoost, conversationBoost, 0, 0, now, cfg)
		candidates = append(candidates, searchCandidate{
			Document:          doc,
			PreliminaryScore:  components.EffectiveScore,
			BaseSemanticScore: semanticScores[doc.NodeID],
			KeywordScore:      keywordScores[doc.NodeID],
			RelationExpansion: relationExpansion,
			SupersessionBoost: supersessionBoost,
			ConversationBoost: conversationBoost,
		})
	}
	rerankScores, rerankRanks := e.rerankCandidates(ctx, input, candidates)

	memories := make([]domain.MemoryDocument, 0, len(candidates))
	componentsByID := map[string]ScoreComponents{}
	rerankConfig := e.RerankConfig()
	for _, candidate := range candidates {
		doc := candidate.Document
		if path, ok := paths[doc.NodeID]; ok {
			doc.Metadata = mergeSearchMetadata(doc.Metadata, map[string]any{"recall_path": path})
		}
		if graphDegraded {
			doc.Metadata = mergeSearchMetadata(doc.Metadata, map[string]any{"graph_timeout": true})
		}
		if input.RelationMode == "conflicts" {
			doc.Metadata = mergeSearchMetadata(doc.Metadata, map[string]any{"conflict_query": true})
		}
		rerankScore := rerankScores[doc.NodeID]
		rerankWeight := 0.0
		if _, ok := rerankRanks[doc.NodeID]; ok {
			rerankWeight = rerankConfig.BlendWeight
		}
		components := scoreDocumentAt(doc, candidate.BaseSemanticScore, candidate.KeywordScore, candidate.RelationExpansion, candidate.SupersessionBoost, candidate.ConversationBoost, rerankScore, rerankWeight, now, cfg)
		if match, ok := keywordMatches[doc.NodeID]; ok {
			doc.Metadata = mergeSearchMetadata(doc.Metadata, map[string]any{
				"matched_keywords": match.Keywords,
			})
		}
		if meta, ok := supersessionLineage[doc.NodeID]; ok {
			doc = mergeLineageSearchMetadata(doc, meta)
		}
		if candidate.SupersessionBoost > 0 || supersessionLineage[doc.NodeID].CurrentHeadID != "" {
			doc.Metadata = mergeSearchMetadata(doc.Metadata, map[string]any{
				"supersession_canonicalized": true,
			})
		}
		if candidate.ConversationBoost > 0 {
			doc.Metadata = mergeSearchMetadata(doc.Metadata, map[string]any{
				"conversation_expanded": true,
			})
		}
		if rank, ok := rerankRanks[doc.NodeID]; ok {
			doc.Metadata = mergeSearchMetadata(doc.Metadata, map[string]any{
				"rerank_applied": true,
				"rerank_rank":    rank,
				"rerank_score":   rerankScore,
			})
		}
		doc.Score = components.SemanticScore
		doc.EffectiveScore = components.EffectiveScore
		if cfg.useRelations {
			doc.Related = relatedMap[doc.NodeID]
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
		BaseScores: seedScores,
	}, nil
}

func (e *Engine) searchWithoutVectors(ctx context.Context, input domain.SearchInput, variant SearchVariant) (DebugSearchResult, error) {
	repoInput := input
	repoInput.MemoryTypes = repoSearchTypes(input)
	repoInput.MinScore = 0
	if repoInput.TopN > 0 {
		repoInput.TopN = max(repoInput.TopN*4, 20)
	} else {
		repoInput.TopN = 200
	}
	result, err := e.repo.Search(ctx, repoInput)
	if err != nil {
		return DebugSearchResult{}, err
	}
	baseScores := make(map[string]float64, len(result.Memories))
	for _, doc := range result.Memories {
		baseScores[doc.NodeID] = doc.Score
	}
	conversationBoosts := map[string]float64{}
	if input.IncludeConversation {
		resultScores, boosts, docs, err := e.expandConversationCandidates(ctx, input, baseScores, result.Memories)
		if err != nil {
			return DebugSearchResult{}, err
		}
		conversationBoosts = boosts
		result.Memories = docs
		for idx := range result.Memories {
			if score, ok := resultScores[result.Memories[idx].NodeID]; ok && conversationBoosts[result.Memories[idx].NodeID] > 0 {
				result.Memories[idx].Score = score
			}
		}
	}
	scoreMap := cloneScoreMap(baseScores)
	for nodeID, score := range conversationBoosts {
		if score > scoreMap[nodeID] {
			scoreMap[nodeID] = score
		}
	}
	scoreMap, supersessionBoosts, supersessionLineage, err := e.canonicalizeSupersededScores(ctx, input, scoreMap, nil)
	if err != nil {
		return DebugSearchResult{}, err
	}
	candidateNodeIDs := make([]string, 0, len(scoreMap))
	for nodeID := range scoreMap {
		candidateNodeIDs = append(candidateNodeIDs, nodeID)
	}
	result.Memories, err = e.repo.ListDocumentsByNodeIDsInGeneration(ctx, input.UserID, input.GenerationID, candidateNodeIDs)
	if err != nil {
		return DebugSearchResult{}, err
	}
	cfg := configForVariant(variant)
	now := e.now()
	candidates := make([]searchCandidate, 0, len(result.Memories))
	componentBaseScores := make(map[string]float64, len(result.Memories))
	for _, doc := range result.Memories {
		if doc.ID == "" || !matchesTemporalSearchInput(doc, input) || !matchesSearchDocument(doc, input) {
			continue
		}
		components := scoreDocumentAt(doc, baseScores[doc.NodeID], 0, 0, supersessionBoosts[doc.NodeID], conversationBoosts[doc.NodeID], 0, 0, now, cfg)
		candidates = append(candidates, searchCandidate{
			Document:          doc,
			PreliminaryScore:  components.EffectiveScore,
			BaseSemanticScore: baseScores[doc.NodeID],
			SupersessionBoost: supersessionBoosts[doc.NodeID],
			ConversationBoost: conversationBoosts[doc.NodeID],
		})
		componentBaseScores[doc.NodeID] = components.BaseSemanticScore
	}
	rerankScores, rerankRanks := e.rerankCandidates(ctx, input, candidates)

	componentsByID := make(map[string]ScoreComponents, len(candidates))
	filtered := make([]domain.MemoryDocument, 0, len(candidates))
	rerankConfig := e.RerankConfig()
	for _, candidate := range candidates {
		doc := candidate.Document
		rerankScore := rerankScores[doc.NodeID]
		rerankWeight := 0.0
		if _, ok := rerankRanks[doc.NodeID]; ok {
			rerankWeight = rerankConfig.BlendWeight
		}
		components := scoreDocumentAt(doc, candidate.BaseSemanticScore, 0, 0, candidate.SupersessionBoost, candidate.ConversationBoost, rerankScore, rerankWeight, now, cfg)
		if meta, ok := supersessionLineage[doc.NodeID]; ok {
			doc = mergeLineageSearchMetadata(doc, meta)
		}
		if candidate.SupersessionBoost > 0 {
			doc.Metadata = mergeSearchMetadata(doc.Metadata, map[string]any{
				"supersession_canonicalized": true,
			})
		}
		if candidate.ConversationBoost > 0 {
			doc.Metadata = mergeSearchMetadata(doc.Metadata, map[string]any{
				"conversation_expanded": true,
			})
		}
		if rank, ok := rerankRanks[doc.NodeID]; ok {
			doc.Metadata = mergeSearchMetadata(doc.Metadata, map[string]any{
				"rerank_applied": true,
				"rerank_rank":    rank,
				"rerank_score":   rerankScore,
			})
		}
		doc.Score = components.SemanticScore
		doc.EffectiveScore = components.EffectiveScore
		componentsByID[doc.ID] = components
		if doc.EffectiveScore < input.MinScore {
			continue
		}
		filtered = append(filtered, doc)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].EffectiveScore == filtered[j].EffectiveScore {
			return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
		}
		return filtered[i].EffectiveScore > filtered[j].EffectiveScore
	})
	if input.TopN > 0 && len(filtered) > input.TopN {
		filtered = filtered[:input.TopN]
	}
	result.Memories = filtered
	result.Total = len(filtered)
	return DebugSearchResult{
		Result:     result,
		Variant:    variant,
		Components: componentsByID,
		BaseScores: componentBaseScores,
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

func lineageGroupIDFromMetadata(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata["lineage_group_id"]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func mergeLineageSearchMetadata(doc domain.MemoryDocument, meta domain.MemoryLineageMeta) domain.MemoryDocument {
	if len(meta.SupersededAliasNodeIDs) == 0 && len(meta.LineagePath) == 0 && meta.CurrentHeadID == "" {
		return doc
	}
	extra := map[string]any{}
	if len(meta.SupersededAliasNodeIDs) > 0 {
		extra["superseded_alias_node_ids"] = meta.SupersededAliasNodeIDs
	}
	if len(meta.LineagePath) > 0 {
		extra["lineage_path"] = meta.LineagePath
	}
	if meta.RevisionDepth > 0 {
		extra["revision_depth"] = meta.RevisionDepth
	}
	if meta.CurrentHeadID != "" {
		extra["current_head_id"] = meta.CurrentHeadID
		if doc.ID == meta.CurrentHeadID {
			extra["current_head_node_id"] = doc.NodeID
		}
	}
	doc.Metadata = mergeSearchMetadata(doc.Metadata, extra)
	if doc.Lineage == nil {
		doc.Lineage = &meta
	} else {
		if len(doc.Lineage.SupersededAliasNodeIDs) == 0 {
			doc.Lineage.SupersededAliasNodeIDs = meta.SupersededAliasNodeIDs
		}
		if len(doc.Lineage.LineagePath) == 0 {
			doc.Lineage.LineagePath = meta.LineagePath
		}
		if doc.Lineage.RevisionDepth == 0 {
			doc.Lineage.RevisionDepth = meta.RevisionDepth
		}
		if meta.CurrentHeadID != "" {
			doc.Lineage.CurrentHeadID = meta.CurrentHeadID
		} else if doc.Lineage.CurrentHeadID == "" {
			doc.Lineage.CurrentHeadID = doc.ID
		}
		if doc.Lineage.FactKey == "" {
			doc.Lineage.FactKey = meta.FactKey
		}
		if doc.Lineage.LineageGroupID == "" {
			doc.Lineage.LineageGroupID = meta.LineageGroupID
		}
	}
	return doc
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
	if input.UserID != "" && doc.UserID != input.UserID {
		return false
	}
	if doc.WorkspaceID != "" && doc.WorkspaceID != domain.PersonalWorkspaceID(input.UserID) {
		return false
	}
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
		return domain.DefaultSearchMemoryTypes(input.IncludeSegments)
	}
	return append([]domain.MemoryType{}, input.MemoryTypes...)
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

func expandRelationScores(base map[string]float64, relations []domain.MemoryRelation, cfg RelationRecallConfig) (map[string]float64, map[string][]domain.RelatedMemory) {
	scores, related, _ := expandRelationPaths(base, relations, cfg)
	return scores, related
}

func (cfg RelationRecallConfig) normalized() RelationRecallConfig {
	if cfg.MaxHops <= 0 {
		cfg.MaxHops = 2
	}
	if cfg.MaxHops > 4 {
		cfg.MaxHops = 4
	}
	if cfg.HopDecayLambda < 0 {
		cfg.HopDecayLambda = 0
	}
	if cfg.HopDecayLambda > 2 {
		cfg.HopDecayLambda = 2
	}
	if cfg.HopDecayExponent <= 0 {
		cfg.HopDecayExponent = 1.25
	}
	if cfg.HopDecayExponent > 3 {
		cfg.HopDecayExponent = 3
	}
	return cfg
}

func (cfg RerankConfig) normalized() RerankConfig {
	defaults := DefaultRerankConfig()
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.CandidateMultiplier <= 0 {
		cfg.CandidateMultiplier = defaults.CandidateMultiplier
	}
	if cfg.CandidateMultiplier > 16 {
		cfg.CandidateMultiplier = 16
	}
	if cfg.MinCandidates <= 0 {
		cfg.MinCandidates = defaults.MinCandidates
	}
	if cfg.MaxCandidates <= 0 {
		cfg.MaxCandidates = defaults.MaxCandidates
	}
	if cfg.MaxCandidates < cfg.MinCandidates {
		cfg.MaxCandidates = cfg.MinCandidates
	}
	if cfg.BlendWeight < 0 {
		cfg.BlendWeight = defaults.BlendWeight
	}
	if cfg.BlendWeight > 1 {
		cfg.BlendWeight = 1
	}
	return cfg
}

func (cfg RerankConfig) enabled() bool {
	cfg = cfg.normalized()
	return cfg.Model != "" && cfg.BlendWeight > 0
}

func (cfg RelationRecallConfig) hopPenalty(hop int) float64 {
	cfg = cfg.normalized()
	if cfg.HopDecayLambda == 0 {
		return 1
	}
	// A stretched exponential keeps near associations usable while making
	// farther hops decay faster than a simple linear penalty.
	distance := float64(hop)
	if distance <= 0 {
		return 1
	}
	return clamp(math.Exp(-cfg.HopDecayLambda*math.Pow(distance, cfg.HopDecayExponent)), 0, 1)
}

func (e *Engine) canonicalizeSupersededScores(ctx context.Context, input domain.SearchInput, scoreMap map[string]float64, relations []domain.MemoryRelation) (map[string]float64, map[string]float64, map[string]domain.MemoryLineageMeta, error) {
	if e.repo == nil || len(scoreMap) == 0 {
		return scoreMap, nil, nil, nil
	}

	nodeIDs := make([]string, 0, len(scoreMap))
	for nodeID := range scoreMap {
		nodeIDs = append(nodeIDs, nodeID)
	}
	docs, err := e.repo.ListDocumentsByNodeIDsInGeneration(ctx, input.UserID, input.GenerationID, nodeIDs)
	if err != nil {
		return nil, nil, nil, err
	}

	originalScores := cloneScoreMap(scoreMap)
	canonicalizedScores := cloneScoreMap(scoreMap)
	inheritedScores := map[string]float64{}
	lineageMeta := map[string]domain.MemoryLineageMeta{}
	lineageCache := map[string]domain.FactLineage{}
	for _, doc := range docs {
		if doc.Type != domain.MemoryTypeFact || doc.NodeID == "" {
			continue
		}
		score, ok := originalScores[doc.NodeID]
		if !ok {
			continue
		}
		lineageGroupID := lineageGroupIDFromMetadata(doc.Metadata)
		if lineageGroupID == "" {
			continue
		}
		lineage, ok := lineageCache[lineageGroupID]
		if !ok {
			lineageValue, lineageErr := e.repo.ListFactLineageByKey(ctx, input.UserID, doc.GenerationID, lineageGroupID)
			if lineageErr != nil || len(lineageValue.Entries) < 2 || lineageValue.CurrentHeadID == "" {
				continue
			}
			lineage = lineageValue
			lineageCache[lineageGroupID] = lineage
		}
		var head domain.FactLineageEntry
		for _, entry := range lineage.Entries {
			if entry.FactID == lineage.CurrentHeadID {
				head = entry
				break
			}
		}
		if head.NodeID == "" || head.NodeID == doc.NodeID {
			continue
		}
		if score > inheritedScores[head.NodeID] {
			inheritedScores[head.NodeID] = score
		}
		meta := lineageMeta[head.NodeID]
		meta.FactKey = lineage.FactKey
		meta.LineageGroupID = lineage.LineageGroupID
		meta.CurrentHeadID = lineage.CurrentHeadID
		meta.SupersededAliasNodeIDs = appendUniqueString(meta.SupersededAliasNodeIDs, doc.NodeID)
		meta.LineagePath = append([]string(nil), lineage.LineagePath...)
		meta.RevisionDepth = max(meta.RevisionDepth, len(meta.LineagePath))
		if lineage.RevisionDepth > meta.RevisionDepth {
			meta.RevisionDepth = lineage.RevisionDepth
		}
		meta.GenerationID = doc.GenerationID
		lineageMeta[head.NodeID] = meta
		delete(canonicalizedScores, doc.NodeID)
	}
	_ = relations

	boosts := map[string]float64{}
	for nodeID, inherited := range inheritedScores {
		if inherited > canonicalizedScores[nodeID] {
			canonicalizedScores[nodeID] = inherited
		}
		if inherited > originalScores[nodeID] {
			boosts[nodeID] = inherited - originalScores[nodeID]
		}
	}
	return canonicalizedScores, boosts, lineageMeta, nil
}

func (e *Engine) expandConversationCandidates(ctx context.Context, input domain.SearchInput, scoreMap map[string]float64, docs []domain.MemoryDocument) (map[string]float64, map[string]float64, []domain.MemoryDocument, error) {
	if !input.IncludeConversation || e.repo == nil || len(docs) == 0 {
		return scoreMap, nil, docs, nil
	}

	conversationSeeds := map[string]float64{}
	seenNodeIDs := make(map[string]struct{}, len(docs))
	for _, doc := range docs {
		seenNodeIDs[doc.NodeID] = struct{}{}
		if strings.TrimSpace(doc.ConversationID) == "" {
			continue
		}
		if score := scoreMap[doc.NodeID]; score > conversationSeeds[doc.ConversationID] {
			conversationSeeds[doc.ConversationID] = score
		}
	}
	if len(conversationSeeds) == 0 {
		return scoreMap, nil, docs, nil
	}

	expandedScores := cloneScoreMap(scoreMap)
	conversationBoosts := map[string]float64{}
	extraDocs := make([]domain.MemoryDocument, 0)
	for conversationID, seedScore := range conversationSeeds {
		if seedScore <= 0 {
			continue
		}
		companions, err := e.repo.ListConversationDocumentsInGeneration(ctx, input.UserID, input.GenerationID, conversationID, input.IncludeSegments)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, companion := range companions {
			if companion.NodeID == "" || companion.ConversationID != conversationID {
				continue
			}
			if _, exists := expandedScores[companion.NodeID]; exists {
				continue
			}
			boost := seedScore * conversationCompanionFactor(companion.Type)
			if boost <= 0 {
				continue
			}
			expandedScores[companion.NodeID] = boost
			conversationBoosts[companion.NodeID] = boost
			if _, seen := seenNodeIDs[companion.NodeID]; seen {
				continue
			}
			seenNodeIDs[companion.NodeID] = struct{}{}
			extraDocs = append(extraDocs, companion)
		}
	}

	if len(extraDocs) == 0 {
		return expandedScores, conversationBoosts, docs, nil
	}
	return expandedScores, conversationBoosts, append(docs, extraDocs...), nil
}

func (e *Engine) rerankCandidates(ctx context.Context, input domain.SearchInput, candidates []searchCandidate) (map[string]float64, map[string]int) {
	scores := map[string]float64{}
	ranks := map[string]int{}
	if e == nil || e.reranker == nil || len(candidates) < 2 {
		return scores, ranks
	}
	cfg := e.RerankConfig()
	if !cfg.enabled() || strings.TrimSpace(input.Query) == "" {
		return scores, ranks
	}

	ordered := append([]searchCandidate{}, candidates...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].PreliminaryScore == ordered[j].PreliminaryScore {
			return ordered[i].Document.UpdatedAt.After(ordered[j].Document.UpdatedAt)
		}
		return ordered[i].PreliminaryScore > ordered[j].PreliminaryScore
	})

	limit := max(input.TopN*cfg.CandidateMultiplier, cfg.MinCandidates)
	if cfg.MaxCandidates > 0 && limit > cfg.MaxCandidates {
		limit = cfg.MaxCandidates
	}
	if limit > len(ordered) {
		limit = len(ordered)
	}
	if limit < 2 {
		return scores, ranks
	}

	documents := make([]domain.RerankDocument, 0, limit)
	for _, candidate := range ordered[:limit] {
		text := rerankDocumentText(candidate.Document)
		if text == "" {
			continue
		}
		documents = append(documents, domain.RerankDocument{
			ID:   candidate.Document.NodeID,
			Text: text,
		})
	}
	if len(documents) < 2 {
		return scores, ranks
	}

	results, err := e.reranker.Rerank(ctx, domain.RerankInput{
		UserID:    input.UserID,
		Query:     input.Query,
		Model:     cfg.Model,
		TopN:      len(documents),
		Documents: documents,
	})
	if err != nil {
		return scores, ranks
	}
	for idx, item := range results {
		if item.DocumentID == "" {
			continue
		}
		scores[item.DocumentID] = item.Score
		ranks[item.DocumentID] = idx + 1
	}
	return scores, ranks
}

func rerankDocumentText(doc domain.MemoryDocument) string {
	content := strings.TrimSpace(doc.Content)
	if content == "" {
		return ""
	}
	return "[" + string(doc.Type) + "] " + content
}

func relationBoostFactor(relationType string) float64 {
	switch strings.TrimSpace(strings.ToLower(relationType)) {
	case domain.MemoryRelationDerivedFrom, "promoted_to_key_memory", domain.MemoryRelationResultedIn:
		return 0.35
	case domain.MemoryRelationSupports, domain.MemoryRelationMentionsEntity:
		return 0.12
	case "summarized_into", domain.MemoryRelationProducedArtifact:
		return 0.2
	case domain.MemoryRelationUsedTool, domain.MemoryRelationDependsOn:
		return 0.16
	case domain.MemoryRelationContradicts, domain.MemoryRelationReplaces, domain.MemoryRelationSupersedes:
		// Contradictory and superseded history is retained for auditability but
		// must never be promoted by neighbourhood expansion.
		return 0
	default:
		return 0.18
	}
}

func conversationCompanionFactor(memoryType domain.MemoryType) float64 {
	switch memoryType {
	case domain.MemoryTypeFact:
		return 0.62
	case domain.MemoryTypeSummary:
		return 0.56
	case domain.MemoryTypeImpression:
		return 0.48
	case domain.MemoryTypeSegment:
		return 0.42
	case domain.MemoryTypeTask:
		return 0.6
	case domain.MemoryTypeProcedure:
		return 0.58
	case domain.MemoryTypeEvent:
		return 0.54
	case domain.MemoryTypeAgentAction:
		return 0.5
	case domain.MemoryTypeArtifact, domain.MemoryTypeEntity:
		return 0.46
	default:
		return 0.4
	}
}

func appendUniqueString(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
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
				item.Metadata["injection_bucket"] = merged[index].Metadata["injection_bucket"]
				merged[index] = item
			}
			return
		}
		seen[item.NodeID] = len(merged)
		merged = append(merged, item)
	}
	for _, item := range keyMemories {
		appendItem(item, "key_memory_topk")
	}
	for _, item := range primary {
		appendItem(item, "semantic_recall")
	}
	// Stable ordering: injection priority, then by effective score and recency.
	sort.SliceStable(merged, func(i, j int) bool {
		if lp, rp := injectionPriority(merged[i]), injectionPriority(merged[j]); lp != rp {
			return lp < rp
		}
		if merged[i].EffectiveScore != merged[j].EffectiveScore {
			return merged[i].EffectiveScore > merged[j].EffectiveScore
		}
		return merged[i].ActivityAt.After(merged[j].ActivityAt)
	})
	return merged
}

func injectionPriority(doc domain.MemoryDocument) int {
	switch doc.Type {
	case domain.MemoryTypeKeyMemory:
		return 0
	case domain.MemoryTypeTask:
		return 1
	case domain.MemoryTypeProcedure:
		return 2
	case domain.MemoryTypeFact:
		return 3
	case domain.MemoryTypeEvent, domain.MemoryTypeAgentAction:
		return 4
	case domain.MemoryTypeArtifact, domain.MemoryTypeEntity:
		return 5
	case domain.MemoryTypeSummary, domain.MemoryTypeSegment:
		return 6
	case domain.MemoryTypeImpression:
		return 7
	default:
		return 8
	}
}

func injectionEligible(doc domain.MemoryDocument) bool {
	status := strings.TrimSpace(strings.ToLower(memoryMetadataString(doc.Metadata, "status")))
	reviewStatus := strings.TrimSpace(strings.ToLower(memoryMetadataString(doc.Metadata, "review_status")))
	switch doc.Type {
	case domain.MemoryTypeTask:
		return status == "" || status == "pending" || status == "current" || status == "in_progress" || status == "blocked"
	case domain.MemoryTypeProcedure:
		return reviewStatus == "approved"
	case domain.MemoryTypeFact:
		return !domain.IsStaleMemory(doc, time.Now().UTC())
	case domain.MemoryTypeArtifact:
		return status != "rejected" && status != "deleted" && status != "stale" && status != "failed" && status != "error"
	case domain.MemoryTypeEvent, domain.MemoryTypeEntity, domain.MemoryTypeAgentAction:
		return status != "rejected" && status != "deleted" && status != "stale"
	default:
		return true
	}
}

func packInjectionMemories(memories []domain.MemoryDocument, maxTokens int) []domain.MemoryDocument {
	if len(memories) == 0 {
		return nil
	}
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	// Input is already sorted by injection priority and then by effective score
	// (see mergeInjectionMemories). A single greedy pass preserves that ordering
	// while enforcing the configured token budget strictly.
	remaining := maxTokens
	selected := make([]domain.MemoryDocument, 0, len(memories))
	for _, item := range memories {
		if !injectionEligible(item) {
			continue
		}
		tokens := estimateMemoryTokens(item)
		if tokens > remaining {
			continue
		}
		selected = append(selected, item)
		remaining -= tokens
		if remaining <= 0 {
			break
		}
	}
	if len(selected) == 0 {
		return nil
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
	case "indexed_in_tier", "occurred_on_day", "day_in_month", "month_in_year", domain.MemoryRelationSupersedes:
		return true
	default:
		return false
	}
}

// buildInjectionSystemPrompt formats retrieved memories into the structured
// system-prompt layout. Key memories render as a
// profile bullet list, summaries/facts/impressions render as labelled sections.
// Only non-empty sections are emitted so the prompt stays tight.
func buildInjectionSystemPrompt(memories []domain.MemoryDocument) string {
	var (
		keyMemories  []domain.MemoryDocument
		tasks        []domain.MemoryDocument
		procedures   []domain.MemoryDocument
		facts        []domain.MemoryDocument
		events       []domain.MemoryDocument
		agentActions []domain.MemoryDocument
		artifacts    []domain.MemoryDocument
		entities     []domain.MemoryDocument
		summaries    []domain.MemoryDocument
		impressions  []domain.MemoryDocument
		other        []domain.MemoryDocument
	)
	for _, item := range memories {
		switch item.Type {
		case domain.MemoryTypeKeyMemory:
			keyMemories = append(keyMemories, item)
		case domain.MemoryTypeTask:
			tasks = append(tasks, item)
		case domain.MemoryTypeProcedure:
			procedures = append(procedures, item)
		case domain.MemoryTypeFact:
			facts = append(facts, item)
		case domain.MemoryTypeEvent:
			events = append(events, item)
		case domain.MemoryTypeAgentAction:
			agentActions = append(agentActions, item)
		case domain.MemoryTypeArtifact:
			artifacts = append(artifacts, item)
		case domain.MemoryTypeEntity:
			entities = append(entities, item)
		case domain.MemoryTypeSummary, domain.MemoryTypeSegment:
			summaries = append(summaries, item)
		case domain.MemoryTypeImpression:
			impressions = append(impressions, item)
		default:
			other = append(other, item)
		}
	}

	var b strings.Builder
	b.WriteString("[Memory Context]\n")

	if len(keyMemories) > 0 {
		b.WriteString("User Profile:\n")
		for _, item := range keyMemories {
			key := keyMemoryLabel(item)
			value := strings.TrimSpace(item.Content)
			if key != "" {
				b.WriteString("- ")
				b.WriteString(key)
				b.WriteString(": ")
				b.WriteString(value)
			} else {
				b.WriteString("- ")
				b.WriteString(value)
			}
			b.WriteString("\n")
		}
	}

	writeBulletSection := func(title string, items []domain.MemoryDocument) {
		if len(items) == 0 {
			return
		}
		b.WriteString("\n")
		b.WriteString(title)
		b.WriteString(":\n")
		for _, item := range items {
			b.WriteString("- ")
			if label := genericMemoryLabel(item); label != "" {
				b.WriteString(label)
				b.WriteString(": ")
			}
			b.WriteString(strings.TrimSpace(item.Content))
			b.WriteString("\n")
		}
	}

	writeBulletSection("Active Tasks", tasks)
	writeBulletSection("Relevant Procedures", procedures)
	writeBulletSection("Relevant Facts", facts)
	writeBulletSection("Relevant Events", events)
	writeBulletSection("Relevant Agent Actions", agentActions)
	writeBulletSection("Relevant Artifacts", artifacts)
	writeBulletSection("Relevant Entities", entities)

	if len(summaries) > 0 {
		b.WriteString("\nRelevant Memories:\n")
		for i, item := range summaries {
			fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(item.Content))
		}
	}

	if len(impressions) > 0 {
		b.WriteString("\nRelevant Impressions:\n")
		for _, item := range impressions {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(item.Content))
			b.WriteString("\n")
		}
	}

	if len(other) > 0 {
		b.WriteString("\nAdditional Context:\n")
		for _, item := range other {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(item.Content))
			b.WriteString("\n")
		}
	}

	b.WriteString("\nUse these naturally. Do not explicitly say you remembered them unless asked.\n")
	b.WriteString("[/Memory Context]")
	return b.String()
}

func keyMemoryLabel(item domain.MemoryDocument) string {
	if item.Metadata != nil {
		if key, ok := item.Metadata["key"].(string); ok && strings.TrimSpace(key) != "" {
			return strings.TrimSpace(key)
		}
	}
	return ""
}

func genericMemoryLabel(item domain.MemoryDocument) string {
	for _, key := range []string{"title", "status", "review_status"} {
		if value := memoryMetadataString(item.Metadata, key); value != "" && value != "auto" && value != "current" {
			return value
		}
	}
	return ""
}

func memoryMetadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}
