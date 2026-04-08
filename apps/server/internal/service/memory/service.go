package memory

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"memstation/apps/server/internal/cache"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
)

type Service struct {
	engine      ports.MemoryEngine
	repo        ports.MemoryRepository
	cache       *cache.JSONStore
	vectors     ports.VectorStore
	scorer      *Scorer
	generations *memorygenservice.Service
}

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
	return s.engine.Search(ctx, input)
}

func (s *Service) GetDocument(ctx context.Context, userID, id string, kind domain.MemoryType) (domain.MemoryDocument, error) {
	return s.GetDocumentInGeneration(ctx, userID, "", id, kind)
}

func (s *Service) GetDocumentInGeneration(ctx context.Context, userID, generationID, id string, kind domain.MemoryType) (domain.MemoryDocument, error) {
	resolvedGenerationID, err := s.resolveGenerationID(ctx, userID, generationID)
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	item, err := s.repo.GetDocumentInGeneration(ctx, userID, resolvedGenerationID, id, kind)
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	s.hydrateRelatedDocuments(ctx, &item)
	return item, nil
}

func (s *Service) ListDocuments(ctx context.Context, userID string, kinds []domain.MemoryType, limit int) ([]domain.MemoryDocument, error) {
	return s.ListDocumentsInGeneration(ctx, userID, "", kinds, limit)
}

func (s *Service) ListDocumentsInGeneration(ctx context.Context, userID, generationID string, kinds []domain.MemoryType, limit int) ([]domain.MemoryDocument, error) {
	resolvedGenerationID, err := s.resolveGenerationID(ctx, userID, generationID)
	if err != nil {
		return nil, err
	}
	return s.repo.ListDocumentsInGeneration(ctx, userID, resolvedGenerationID, kinds, limit)
}

func (s *Service) ListDocumentsByConversationInGeneration(ctx context.Context, userID, generationID, conversationID string) ([]domain.MemoryDocument, error) {
	resolvedGenerationID, err := s.resolveGenerationID(ctx, userID, generationID)
	if err != nil {
		return nil, err
	}
	return s.repo.ListDocumentsByConversationInGeneration(ctx, userID, resolvedGenerationID, conversationID)
}

func (s *Service) UpdateDocument(ctx context.Context, userID, id string, kind domain.MemoryType, content string) (domain.MemoryDocument, error) {
	return s.repo.UpdateDocument(ctx, userID, id, kind, content)
}

