package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"memstation/apps/server/internal/aicall"

	"github.com/google/uuid"

	"memstation/apps/server/internal/cache"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
)

type Service struct {
	watermarks  ports.RecallWatermarkReader
	engine      ports.MemoryEngine
	repo        ports.MemoryRepository
	cache       *cache.JSONStore
	vectors     ports.VectorStore
	scorer      *Scorer
	generations *memorygenservice.Service
}

func (s *Service) SetWatermarks(reader ports.RecallWatermarkReader) { s.watermarks = reader }

func NewService(engine ports.MemoryEngine, repo ports.MemoryRepository) *Service {
	return &Service{engine: engine, repo: repo}
}

func (s *Service) SetKeyMemoryCache(store *cache.JSONStore) {
	s.cache = store
}

func (s *Service) SetVectorStore(store ports.VectorStore) {
	s.vectors = store
}

func (s *Service) SetScorer(scorer *Scorer) {
	s.scorer = scorer
}

func (s *Service) SetGenerationService(service *memorygenservice.Service) {
	s.generations = service
}

func (s *Service) Search(ctx context.Context, input domain.SearchInput) (domain.SearchResult, error) {
	started := time.Now()
	ctx, calls := aicall.WithTrace(ctx)
	if err := security.RequireCapability(ctx, domain.CapabilityMemoryRead); err != nil {
		return domain.SearchResult{}, err
	}
	if len(input.SubjectActorIDs) > 64 || len(input.QueryID) > 256 {
		return domain.SearchResult{}, domain.ErrAccessDenied
	}
	ctx = security.WithMemoryFilter(ctx, security.MemoryFilter{SubjectActorIDs: input.SubjectActorIDs, From: input.DateFrom, To: input.DateTo})
	if _, err := security.DataScope(ctx, input.UserID, input.WorkspaceID); err != nil {
		return domain.SearchResult{}, err
	}
	input.WorkspaceID = security.WorkspaceID(ctx, input.UserID)
	if security.RequireCapability(ctx, domain.CapabilitySourcesRead) != nil {
		if len(input.MemoryTypes) == 0 {
			input.MemoryTypes = domain.DefaultSearchMemoryTypes(false)
		}
		input.MemoryTypes = slices.DeleteFunc(slices.Clone(input.MemoryTypes), func(v domain.MemoryType) bool { return v == domain.MemoryTypeSegment })
		if len(input.MemoryTypes) == 0 {
			return domain.SearchResult{Memories: []domain.MemoryDocument{}}, nil
		}
	}
	result, err := s.engine.Search(ctx, input)
	if err != nil {
		return domain.SearchResult{}, err
	}
	if input.AsOf == nil && input.KnownAt == nil {
		s.hydrateRelatedDocumentsBatch(ctx, memoryDocumentPointers(result.Memories))
	}
	returned := len(result.Memories)
	if err = s.filterLiveSearch(ctx, input.UserID, &result, input.AsOf != nil || input.KnownAt != nil); err != nil {
		return domain.SearchResult{}, err
	}
	if err = security.RequireCapability(ctx, domain.CapabilityMemoryRead); err != nil {
		return domain.SearchResult{}, err
	}
	queryID := input.QueryID
	if queryID == "" {
		queryID = domain.AccessID("query", fmt.Sprint(started.UnixNano()))
	}
	result.Trace = &domain.RecallTrace{QueryID: queryID, LatencyMS: time.Since(started).Milliseconds(), ReturnedCount: returned, EligibleCount: len(result.Memories), ScoreDefinition: "provider semantic score retained in metadata; effective score combines semantic relevance, importance, time decay and configured relation/reranking adjustments; not factual confidence"}
	result.Trace.ProviderCalls = calls.Calls()
	result.Trace.FailureCode = calls.FailureCode()
	result.Trace.Degraded = result.Trace.FailureCode != ""
	for _, call := range result.Trace.ProviderCalls {
		if call.ProviderRequestID != "" && result.Trace.ProviderRequestID == "" {
			result.Trace.ProviderRequestID = call.ProviderRequestID
		}
	}
	result.Trace.Watermarks = []domain.StreamWatermark{}
	if s.watermarks != nil {
		result.Trace.Watermarks, result.Trace.WatermarksPartial, err = s.watermarks.Watermarks(ctx)
		if err != nil {
			result.Trace.Degraded = true
			if result.Trace.FailureCode == "" {
				result.Trace.FailureCode = "watermarks_unavailable"
			}
		}
	}
	if err = security.RequireCapability(ctx, domain.CapabilityMemoryRead); err != nil {
		return domain.SearchResult{}, err
	}
	result.Trace.LatencyMS = time.Since(started).Milliseconds()
	result.Memories = security.MemoryContents(ctx, result.Memories)
	result.Total = len(result.Memories)
	result.Trace.EligibleCount = len(result.Memories)
	return result, nil
}

func (s *Service) GetDocument(ctx context.Context, userID, id string, kind domain.MemoryType) (domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemoryDocument{}, err
	}
	return s.GetDocumentInGeneration(ctx, userID, "", id, kind)
}

func (s *Service) GetDocumentInGeneration(ctx context.Context, userID, generationID, id string, kind domain.MemoryType) (domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemoryDocument{}, err
	}
	resolvedGenerationID, err := s.resolveGenerationID(ctx, userID, generationID)
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	item, err := s.repo.GetDocumentInGeneration(ctx, userID, resolvedGenerationID, id, kind)
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	s.hydrateRelatedDocuments(ctx, &item)
	if visible, ok := security.MemoryContent(ctx, item); ok {
		return visible, nil
	}
	return domain.MemoryDocument{}, domain.ErrAccessNotFound
}

func (s *Service) ListDocuments(ctx context.Context, userID string, kinds []domain.MemoryType, limit int) ([]domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	return s.ListDocumentsInGeneration(ctx, userID, "", kinds, limit)
}

func (s *Service) ListDocumentsInGeneration(ctx context.Context, userID, generationID string, kinds []domain.MemoryType, limit int) ([]domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	resolvedGenerationID, err := s.resolveGenerationID(ctx, userID, generationID)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ListDocumentsInGeneration(ctx, userID, resolvedGenerationID, kinds, limit)
	if err != nil {
		return nil, err
	}
	s.hydrateRelatedDocumentsBatch(ctx, memoryDocumentPointers(items))
	return security.MemoryContents(ctx, items), nil
}

func (s *Service) ListDocumentsByConversationInGeneration(ctx context.Context, userID, generationID, conversationID string) ([]domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	resolvedGenerationID, err := s.resolveGenerationID(ctx, userID, generationID)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ListDocumentsByConversationInGeneration(ctx, userID, resolvedGenerationID, conversationID)
	if err != nil {
		return nil, err
	}
	s.hydrateRelatedDocumentsBatch(ctx, memoryDocumentPointers(items))
	return security.MemoryContents(ctx, items), nil
}

func memoryDocumentPointers(items []domain.MemoryDocument) []*domain.MemoryDocument {
	if len(items) == 0 {
		return nil
	}
	out := make([]*domain.MemoryDocument, 0, len(items))
	for idx := range items {
		out = append(out, &items[idx])
	}
	return out
}

func isTemporalRelationTypeForHydration(relationType string) bool {
	switch relationType {
	case "indexed_in_tier", "occurred_on_day", "day_in_month", "month_in_year", domain.MemoryRelationSupersedes:
		return true
	default:
		return false
	}
}

func (s *Service) UpdateDocument(ctx context.Context, userID, id string, kind domain.MemoryType, content string) (domain.MemoryDocument, error) {
	return s.updateDocument(ctx, userID, id, kind, content, nil)
}

func (s *Service) updateDocument(ctx context.Context, userID, id string, kind domain.MemoryType, content string, condition *domain.MemoryWriteCondition) (domain.MemoryDocument, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.MemoryDocument{}, err
	}
	var item domain.MemoryDocument
	var err error
	if condition == nil {
		item, err = s.repo.UpdateDocument(ctx, userID, id, kind, content)
	} else if repo, ok := s.repo.(ports.ConditionalMemoryRepository); ok {
		item, err = repo.UpdateDocumentConditional(ctx, userID, id, kind, content, *condition)
	} else {
		err = domain.ErrAccessDenied
	}
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	if !hasTransactionalMemoryOutbox(s.repo) {
		if err := s.syncDocumentEmbedding(ctx, item); err != nil {
			return domain.MemoryDocument{}, err
		}
	}
	s.hydrateRelatedDocuments(ctx, &item)
	return item, nil
}

func (s *Service) ListKeyMemories(ctx context.Context, userID string, limit int) ([]domain.KeyMemory, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	if s.cache != nil {
		var cached []domain.KeyMemory
		ok, err := s.cache.Get(ctx, keyMemoryCacheKey(ctx, userID, limit), &cached)
		if err == nil && ok {
			valid := true
			for _, item := range cached {
				if item.UserID != userID || item.WorkspaceID != security.WorkspaceID(ctx, userID) {
					valid = false
					break
				}
			}
			if valid {
				ids := make([]string, 0, len(cached))
				for _, item := range cached {
					if node, ok := item.Metadata["node_id"].(string); ok {
						ids = append(ids, node)
					}
				}
				live, err := s.repo.ListDocumentsByNodeIDs(ctx, userID, ids)
				if err != nil {
					return nil, err
				}
				values := map[string]string{}
				for _, doc := range live {
					values[doc.NodeID] = doc.Content
				}
				for _, item := range cached {
					node, _ := item.Metadata["node_id"].(string)
					if node == "" || values[node] != item.Key+": "+item.Value {
						valid = false
						break
					}
				}
				if valid {
					if err = security.RequireCapability(ctx, domain.CapabilityMemoryRead); err != nil {
						return nil, err
					}
					return cached, nil
				}
			}
		}
	}

	fetchLimit := limit
	if fetchLimit <= 0 {
		fetchLimit = 100
	}
	fetchLimit = max(fetchLimit*10, fetchLimit)
	if fetchLimit < 200 {
		fetchLimit = 200
	}
	memories, err := s.repo.ListKeyMemories(ctx, userID, fetchLimit)
	if err != nil {
		return nil, err
	}
	rankKeyMemories(memories)
	if limit > 0 && len(memories) > limit {
		memories = memories[:limit]
	}
	if s.cache != nil {
		_ = s.cache.Set(ctx, keyMemoryCacheKey(ctx, userID, limit), memories)
	}
	return memories, nil
}