func (s *Service) ListKeyMemories(ctx context.Context, userID string, limit int) ([]domain.KeyMemory, error) {
	if s.cache != nil {
		var cached []domain.KeyMemory
		ok, err := s.cache.Get(ctx, keyMemoryCacheKey(userID, limit), &cached)
		if err == nil && ok {
			return cached, nil
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
		_ = s.cache.Set(ctx, keyMemoryCacheKey(userID, limit), memories)
	}
	return memories, nil
}

func (s *Service) SearchKeyMemories(ctx context.Context, input domain.KeyMemoryQuery) ([]domain.KeyMemory, error) {
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

func (s *Service) UpsertKeyMemory(ctx context.Context, km domain.KeyMemory) (domain.KeyMemory, error) {
	if km.ID == "" {
		km.ID = uuid.NewString()
	}
	if km.Metadata == nil {
		km.Metadata = map[string]any{}
	}
	nodeID := stringMetadataValue(km.Metadata, "node_id")
	if nodeID == "" {
		nodeID = keyMemoryNodeID(km.UserID, km.Key)
		km.Metadata["node_id"] = nodeID
	}
	now := time.Now().UTC()
	if km.CreatedAt.IsZero() {
		km.CreatedAt = now
	}
	km.UpdatedAt = now
	km = s.applyKeyMemoryScore(ctx, km)
	if err := s.repo.UpsertKeyMemory(ctx, km); err != nil {
		return domain.KeyMemory{}, err
	}
	if s.vectors != nil {
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
	if err := s.syncKeyMemoryRelations(ctx, km, nodeID); err != nil {
		return domain.KeyMemory{}, err
	}
	s.invalidateKeyMemoryCache(ctx, km.UserID)
	return km, nil
}

func (s *Service) DeleteKeyMemory(ctx context.Context, userID, memoryKey string) error {
	if err := s.repo.DeleteKeyMemory(ctx, userID, memoryKey); err != nil {
		return err
	}
	if s.vectors != nil {
		if err := s.vectors.DeleteByNode(ctx, userID, keyMemoryNodeID(userID, memoryKey)); err != nil {
			return err
		}
	}
	s.invalidateKeyMemoryCache(ctx, userID)
	return nil
}

func (s *Service) applyKeyMemoryScore(ctx context.Context, km domain.KeyMemory) domain.KeyMemory {
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
	score := fallbackScore(input)
	if s.scorer != nil {
		score = s.scorer.Score(ctx, input)
	}
	km.Importance = score.Importance
	km.DecayFactor = score.DecayFactor
	km.ActivityAt = domain.DeriveActivityAt(score.ActivityAt, km.UpdatedAt, time.Now().UTC())
	km.TimeTier = domain.ClassifyTimeTier(km.ActivityAt, time.Now().UTC())
	km.EffectiveScore = km.Importance * domain.TimeDecay(domain.MemoryTypeKeyMemory, km.ActivityAt, km.DecayFactor, km.Importance, time.Now().UTC())
	km.Metadata["activity_at"] = km.ActivityAt.UTC().Format(time.RFC3339)
	km.Metadata["time_tier"] = km.TimeTier
	km.Metadata["score_reason"] = score.Reason
	return km
}

func (s *Service) InvalidateKeyMemoryCache(ctx context.Context, userID string) {
	s.invalidateKeyMemoryCache(ctx, userID)
}

func (s *Service) invalidateKeyMemoryCache(ctx context.Context, userID string) {
	if s.cache == nil {
		return
	}
	_ = s.cache.DeletePrefix(ctx, keyMemoryCachePrefix(userID))
}

func keyMemoryCacheKey(userID string, limit int) string {
	if limit < 0 {
		limit = 0
	}
	return keyMemoryCachePrefix(userID) + "limit:" + itoa(limit)
}

func keyMemoryCachePrefix(userID string) string {
	return "memory:keymemories:user:" + userID + ":"
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

func keyMemoryNodeID(userID, memoryKey string) string {
	return "node:keymemory:" + userID + ":" + memoryKey
}

func keyMemoryEmbeddingContent(km domain.KeyMemory) string {
	if km.Category != "" {
		return km.Key + ": " + km.Value + " (" + km.Category + ")"
	}
	return km.Key + ": " + km.Value
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
	if s.repo == nil || item == nil || item.NodeID == "" || item.UserID == "" {
		return
	}
	relations, err := s.repo.ListRelations(ctx, item.UserID, item.GenerationID, []string{item.NodeID}, 1)
	if err != nil || len(relations) == 0 {
		return
	}

	relatedNodeIDs := make([]string, 0, len(relations))
	weights := map[string]float64{}
	for _, relation := range relations {
		otherNodeID := relation.SrcNodeID
		if otherNodeID == item.NodeID {
			otherNodeID = relation.DstNodeID
		}
		if otherNodeID == "" || otherNodeID == item.NodeID {
			continue
		}
		relatedNodeIDs = append(relatedNodeIDs, otherNodeID)
		if relation.Weight > weights[otherNodeID] {
			weights[otherNodeID] = relation.Weight
		}
	}
	if len(relatedNodeIDs) == 0 {
		return
	}

	documents, err := s.repo.ListDocumentsByNodeIDsInGeneration(ctx, item.UserID, item.GenerationID, relatedNodeIDs)
	if err != nil || len(documents) == 0 {
		return
	}

	related := make([]domain.RelatedMemory, 0, len(documents))
	seen := map[string]struct{}{}
	for _, document := range documents {
		if _, ok := seen[document.ID]; ok {
			continue
		}
		seen[document.ID] = struct{}{}
		related = append(related, domain.RelatedMemory{
			ID:     document.ID,
			Type:   string(document.Type),
			Weight: weights[document.NodeID],
		})
	}
	sort.Slice(related, func(i, j int) bool {
		if related[i].Weight == related[j].Weight {
			return related[i].ID < related[j].ID
		}
		return related[i].Weight > related[j].Weight
	})
	item.Related = related
}

func (s *Service) resolveGenerationID(ctx context.Context, userID, generationID string) (string, error) {
	if generationID != "" || userID == "" {
		return generationID, nil
	}
	if s.generations == nil {
		return memorygenservice.CurrentGenerationID(userID), nil
	}
	generation, err := s.generations.GetActiveGeneration(ctx, userID)
	if err != nil {
		return "", err
	}
	return generation.GenerationID, nil
}