func (s *Service) SearchKeyMemories(ctx context.Context, input domain.KeyMemoryQuery) ([]domain.KeyMemory, error) {
	if _, err := security.DataScope(ctx, input.UserID, input.WorkspaceID); err != nil {
		return nil, err
	}
	input.WorkspaceID = security.WorkspaceID(ctx, input.UserID)
	if s.repo == nil {
		return nil, nil
	}
	if input.TopK <= 0 {
		input.TopK = 10
	}
	if strings.TrimSpace(input.Query) != "" && s.engine != nil {
		result, err := s.engine.Search(ctx, domain.SearchInput{
			UserID:      input.UserID,
			Query:       input.Query,
			TopN:        input.TopK,
			MemoryTypes: []domain.MemoryType{domain.MemoryTypeKeyMemory},
			TimeTiers:   input.TimeTiers,
			DateFrom:    input.DateFrom,
			DateTo:      input.DateTo,
			MinScore:    0,
		})
		if err != nil {
			return nil, err
		}
		return s.keyMemoriesFromDocuments(ctx, input.UserID, result.Memories)
	}

	memories, err := s.repo.ListKeyMemories(ctx, input.UserID, max(input.TopK*10, 200))
	if err != nil {
		return nil, err
	}
	filtered := make([]domain.KeyMemory, 0, len(memories))
	now := time.Now().UTC()
	for _, item := range memories {
		if !matchesTemporalFilter(item.ActivityAt, item.TimeTier, input.DateFrom, input.DateTo, input.TimeTiers) {
			continue
		}
		item.Importance = domain.ClampImportance(item.Importance)
		item.DecayFactor = domain.ClampDecayFactor(item.DecayFactor)
		item.EffectiveScore = item.Importance * domain.TimeDecay(domain.MemoryTypeKeyMemory, item.ActivityAt, item.DecayFactor, item.Importance, now)
		if item.TimeTier == "" {
			item.TimeTier = domain.ClassifyTimeTier(item.ActivityAt, now)
		}
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return keyMemoryLess(filtered[i], filtered[j])
	})
	if len(filtered) > input.TopK {
		filtered = filtered[:input.TopK]
	}
	return filtered, nil
}

func (s *Service) GetKeyMemory(ctx context.Context, userID, memoryKey string) (domain.KeyMemory, bool, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.KeyMemory{}, false, err
	}
	if s == nil || s.repo == nil || strings.TrimSpace(userID) == "" || strings.TrimSpace(memoryKey) == "" {
		return domain.KeyMemory{}, false, nil
	}
	return s.repo.GetKeyMemory(ctx, userID, strings.TrimSpace(memoryKey))
}

func (s *Service) GetKeyMemoryByID(ctx context.Context, userID, id string) (domain.KeyMemory, bool, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return domain.KeyMemory{}, false, err
	}
	if s == nil || s.repo == nil || strings.TrimSpace(userID) == "" || strings.TrimSpace(id) == "" {
		return domain.KeyMemory{}, false, nil
	}
	return s.repo.GetKeyMemoryByID(ctx, userID, strings.TrimSpace(id))
}

func (s *Service) UpsertKeyMemory(ctx context.Context, km domain.KeyMemory) (domain.KeyMemory, error) {
	return s.upsertKeyMemory(ctx, km, nil)
}

func (s *Service) upsertKeyMemory(ctx context.Context, km domain.KeyMemory, condition *domain.MemoryWriteCondition) (domain.KeyMemory, error) {
	if _, err := security.DataScope(ctx, km.UserID, km.WorkspaceID); err != nil {
		return domain.KeyMemory{}, err
	}
	km.WorkspaceID = security.WorkspaceID(ctx, km.UserID)
	km.Key = strings.TrimSpace(km.Key)
	existing, hasExisting, err := s.GetKeyMemory(ctx, km.UserID, km.Key)
	if err != nil {
		return domain.KeyMemory{}, err
	}
	if hasExisting {
		km.ID = existing.ID
		if km.CreatedAt.IsZero() {
			km.CreatedAt = existing.CreatedAt
		}
		if km.Metadata == nil {
			km.Metadata = cloneMetadata(existing.Metadata)
		} else if existing.Metadata != nil {
			merged := cloneMetadata(existing.Metadata)
			for key, value := range km.Metadata {
				merged[key] = value
			}
			km.Metadata = merged
		}
		if existingNodeID := stringMetadataValue(existing.Metadata, "node_id"); existingNodeID != "" {
			km.Metadata["node_id"] = existingNodeID
		}
		if km.Category == "" && condition == nil {
			km.Category = existing.Category
		}
		if km.Source == "" {
			km.Source = existing.Source
		}
		if km.ReviewStatus == "" {
			km.ReviewStatus = existing.ReviewStatus
		}
		if km.LastConfirmedAt == nil {
			km.LastConfirmedAt = existing.LastConfirmedAt
		}
	}
	if km.ID == "" {
		km.ID = uuid.NewString()
	}
	if km.Metadata == nil {
		km.Metadata = map[string]any{}
	}
	nodeID := stringMetadataValue(km.Metadata, "node_id")
	if nodeID == "" {
		nodeID = keyMemoryNodeID(ctx, km.UserID, km.Key)
		km.Metadata["node_id"] = nodeID
	}
	now := time.Now().UTC()
	if km.CreatedAt.IsZero() {
		km.CreatedAt = now
	}
	km.UpdatedAt = now
	km, err = s.applyKeyMemoryScore(ctx, km)
	if err != nil {
		return domain.KeyMemory{}, err
	}
	if condition == nil {
		err = s.repo.UpsertKeyMemory(ctx, km)
	} else if repo, ok := s.repo.(ports.ConditionalMemoryRepository); ok {
		err = repo.UpsertKeyMemoryConditional(ctx, km, *condition)
	} else {
		err = domain.ErrAccessDenied
	}
	if err != nil {
		return domain.KeyMemory{}, err
	}
	if stored, ok, err := s.GetKeyMemory(ctx, km.UserID, km.Key); err != nil {
		return domain.KeyMemory{}, err
	} else if ok {
		km = stored
		if storedNodeID := stringMetadataValue(km.Metadata, "node_id"); storedNodeID != "" {
			nodeID = storedNodeID
		}
	}
	if s.vectors != nil && !hasTransactionalMemoryOutbox(s.repo) {
		if err := s.vectors.Upsert(ctx, domain.VectorUpsertRequest{
			UserID:           km.UserID,
			NodeID:           nodeID,
			NodeType:         string(domain.MemoryTypeKeyMemory),
			EmbeddingVersion: 1,
			Content:          keyMemoryEmbeddingContent(km),
		}); err != nil {
			return domain.KeyMemory{}, err
		}
	}
	if !hasTransactionalMemoryOutbox(s.repo) {
		if err := s.syncKeyMemoryRelations(ctx, km, nodeID); err != nil {
			return domain.KeyMemory{}, err
		}
	}
	s.invalidateKeyMemoryCache(ctx, km.UserID)
	return km, nil
}

func (s *Service) DeleteKeyMemory(ctx context.Context, userID, memoryKey string) error {
	return s.deleteKeyMemory(ctx, userID, memoryKey, nil)
}

func (s *Service) deleteKeyMemory(ctx context.Context, userID, memoryKey string, condition *domain.MemoryWriteCondition) error {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return err
	}
	memoryKey = strings.TrimSpace(memoryKey)
	existing, hasExisting, err := s.GetKeyMemory(ctx, userID, memoryKey)
	if err != nil {
		return err
	}
	if condition == nil {
		err = s.repo.DeleteKeyMemory(ctx, userID, memoryKey)
	} else if repo, ok := s.repo.(ports.ConditionalMemoryRepository); ok {
		err = repo.DeleteKeyMemoryConditional(ctx, userID, memoryKey, *condition)
	} else {
		err = domain.ErrAccessDenied
	}
	if err != nil {
		return err
	}
	if s.vectors != nil && !hasTransactionalMemoryOutbox(s.repo) {
		nodeID := keyMemoryNodeID(ctx, userID, memoryKey)
		if hasExisting {
			if existingNodeID := stringMetadataValue(existing.Metadata, "node_id"); existingNodeID != "" {
				nodeID = existingNodeID
			}
		}
		if err := s.vectors.DeleteByNode(ctx, domain.VectorDeleteRequest{UserID: userID, NodeID: nodeID, RequireMissingNode: true}); err != nil {
			return err
		}
	}
	s.invalidateKeyMemoryCache(ctx, userID)
	return nil
}

func (s *Service) SyncKeyMemoryGraph(ctx context.Context, userID, memoryKey string) error {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return err
	}
	km, ok, err := s.GetKeyMemory(ctx, userID, strings.TrimSpace(memoryKey))
	if err != nil || !ok {
		return err
	}
	nodeID := stringMetadataValue(km.Metadata, "node_id")
	if nodeID == "" {
		nodeID = keyMemoryNodeID(ctx, userID, km.Key)
	}
	return s.syncKeyMemoryRelations(ctx, km, nodeID)
}

func hasTransactionalMemoryOutbox(repo ports.MemoryRepository) bool {
	typed, ok := repo.(interface{ HasTransactionalMemoryOutbox() bool })
	return ok && typed.HasTransactionalMemoryOutbox()
}

func (s *Service) applyKeyMemoryScore(ctx context.Context, km domain.KeyMemory) (domain.KeyMemory, error) {
	if km.Metadata == nil {
		km.Metadata = map[string]any{}
	}
	if km.LastConfirmedAt != nil && km.ActivityAt.IsZero() {
		km.ActivityAt = km.LastConfirmedAt.UTC()
	}
	input := ScoreInput{
		UserID:         km.UserID,
		MemoryType:     domain.MemoryTypeKeyMemory,
		Content:        keyMemoryEmbeddingContent(km),
		ConversationID: inferKeyMemoryConversationID(km),
		Metadata:       cloneMetadata(km.Metadata),
		ReferenceTime:  domain.DeriveActivityAt(km.ActivityAt, km.UpdatedAt, time.Now().UTC()),
	}
	score := MemoryScore{Importance: km.Importance, DecayFactor: km.DecayFactor, ActivityAt: input.ReferenceTime, Reason: "explicit_write"}
	if score.Importance == 0 {
		score.Importance = 1
	}
	if score.DecayFactor == 0 {
		score.DecayFactor = 1
	}
	if s.scorer != nil && stringMetadataValue(km.Metadata, "extraction_method") != "llm" {
		var err error
		score, err = s.scorer.Score(ctx, input)
		if err != nil {
			return domain.KeyMemory{}, err
		}
	}
	km.Importance = score.Importance
	km.DecayFactor = score.DecayFactor
	km.ActivityAt = domain.DeriveActivityAt(score.ActivityAt, km.UpdatedAt, time.Now().UTC())
	km.TimeTier = domain.ClassifyTimeTier(km.ActivityAt, time.Now().UTC())
	km.EffectiveScore = km.Importance * domain.TimeDecay(domain.MemoryTypeKeyMemory, km.ActivityAt, km.DecayFactor, km.Importance, time.Now().UTC())
	km.Metadata["activity_at"] = km.ActivityAt.UTC().Format(time.RFC3339)
	km.Metadata["time_tier"] = km.TimeTier
	km.Metadata["score_reason"] = score.Reason
	return km, nil
}

func (s *Service) InvalidateKeyMemoryCache(ctx context.Context, userID string) {
	s.invalidateKeyMemoryCache(ctx, userID)
}

func (s *Service) invalidateKeyMemoryCache(ctx context.Context, userID string) {
	if s.cache == nil {
		return
	}
	_ = s.cache.DeletePrefix(ctx, keyMemoryCachePrefix(ctx, userID))
}

func keyMemoryCacheKey(ctx context.Context, userID string, limit int) string {
	if limit < 0 {
		limit = 0
	}
	partition, _ := json.Marshal(security.Partitions(ctx))
	return keyMemoryCachePrefix(ctx, userID) + domain.AccessID("partitions", string(partition)) + ":limit:" + itoa(limit)
}

func keyMemoryCachePrefix(ctx context.Context, userID string) string {
	// Versioned scope encoding prevents old user-only entries and separator
	// collisions from crossing a workspace boundary during lookup/invalidation.
	return "memory:keymemories:v2:" + domain.AccessID("scope", userID+"\x00"+security.WorkspaceID(ctx, userID)) + ":"
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

func keyMemoryNodeID(ctx context.Context, userID, memoryKey string) string {
	return "node:keymemory:" + security.ScopedIdentity(ctx, userID) + ":" + memoryKey
}

func keyMemoryEmbeddingContent(km domain.KeyMemory) string {
	if km.Category != "" {
		return km.Key + ": " + km.Value + " (" + km.Category + ")"
	}
	return km.Key + ": " + km.Value
}

func (s *Service) syncDocumentEmbedding(ctx context.Context, doc domain.MemoryDocument) error {
	if s.vectors == nil || strings.TrimSpace(doc.UserID) == "" || strings.TrimSpace(doc.NodeID) == "" {
		return nil
	}
	embeddingVersion := 1
	embeddingModel := ""
	if s.generations != nil {
		generation, err := s.generations.ResolveGeneration(ctx, doc.UserID, doc.GenerationID)
		if err != nil {
			return err
		}
		if generation.EmbeddingVersion > 0 {
			embeddingVersion = generation.EmbeddingVersion
		}
		embeddingModel = generation.EmbeddingModel
	}
	return s.vectors.Upsert(ctx, domain.VectorUpsertRequest{
		UserID:           doc.UserID,
		GenerationID:     doc.GenerationID,
		NodeID:           doc.NodeID,
		NodeType:         string(doc.Type),
		EmbeddingVersion: embeddingVersion,
		EmbeddingModel:   embeddingModel,
		Content:          documentEmbeddingContent(doc),
	})
}

func documentEmbeddingContent(doc domain.MemoryDocument) string {
	content := strings.TrimSpace(doc.Content)
	if doc.Type != domain.MemoryTypeSummary {
		if content == "" {
			return doc.NodeID
		}
		return content
	}
	parts := make([]string, 0, 3)
	if content != "" {
		parts = append(parts, content)
	}
	if topic := stringMetadataValue(doc.Metadata, "topic_label"); topic != "" {
		parts = append(parts, "Topic: "+topic)
	}
	if keywords := stringSliceMetadataValue(doc.Metadata, "keywords"); len(keywords) > 0 {
		parts = append(parts, "Keywords: "+strings.Join(keywords, ", "))
	}
	if len(parts) == 0 {
		return doc.NodeID
	}
	return strings.Join(parts, "\n")
}

func (s *Service) keyMemoriesFromDocuments(ctx context.Context, userID string, docs []domain.MemoryDocument) ([]domain.KeyMemory, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	all, err := s.repo.ListKeyMemories(ctx, userID, max(len(docs)*4, 200))
	if err != nil {
		return nil, err
	}
	byID := make(map[string]domain.KeyMemory, len(all))
	for _, item := range all {
		byID[item.ID] = item
	}
	out := make([]domain.KeyMemory, 0, len(docs))
	for _, doc := range docs {
		item, ok := byID[doc.ID]
		if !ok {
			item = keyMemoryFromDocument(doc)
		}
		item.Importance = doc.Importance
		item.DecayFactor = doc.DecayFactor
		item.ActivityAt = doc.ActivityAt
		item.TimeTier = doc.TimeTier
		item.EffectiveScore = doc.EffectiveScore
		if item.Metadata == nil {
			item.Metadata = map[string]any{}
		}
		for key, value := range doc.Metadata {
			if _, exists := item.Metadata[key]; !exists {
				item.Metadata[key] = value
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func keyMemoryFromDocument(doc domain.MemoryDocument) domain.KeyMemory {
	item := domain.KeyMemory{
		ID:             doc.ID,
		UserID:         doc.UserID,
		WorkspaceID:    doc.WorkspaceID,
		Importance:     doc.Importance,
		DecayFactor:    doc.DecayFactor,
		ActivityAt:     doc.ActivityAt,
		TimeTier:       doc.TimeTier,
		EffectiveScore: doc.EffectiveScore,
		Metadata:       cloneMetadata(doc.Metadata),
		CreatedAt:      doc.CreatedAt,
		UpdatedAt:      doc.UpdatedAt,
	}
	if item.Metadata != nil {
		item.Key = stringMetadataValue(item.Metadata, "key")
		item.Value = stringMetadataValue(item.Metadata, "value")
		item.Category = stringMetadataValue(item.Metadata, "category")
		item.Source = stringMetadataValue(item.Metadata, "source")
		item.ReviewStatus = stringMetadataValue(item.Metadata, "review_status")
	}
	if item.Key == "" {
		parts := strings.SplitN(doc.Content, ": ", 2)
		item.Key = strings.TrimSpace(parts[0])
		if len(parts) > 1 {
			item.Value = strings.TrimSpace(parts[1])
		}
	}
	return item
}

func (s *Service) syncKeyMemoryRelations(ctx context.Context, km domain.KeyMemory, nodeID string) error {
	if s.repo == nil || km.UserID == "" || nodeID == "" {
		return nil
	}
	if err := s.repo.DeleteRelationsByTarget(ctx, km.UserID, nodeID, "promoted_to_key_memory"); err != nil {
		return err
	}

	sourceNodeIDs, err := s.resolveKeyMemorySourceNodes(ctx, km)
	if err != nil {
		return err
	}
	generationID, err := s.resolveGenerationID(ctx, km.UserID, stringMetadataValue(km.Metadata, "generation_id"))
	if err != nil {
		return err
	}
	for _, sourceNodeID := range sourceNodeIDs {
		if sourceNodeID == "" || sourceNodeID == nodeID {
			continue
		}
		if err := s.repo.LinkDocuments(ctx, domain.MemoryRelation{
			GenerationID: generationID,
			UserID:       km.UserID,
			SrcNodeID:    sourceNodeID,
			DstNodeID:    nodeID,
			RelationType: "promoted_to_key_memory",
			Weight:       1,
			Source:       firstNonEmptyString(km.Source, "key_memory"),
			Metadata: map[string]any{
				"key":             km.Key,
				"conversation_id": inferKeyMemoryConversationID(km),
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) resolveKeyMemorySourceNodes(ctx context.Context, km domain.KeyMemory) ([]string, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	generationID, err := s.resolveGenerationID(ctx, km.UserID, stringMetadataValue(km.Metadata, "generation_id"))
	if err != nil {
		return nil, err
	}
	addNode := func(nodeID string) {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			return
		}
		if _, ok := seen[nodeID]; ok {
			return
		}
		seen[nodeID] = struct{}{}
		out = append(out, nodeID)
	}

	if km.Metadata != nil {
		addNode(stringMetadataValue(km.Metadata, "source_node_id"))
		for _, nodeID := range stringSliceMetadataValue(km.Metadata, "source_node_ids") {
			addNode(nodeID)
		}
		s.addDocumentNodeByID(ctx, km.UserID, generationID, stringMetadataValue(km.Metadata, "source_summary_id"), domain.MemoryTypeSummary, addNode)
		s.addDocumentNodeByID(ctx, km.UserID, generationID, stringMetadataValue(km.Metadata, "source_fact_id"), domain.MemoryTypeFact, addNode)
		s.addDocumentNodeByID(ctx, km.UserID, generationID, stringMetadataValue(km.Metadata, "source_impression_id"), domain.MemoryTypeImpression, addNode)
	}

	if conversationID := inferKeyMemoryConversationID(km); conversationID != "" {
		docs, err := s.repo.ListDocumentsByConversationInGeneration(ctx, km.UserID, generationID, conversationID)
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			addNode(doc.NodeID)
		}
	}

	return out, nil
}

func (s *Service) addDocumentNodeByID(ctx context.Context, userID, generationID, documentID string, kind domain.MemoryType, add func(string)) {
	if s.repo == nil || strings.TrimSpace(documentID) == "" {
		return
	}
	doc, err := s.repo.GetDocumentInGeneration(ctx, userID, generationID, strings.TrimSpace(documentID), kind)
	if err != nil {
		return
	}
	add(doc.NodeID)
}

func stringMetadataValue(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	typed, ok := value.(string)
	if !ok {
		return ""
	}
	return typed
}

func stringSliceMetadataValue(metadata map[string]any, key string) []string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func matchesTemporalFilter(activityAt time.Time, timeTier domain.TimeTier, dateFrom, dateTo *time.Time, tiers []domain.TimeTier) bool {
	activityAt = domain.DeriveActivityAt(activityAt, time.Time{}, time.Time{})
	if timeTier == "" {
		timeTier = domain.ClassifyTimeTier(activityAt, time.Now().UTC())
	}
	if len(tiers) > 0 {
		matched := false
		for _, tier := range tiers {
			if tier == timeTier {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if dateFrom != nil && activityAt.Before(dateFrom.UTC()) {
		return false
	}
	if dateTo != nil && activityAt.After(dateTo.UTC()) {
		return false
	}
	return true
}

func rankKeyMemories(memories []domain.KeyMemory) {
	now := time.Now().UTC()
	for idx := range memories {
		memories[idx].Importance = domain.ClampImportance(memories[idx].Importance)
		memories[idx].DecayFactor = domain.ClampDecayFactor(memories[idx].DecayFactor)
		memories[idx].ActivityAt = domain.DeriveActivityAt(memories[idx].ActivityAt, memories[idx].UpdatedAt, memories[idx].CreatedAt)
		memories[idx].TimeTier = domain.ClassifyTimeTier(memories[idx].ActivityAt, now)
		memories[idx].EffectiveScore = memories[idx].Importance * domain.TimeDecay(domain.MemoryTypeKeyMemory, memories[idx].ActivityAt, memories[idx].DecayFactor, memories[idx].Importance, now)
	}
	sort.Slice(memories, func(i, j int) bool {
		return keyMemoryLess(memories[i], memories[j])
	})
}

func keyMemoryLess(left, right domain.KeyMemory) bool {
	if left.EffectiveScore == right.EffectiveScore {
		return left.ActivityAt.After(right.ActivityAt)
	}
	return left.EffectiveScore > right.EffectiveScore
}

func inferKeyMemoryConversationID(km domain.KeyMemory) string {
	if km.Metadata != nil {
		if conversationID := stringMetadataValue(km.Metadata, "conversation_id"); conversationID != "" {
			return conversationID
		}
	}
	if km.Key == "recent_conversation" {
		return strings.TrimSpace(km.Value)
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s *Service) hydrateRelatedDocuments(ctx context.Context, item *domain.MemoryDocument) {
	s.hydrateRelatedDocumentsBatch(ctx, []*domain.MemoryDocument{item})
}

func (s *Service) hydrateRelatedDocumentsBatch(ctx context.Context, items []*domain.MemoryDocument) {
	if s.repo == nil || len(items) == 0 {
		return
	}
	lineageCache := make(map[string]domain.FactLineage)
	type batchGroup struct {
		userID       string
		generationID string
		items        []*domain.MemoryDocument
	}
	type batchGroupKey struct {
		userID       string
		generationID string
	}
	groups := make(map[batchGroupKey]*batchGroup)
	groupOrder := make([]batchGroupKey, 0)
	for _, item := range items {
		if item == nil || item.NodeID == "" || item.UserID == "" {
			continue
		}
		key := batchGroupKey{userID: item.UserID, generationID: item.GenerationID}
		group, ok := groups[key]
		if !ok {
			group = &batchGroup{userID: item.UserID, generationID: item.GenerationID}
			groups[key] = group
			groupOrder = append(groupOrder, key)
		}
		group.items = append(group.items, item)
	}
	for _, key := range groupOrder {
		group := groups[key]
		if group == nil || len(group.items) == 0 {
			continue
		}

		seedNodeIDs := make([]string, 0, len(group.items))
		seedSet := make(map[string]struct{}, len(group.items))
		docByNodeID := make(map[string]domain.MemoryDocument, len(group.items))
		for _, item := range group.items {
			if item == nil || item.NodeID == "" {
				continue
			}
			seedNodeIDs = append(seedNodeIDs, item.NodeID)
			seedSet[item.NodeID] = struct{}{}
			docByNodeID[item.NodeID] = *item
		}
		if len(seedNodeIDs) == 0 {
			continue
		}
		seedNodeIDs = seedNodeIDs[:0]
		for nodeID := range seedSet {
			seedNodeIDs = append(seedNodeIDs, nodeID)
		}
		sort.Strings(seedNodeIDs)

		if evidences, err := s.repo.ListEvidencesByNodeIDs(ctx, group.userID, seedNodeIDs); err == nil && len(evidences) > 0 {
			evidenceByNodeID := make(map[string][]domain.MemoryEvidence, len(seedNodeIDs))
			for _, evidence := range evidences {
				if evidence.NodeID == "" {
					continue
				}
				evidenceByNodeID[evidence.NodeID] = append(evidenceByNodeID[evidence.NodeID], evidence)
			}
			for _, item := range group.items {
				if item == nil || item.NodeID == "" {
					continue
				}
				if nodeEvidences, ok := evidenceByNodeID[item.NodeID]; ok {
					item.Evidences = nodeEvidences
				}
			}
		}

		for _, item := range group.items {
			s.hydrateFactLineageWithCache(ctx, item, lineageCache)
		}

		type relatedInfo struct {
			weight       float64
			relationType string
		}
		relatedBySeed := make(map[string]map[string]relatedInfo, len(seedNodeIDs))
		relatedNodeIDs := make(map[string]struct{})
		precomputedSeeds := make(map[string]struct{})
		addRelated := func(seedNodeID, relatedNodeID string, info relatedInfo) {
			if seedNodeID == "" || relatedNodeID == "" || seedNodeID == relatedNodeID {
				return
			}
			items := relatedBySeed[seedNodeID]
			if items == nil {
				items = map[string]relatedInfo{}
				relatedBySeed[seedNodeID] = items
			}
			if current, ok := items[relatedNodeID]; !ok || info.weight > current.weight {
				items[relatedNodeID] = info
			}
			if _, ok := seedSet[relatedNodeID]; !ok {
				relatedNodeIDs[relatedNodeID] = struct{}{}
			}
		}
		for _, item := range group.items {
			if item != nil && item.Metadata["graph_timeout"] == true {
				precomputedSeeds[item.NodeID] = struct{}{}
			}
			if item == nil || item.NodeID == "" || len(item.Related) == 0 {
				continue
			}
			for _, relation := range item.Related {
				if relation.NodeID == "" {
					continue
				}
				precomputedSeeds[item.NodeID] = struct{}{}
				addRelated(item.NodeID, relation.NodeID, relatedInfo{
					weight:       relation.Weight,
					relationType: relation.RelationType,
				})
			}
		}

		relationSeedNodeIDs := make([]string, 0, len(seedNodeIDs))
		for _, nodeID := range seedNodeIDs {
			if _, ok := precomputedSeeds[nodeID]; ok {
				continue
			}
			relationSeedNodeIDs = append(relationSeedNodeIDs, nodeID)
		}
		if len(relationSeedNodeIDs) > 0 {
			if relations, err := s.repo.RecallRelations(ctx, domain.RelationQuery{UserID: group.userID, GenerationID: group.generationID, NodeIDs: relationSeedNodeIDs, MaxHops: 1}); err == nil {
				for _, relation := range relations {
					if isTemporalRelationTypeForHydration(relation.RelationType) {
						continue
					}
					if _, ok := seedSet[relation.SrcNodeID]; ok {
						if _, precomputed := precomputedSeeds[relation.SrcNodeID]; !precomputed {
							addRelated(relation.SrcNodeID, relation.DstNodeID, relatedInfo{weight: relation.Weight, relationType: relation.RelationType})
						}
					}
					if _, ok := seedSet[relation.DstNodeID]; ok {
						if _, precomputed := precomputedSeeds[relation.DstNodeID]; !precomputed {
							addRelated(relation.DstNodeID, relation.SrcNodeID, relatedInfo{weight: relation.Weight, relationType: relation.RelationType})
						}
					}
				}
			}
		}
		if len(relatedBySeed) == 0 {
			continue
		}

		if len(relatedNodeIDs) > 0 {
			lookupIDs := make([]string, 0, len(relatedNodeIDs))
			for nodeID := range relatedNodeIDs {
				lookupIDs = append(lookupIDs, nodeID)
			}
			if documents, err := s.repo.ListDocumentsByNodeIDsInGeneration(ctx, group.userID, group.generationID, lookupIDs); err == nil && len(documents) > 0 {
				for _, document := range documents {
					if document.NodeID == "" {
						continue
					}
					docByNodeID[document.NodeID] = document
				}
			}
		}

		for _, item := range group.items {
			if item == nil || item.NodeID == "" {
				continue
			}
			items := relatedBySeed[item.NodeID]
			if len(items) == 0 {
				item.Related = nil
				continue
			}
			related := make([]domain.RelatedMemory, 0, len(items))
			for nodeID, info := range items {
				document, ok := docByNodeID[nodeID]
				if !ok || document.ID == "" {
					continue
				}
				related = append(related, domain.RelatedMemory{
					NodeID:       document.NodeID,
					DocumentID:   document.ID,
					DocumentType: string(document.Type),
					RelationType: info.relationType,
					Weight:       info.weight,
				})
			}
			sort.Slice(related, func(i, j int) bool {
				if related[i].Weight == related[j].Weight {
					return related[i].DocumentID < related[j].DocumentID
				}
				return related[i].Weight > related[j].Weight
			})
			item.Related = related
		}
	}
}

func (s *Service) hydrateFactLineageWithCache(ctx context.Context, item *domain.MemoryDocument, cache map[string]domain.FactLineage) {
	if item == nil || item.Type != domain.MemoryTypeFact {
		return
	}
	if s.repo == nil {
		return
	}
	cacheKey := ""
	lineageGroupID := ""
	if item.Lineage != nil {
		lineageGroupID = strings.TrimSpace(item.Lineage.LineageGroupID)
	}
	if lineageGroupID == "" {
		lineageGroupID = strings.TrimSpace(stringMetadataValue(item.Metadata, "lineage_group_id"))
	}
	if lineageGroupID != "" {
		cacheKey = "group:" + item.GenerationID + ":" + lineageGroupID
	}

	var lineage domain.FactLineage
	if cache != nil && cacheKey != "" {
		if cached, ok := cache[cacheKey]; ok {
			lineage = cached
		}
	}
	if len(lineage.Entries) == 0 {
		var err error
		if lineageGroupID != "" {
			lineage, err = s.repo.ListFactLineageByKey(ctx, item.UserID, item.GenerationID, lineageGroupID)
		} else {
			cacheKey = "fact:" + item.GenerationID + ":" + item.ID
			if cache != nil {
				if cached, ok := cache[cacheKey]; ok {
					lineage = cached
				}
			}
			if len(lineage.Entries) == 0 {
				lineage, err = s.repo.ListFactLineage(ctx, item.UserID, item.GenerationID, item.ID)
			}
		}
		if err != nil || len(lineage.Entries) == 0 {
			return
		}
		if cache != nil && cacheKey != "" {
			cache[cacheKey] = lineage
		}
	}
	aliases := make([]string, 0, len(lineage.Entries))
	for _, entry := range lineage.Entries {
		if entry.NodeID != "" && entry.NodeID != item.NodeID && (entry.IsSuperseded || entry.SupersededByID != "") {
			aliases = appendUniqueString(aliases, entry.NodeID)
		}
	}
	meta := &domain.MemoryLineageMeta{
		FactKey:                lineage.FactKey,
		LineageGroupID:         lineage.LineageGroupID,
		RevisionDepth:          lineage.RevisionDepth,
		LineagePath:            append([]string(nil), lineage.LineagePath...),
		SupersededAliasNodeIDs: aliases,
		CurrentHeadID:          lineage.CurrentHeadID,
		GenerationID:           item.GenerationID,
	}
	for _, entry := range lineage.Entries {
		if entry.FactID == item.ID {
			meta.RevisionSeq = entry.RevisionSeq
			break
		}
	}
	item.Lineage = meta
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	item.Metadata["lineage_path"] = meta.LineagePath
	item.Metadata["revision_depth"] = meta.RevisionDepth
	item.Metadata["current_head_id"] = meta.CurrentHeadID
	if len(meta.SupersededAliasNodeIDs) > 0 {
		item.Metadata["superseded_alias_node_ids"] = meta.SupersededAliasNodeIDs
	}
}

func (s *Service) resolveGenerationID(ctx context.Context, userID, generationID string) (string, error) {
	if generationID != "" || userID == "" {
		return generationID, nil
	}
	if s.generations == nil {
		return memorygenservice.CurrentGenerationID(security.WorkspaceID(ctx, userID)), nil
	}
	generation, err := s.generations.GetActiveGeneration(ctx, userID)
	if err != nil {
		return "", err
	}
	return generation.GenerationID, nil
}
