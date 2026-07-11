package sql

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"

	"memstation/apps/server/internal/domain"
	conversationservice "memstation/apps/server/internal/service/conversation"
)

type InMemoryConversationRepository struct {
	mu            sync.RWMutex
	conversations map[string]domain.ConversationRecord
	segments      map[string][]domain.ConversationSegment
}

func NewInMemoryConversationRepository() *InMemoryConversationRepository {
	return &InMemoryConversationRepository{
		conversations: map[string]domain.ConversationRecord{},
		segments:      map[string][]domain.ConversationSegment{},
	}
}

func (r *InMemoryConversationRepository) PersistExchange(_ context.Context, conversation domain.ConversationRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.conversations[key(conversation.UserID, conversation.ConversationID)]
	if ok {
		if conversation.CreatedAt.IsZero() {
			conversation.CreatedAt = existing.CreatedAt
		}
		if conversation.FinalizeStatus == "" {
			conversation.FinalizeStatus = existing.FinalizeStatus
		}
		if conversation.SummaryStatus == "" {
			conversation.SummaryStatus = existing.SummaryStatus
		}
		if conversation.AnalysisStatus == "" {
			conversation.AnalysisStatus = existing.AnalysisStatus
		}
		if conversation.Metadata == nil {
			conversation.Metadata = map[string]any{}
		}
		for metaKey, metaValue := range existing.Metadata {
			if _, exists := conversation.Metadata[metaKey]; !exists {
				conversation.Metadata[metaKey] = metaValue
			}
		}
	}
	if conversation.Metadata == nil {
		conversation.Metadata = map[string]any{}
	}
	conversation.Messages = conversationservice.PrepareMessagesForStorage(conversation.Messages)
	plan := planConversationMessages(existing.Messages, conversation.Messages)
	conversation.Messages = plan.Stored
	conversation.UpdatedAt = time.Now().UTC()
	if conversation.CreatedAt.IsZero() {
		conversation.CreatedAt = conversation.UpdatedAt
	}
	if conversation.FinalizeStatus == "" {
		conversation.FinalizeStatus = domain.FinalizeStatusActive
	}
	if conversation.SummaryStatus == "" {
		conversation.SummaryStatus = domain.ProcessingStatusPending
	}
	if conversation.AnalysisStatus == "" {
		conversation.AnalysisStatus = domain.ProcessingStatusPending
	}
	conversation.Metadata["message_count"] = len(conversation.Messages)
	if len(conversation.Messages) > 0 {
		conversation.Metadata["last_sequence_num"] = len(conversation.Messages) - 1
	}
	conversation.Metadata["message_storage_mode"] = plan.Mode
	conversation.Metadata["history_rewrite_detected"] = plan.Mode == "rewrite"
	conversation.Metadata["history_partial_snapshot"] = plan.Mode == "partial_snapshot"
	if ok {
		conversation.Segments = filterConversationSegments(
			conversation.UserID,
			conversationSegmentGeneration(conversation.UserID, conversation.Metadata),
			r.segments[key(conversation.UserID, conversation.ConversationID)],
		)
	}
	r.conversations[key(conversation.UserID, conversation.ConversationID)] = conversation
	return nil
}

func (r *InMemoryConversationRepository) GetConversation(_ context.Context, userID, conversationID string) (domain.ConversationRecord, error) {
	return r.GetConversationInGeneration(context.Background(), userID, conversationID, "")
}

func (r *InMemoryConversationRepository) GetConversationInGeneration(_ context.Context, userID, conversationID, generationID string) (domain.ConversationRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conversation, ok := r.conversations[key(userID, conversationID)]
	if !ok {
		return domain.ConversationRecord{}, errors.New("conversation not found")
	}
	resolvedGenerationID := strings.TrimSpace(generationID)
	if resolvedGenerationID == "" {
		resolvedGenerationID = conversationSegmentGeneration(userID, conversation.Metadata)
	}
	conversation.Segments = filterConversationSegments(
		userID,
		resolvedGenerationID,
		r.segments[key(userID, conversationID)],
	)
	conversation.Metadata = cloneMetadata(conversation.Metadata)
	if conversation.Metadata == nil {
		conversation.Metadata = map[string]any{}
	}
	conversation.Metadata["memory_generation_id"] = resolvedGenerationID
	return conversation, nil
}

func (r *InMemoryConversationRepository) ListConversations(_ context.Context, userID string) ([]domain.ConversationRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.ConversationRecord, 0)
	for _, conversation := range r.conversations {
		if conversation.UserID == userID {
			out = append(out, conversation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (r *InMemoryConversationRepository) ListTimedOutConversations(_ context.Context, before time.Time, limit int) ([]domain.ConversationRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.ConversationRecord, 0)
	for _, conversation := range r.conversations {
		if conversation.FinalizeStatus == domain.FinalizeStatusActive && conversation.UpdatedAt.Before(before) {
			out = append(out, conversation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.Before(out[j].UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *InMemoryConversationRepository) ReplaceSegments(_ context.Context, userID, conversationID string, segments []domain.ConversationSegment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	targetGenerationID := currentGenerationID(userID)
	if len(segments) > 0 && strings.TrimSpace(segments[0].GenerationID) != "" {
		targetGenerationID = strings.TrimSpace(segments[0].GenerationID)
	} else if conversation, ok := r.conversations[key(userID, conversationID)]; ok {
		targetGenerationID = conversationSegmentGeneration(userID, conversation.Metadata)
	}

	next := make([]domain.ConversationSegment, 0, len(r.segments[key(userID, conversationID)])+len(segments))
	for _, existing := range r.segments[key(userID, conversationID)] {
		if matchesConversationSegmentGeneration(userID, targetGenerationID, existing.GenerationID) {
			continue
		}
		next = append(next, existing)
	}
	for idx := range segments {
		if strings.TrimSpace(segments[idx].GenerationID) == "" {
			segments[idx].GenerationID = targetGenerationID
		}
		if segments[idx].Metadata == nil {
			segments[idx].Metadata = map[string]any{}
		}
		segments[idx].Metadata["generation_id"] = segments[idx].GenerationID
	}
	next = append(next, segments...)
	r.segments[key(userID, conversationID)] = next
	if conversation, ok := r.conversations[key(userID, conversationID)]; ok {
		if conversation.Metadata == nil {
			conversation.Metadata = map[string]any{}
		}
		conversation.Metadata["memory_generation_id"] = targetGenerationID
		conversation.Segments = filterConversationSegments(userID, targetGenerationID, next)
		r.conversations[key(userID, conversationID)] = conversation
	}
	return nil
}

func (r *InMemoryConversationRepository) FinalizeConversation(ctx context.Context, input domain.ConversationFinalizeInput) error {
	conversation, err := r.GetConversation(ctx, input.UserID, input.ConversationID)
	if err != nil {
		return err
	}
	conversation.FinalizeStatus = input.Status
	if conversation.Metadata == nil {
		conversation.Metadata = map[string]any{}
	}
	conversation.Metadata["finalize_reason"] = input.Reason
	return r.PersistExchange(ctx, conversation)
}

func (r *InMemoryConversationRepository) UpdateConversationState(ctx context.Context, input domain.ConversationStateUpdate) error {
	conversation, err := r.GetConversation(ctx, input.UserID, input.ConversationID)
	if err != nil {
		return err
	}
	if input.FinalizeStatus != nil {
		conversation.FinalizeStatus = *input.FinalizeStatus
	}
	if input.SummaryStatus != nil {
		conversation.SummaryStatus = *input.SummaryStatus
	}
	if input.AnalysisStatus != nil {
		conversation.AnalysisStatus = *input.AnalysisStatus
	}
	if conversation.Metadata == nil {
		conversation.Metadata = map[string]any{}
	}
	for key, value := range input.Metadata {
		conversation.Metadata[key] = value
	}
	if !input.UpdatedAt.IsZero() {
		conversation.UpdatedAt = input.UpdatedAt
	}
	return r.PersistExchange(ctx, conversation)
}

type InMemoryMemoryRepository struct {
	mu          sync.RWMutex
	documents   map[string]domain.MemoryDocument
	keyMemories map[string]domain.KeyMemory
	relations   []domain.MemoryRelation
	evidences   map[string][]domain.MemoryEvidence
}

func NewInMemoryMemoryRepository() *InMemoryMemoryRepository {
	return &InMemoryMemoryRepository{
		documents:   map[string]domain.MemoryDocument{},
		keyMemories: map[string]domain.KeyMemory{},
		evidences:   map[string][]domain.MemoryEvidence{},
	}
}

func (r *InMemoryMemoryRepository) Search(_ context.Context, input domain.SearchInput) (domain.SearchResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	query := strings.ToLower(strings.TrimSpace(input.Query))
	docs := make([]domain.MemoryDocument, 0)
	now := time.Now().UTC()
	for _, doc := range r.documents {
		if doc.UserID != input.UserID {
			continue
		}
		if !matchesDocumentGeneration(input.UserID, input.GenerationID, doc) {
			continue
		}
		if len(input.MemoryTypes) > 0 {
			matchedType := false
			for _, memoryType := range input.MemoryTypes {
				if doc.Type == memoryType {
					matchedType = true
					break
				}
			}
			if !matchedType {
				continue
			}
		}
		if !matchesTemporalInput(doc, input) {
			continue
		}
		score := 0.25
		if query != "" && strings.Contains(strings.ToLower(doc.Content), query) {
			score = 0.92
		}
		doc.Score = score
		doc.Importance = domain.ClampImportance(doc.Importance)
		doc.DecayFactor = domain.ClampDecayFactor(doc.DecayFactor)
		doc.ActivityAt = domain.DeriveActivityAt(doc.ActivityAt, doc.UpdatedAt, doc.CreatedAt)
		doc.TimeTier = domain.ClassifyTimeTier(doc.ActivityAt, now)
		doc.EffectiveScore = score * doc.Importance * domain.TimeDecay(doc.Type, doc.ActivityAt, doc.DecayFactor, doc.Importance, now) * domain.StalePenalty(doc, now)
		if doc.Score >= input.MinScore {
			docs = append(docs, doc)
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].EffectiveScore > docs[j].EffectiveScore })
	if input.TopN > 0 && len(docs) > input.TopN {
		docs = docs[:input.TopN]
	}
	return domain.SearchResult{Memories: docs, Total: len(docs), QueryEmbeddingTokens: len(query) / 4}, nil
}

func (r *InMemoryMemoryRepository) StoreDocument(_ context.Context, doc domain.MemoryDocument) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	if doc.Type != domain.MemoryTypeKeyMemory && strings.TrimSpace(doc.GenerationID) == "" {
		doc.GenerationID = currentGenerationID(doc.UserID)
	}
	hadExplicitID := strings.TrimSpace(doc.ID) != ""
	hadExplicitNodeID := strings.TrimSpace(doc.NodeID) != ""
	if doc.ID == "" {
		doc.ID = uuid.NewString()
	}
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = now
	}
	if doc.UpdatedAt.IsZero() {
		doc.UpdatedAt = now
	}
	if doc.Importance == 0 {
		doc.Importance = 1
	}
	if doc.DecayFactor == 0 {
		doc.DecayFactor = 1
	}
	doc.Importance = domain.ClampImportance(doc.Importance)
	doc.DecayFactor = domain.ClampDecayFactor(doc.DecayFactor)
	doc.ActivityAt = domain.DeriveActivityAt(doc.ActivityAt, doc.UpdatedAt, doc.CreatedAt)
	doc.TimeTier = domain.ClassifyTimeTier(doc.ActivityAt, now)
	doc.Metadata = cloneMetadata(doc.Metadata)
	if doc.Type == domain.MemoryTypeFact {
		doc = ensureFactLineageMetadata(doc)
		if groupID := factLineageGroupIDFromMetadata(doc.Metadata); groupID != "" {
			if !hadExplicitID {
				doc.ID = lineageRevisionID(doc)
			}
			if !hadExplicitNodeID {
				doc.NodeID = "node:" + doc.ID
			}
		}
		if seq := factLineageRevisionSeqFromMetadata(doc.Metadata); seq <= 0 {
			doc.Metadata["revision_seq"] = r.nextFactRevisionSeqLocked(doc)
		}
	}
	if doc.NodeID == "" {
		doc.NodeID = defaultMemoryNodeID(doc.UserID, doc.Type, doc.ID)
	}
	documentKey := key(doc.UserID, doc.ID)
	existing, existed := r.documents[documentKey]
	if existed {
		if shouldKeepExistingSnapshot(existing.Metadata, doc.Metadata) {
			return nil
		}
		doc = preserveFactSupersessionMetadata(doc, existing)
	}
	if doc.Type == domain.MemoryTypeFact {
		supersessionStateSpecified := metadataContainsKey(doc.Metadata, "superseded_by_id") || metadataContainsKey(doc.Metadata, "is_superseded")
		if !supersessionStateSpecified && !boolMetadataValue(doc.Metadata, "is_superseded") && stringMetadataValue(doc.Metadata, "superseded_by_id") == "" && len(supersededFactIDsFromMetadata(doc.Metadata)) == 0 {
			if heads := r.currentFactLineageHeadIDsLocked(doc); len(heads) > 0 {
				doc.Metadata["supersedes_fact_ids"] = heads
			}
		}
	}
	doc.Metadata["activity_at"] = doc.ActivityAt.Format(time.RFC3339)
	doc.Metadata["time_tier"] = doc.TimeTier
	if doc.GenerationID != "" {
		doc.Metadata["generation_id"] = doc.GenerationID
	}
	r.documents[documentKey] = doc
	if doc.Type == domain.MemoryTypeFact {
		if err := r.syncFactSupersedesLocked(doc); err != nil {
			if existed {
				r.documents[documentKey] = existing
			} else {
				delete(r.documents, documentKey)
			}
			return err
		}
	}
	r.syncTemporalRelationsLocked(doc.UserID, doc.GenerationID, doc.NodeID, doc.ActivityAt)
	return nil
}

func (r *InMemoryMemoryRepository) DeleteDocument(_ context.Context, userID, id string, kind domain.MemoryType) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	doc, ok := r.documents[key(userID, id)]
	if !ok || doc.Type != kind {
		return nil
	}
	delete(r.documents, key(userID, id))

	filtered := r.relations[:0]
	for _, relation := range r.relations {
		if relation.UserID == userID && (relation.SrcNodeID == doc.NodeID || relation.DstNodeID == doc.NodeID) {
			continue
		}
		filtered = append(filtered, relation)
	}
	r.relations = filtered
	delete(r.evidences, key(userID, doc.NodeID))
	return nil
}

func (r *InMemoryMemoryRepository) DeleteSegmentsByConversation(_ context.Context, userID, conversationID string, keepIDs []string) ([]string, error) {
	return r.DeleteSegmentsByConversationInGeneration(context.Background(), userID, "", conversationID, keepIDs)
}

func (r *InMemoryMemoryRepository) DeleteSegmentsByConversationInGeneration(_ context.Context, userID, generationID, conversationID string, keepIDs []string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if userID == "" || conversationID == "" {
		return nil, nil
	}

	keep := make(map[string]struct{}, len(keepIDs))
	for _, id := range keepIDs {
		keep[id] = struct{}{}
	}

	deletedNodeIDs := make([]string, 0)
	deletedNodes := make(map[string]struct{})
	for docKey, doc := range r.documents {
		if doc.UserID != userID || doc.Type != domain.MemoryTypeSegment || doc.ConversationID != conversationID {
			continue
		}
		if !matchesDocumentGeneration(userID, generationID, doc) {
			continue
		}
		if _, ok := keep[doc.ID]; ok {
			continue
		}
		delete(r.documents, docKey)
		if doc.NodeID != "" {
			deletedNodeIDs = append(deletedNodeIDs, doc.NodeID)
			deletedNodes[doc.NodeID] = struct{}{}
		}
	}

	if len(deletedNodes) == 0 {
		return nil, nil
	}

	filtered := r.relations[:0]
	for _, relation := range r.relations {
		if relation.UserID == userID {
			if _, ok := deletedNodes[relation.SrcNodeID]; ok {
				continue
			}
			if _, ok := deletedNodes[relation.DstNodeID]; ok {
				continue
			}
		}
		filtered = append(filtered, relation)
	}
	r.relations = filtered
	sort.Strings(deletedNodeIDs)
	return deletedNodeIDs, nil
}

func (r *InMemoryMemoryRepository) GetDocument(_ context.Context, userID, id string, kind domain.MemoryType) (domain.MemoryDocument, error) {
	return r.GetDocumentInGeneration(context.Background(), userID, "", id, kind)
}

func (r *InMemoryMemoryRepository) GetDocumentInGeneration(_ context.Context, userID, generationID, id string, kind domain.MemoryType) (domain.MemoryDocument, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	doc, ok := r.documents[key(userID, id)]
	if !ok || doc.Type != kind || !matchesDocumentGeneration(userID, generationID, doc) {
		return domain.MemoryDocument{}, errors.New("memory document not found")
	}
	return attachDocumentRuntimeMetadata(doc), nil
}

func (r *InMemoryMemoryRepository) ListDocuments(_ context.Context, userID string, memoryTypes []domain.MemoryType, limit int) ([]domain.MemoryDocument, error) {
	return r.ListDocumentsInGeneration(context.Background(), userID, "", memoryTypes, limit)
}

func (r *InMemoryMemoryRepository) ListDocumentsInGeneration(_ context.Context, userID, generationID string, memoryTypes []domain.MemoryType, limit int) ([]domain.MemoryDocument, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.MemoryDocument, 0)
	for _, doc := range r.documents {
		if doc.UserID != userID {
			continue
		}
		if !matchesDocumentGeneration(userID, generationID, doc) {
			continue
		}
		if !matchesMemoryType(doc.Type, memoryTypes) {
			continue
		}
		out = append(out, attachDocumentRuntimeMetadata(doc))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *InMemoryMemoryRepository) ListDocumentsByNodeIDs(_ context.Context, userID string, nodeIDs []string) ([]domain.MemoryDocument, error) {
	return r.ListDocumentsByNodeIDsInGeneration(context.Background(), userID, "", nodeIDs)
}

func (r *InMemoryMemoryRepository) ListDocumentsByNodeIDsInGeneration(_ context.Context, userID, generationID string, nodeIDs []string) ([]domain.MemoryDocument, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	allowed := map[string]struct{}{}
	for _, nodeID := range nodeIDs {
		allowed[nodeID] = struct{}{}
	}
	out := make([]domain.MemoryDocument, 0, len(nodeIDs))
	for _, doc := range r.documents {
		if doc.UserID != userID {
			continue
		}
		if !matchesDocumentGeneration(userID, generationID, doc) {
			continue
		}
		if _, ok := allowed[doc.NodeID]; ok {
			out = append(out, attachDocumentRuntimeMetadata(doc))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (r *InMemoryMemoryRepository) SearchKeywordMatches(_ context.Context, userID, generationID, query string, limit int) ([]domain.KeywordMatch, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(normalizeKeywordTerms(query)) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	matches := make([]domain.KeywordMatch, 0)
	for _, doc := range r.documents {
		if doc.UserID != userID || (doc.Type != domain.MemoryTypeSummary && doc.Type != domain.MemoryTypeFact) {
			continue
		}
		if !matchesDocumentGeneration(userID, generationID, doc) {
			continue
		}
		keywords := stringSliceMetadataValue(doc.Metadata, "keywords")
		content := doc.Content
		if doc.Type == domain.MemoryTypeFact {
			keywords = append(keywords, factKeyFromMetadata(doc.Metadata))
			content = strings.Join(keywords, " ")
		}
		match, ok := buildSummaryKeywordMatch(doc.NodeID, query, content, keywords)
		if !ok {
			continue
		}
		matches = append(matches, match)
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].NodeID < matches[j].NodeID
		}
		return matches[i].Score > matches[j].Score
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func (r *InMemoryMemoryRepository) ListDocumentsByConversation(_ context.Context, userID, conversationID string) ([]domain.MemoryDocument, error) {
	return r.ListDocumentsByConversationInGeneration(context.Background(), userID, "", conversationID)
}

func (r *InMemoryMemoryRepository) ListDocumentsByConversationInGeneration(_ context.Context, userID, generationID, conversationID string) ([]domain.MemoryDocument, error) {
	return r.ListConversationDocumentsInGeneration(context.Background(), userID, generationID, conversationID, false)
}

func (r *InMemoryMemoryRepository) ListConversationDocumentsInGeneration(_ context.Context, userID, generationID, conversationID string, includeSegments bool) ([]domain.MemoryDocument, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.MemoryDocument, 0)
	for _, doc := range r.documents {
		if doc.UserID != userID || doc.ConversationID != conversationID {
			continue
		}
		if doc.Type == domain.MemoryTypeSegment && !includeSegments {
			continue
		}
		if !matchesDocumentGeneration(userID, generationID, doc) {
			continue
		}
		out = append(out, attachDocumentRuntimeMetadata(doc))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (r *InMemoryMemoryRepository) UpdateDocument(_ context.Context, userID, id string, kind domain.MemoryType, content string) (domain.MemoryDocument, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	doc, ok := r.documents[key(userID, id)]
	if !ok || doc.Type != kind {
		return domain.MemoryDocument{}, errors.New("memory document not found")
	}
	now := time.Now().UTC()
	doc.Content = content
	doc.ActivityAt = now
	doc.TimeTier = domain.ClassifyTimeTier(now, now)
	doc.UpdatedAt = now
	doc.Metadata = cloneMetadata(doc.Metadata)
	r.documents[key(userID, id)] = doc
	return attachDocumentRuntimeMetadata(doc), nil
}

func (r *InMemoryMemoryRepository) ListFactLineage(_ context.Context, userID, generationID, factID string) (domain.FactLineage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	doc, ok := r.documents[key(userID, factID)]
	if !ok || doc.Type != domain.MemoryTypeFact || !matchesDocumentGeneration(userID, generationID, doc) {
		return domain.FactLineage{}, errors.New("memory document not found")
	}
	lineageGroupID := factLineageGroupIDFromMetadata(doc.Metadata)
	if lineageGroupID == "" {
		entry := factLineageEntryFromDocument(doc)
		entry.IsHead = true
		return domain.FactLineage{
			CurrentHeadID:  doc.ID,
			FactKey:        factKeyFromMetadata(doc.Metadata),
			LineageGroupID: lineageGroupID,
			Entries:        []domain.FactLineageEntry{entry},
			RevisionDepth:  1,
			LineagePath:    []string{doc.ID},
		}, nil
	}
	return r.factLineageByGroupLocked(userID, generationID, lineageGroupID), nil
}

func (r *InMemoryMemoryRepository) ListFactLineageByKey(_ context.Context, userID, generationID, factKey string) (domain.FactLineage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	lineageGroupID := strings.TrimSpace(factKey)
	if !strings.HasPrefix(lineageGroupID, "fact-lineage:") {
		lineageGroupID = "fact-lineage:" + strings.TrimSpace(userID) + ":" + sanitizeLineageKey(factKey)
	}
	return r.factLineageByGroupLocked(userID, generationID, lineageGroupID), nil
}

func (r *InMemoryMemoryRepository) factLineageByGroupLocked(userID, generationID, lineageGroupID string) domain.FactLineage {
	entries := make([]domain.FactLineageEntry, 0)
	for _, doc := range r.documents {
		if doc.UserID != userID || doc.Type != domain.MemoryTypeFact || !matchesDocumentGeneration(userID, generationID, doc) {
			continue
		}
		if lineageGroupID != "" && factLineageGroupIDFromMetadata(doc.Metadata) != lineageGroupID {
			continue
		}
		entries = append(entries, factLineageEntryFromDocument(doc))
	}
	lineage := factLineageFromEntries(entries)
	for idx := range lineage.Entries {
		lineage.Entries[idx].IsHead = lineage.Entries[idx].FactID == lineage.CurrentHeadID
	}
	return lineage
}

func (r *InMemoryMemoryRepository) ListKeyMemories(_ context.Context, userID string, limit int) ([]domain.KeyMemory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.KeyMemory, 0)
	for _, km := range r.keyMemories {
		if km.UserID == userID {
			km.Importance = domain.ClampImportance(km.Importance)
			km.DecayFactor = domain.ClampDecayFactor(km.DecayFactor)
			km.ActivityAt = domain.DeriveActivityAt(km.ActivityAt, km.UpdatedAt, km.CreatedAt)
			km.TimeTier = domain.ClassifyTimeTier(km.ActivityAt, time.Now().UTC())
			km.EffectiveScore = km.Importance * domain.TimeDecay(domain.MemoryTypeKeyMemory, km.ActivityAt, km.DecayFactor, km.Importance, time.Now().UTC())
			out = append(out, km)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *InMemoryMemoryRepository) GetKeyMemory(_ context.Context, userID, memoryKey string) (domain.KeyMemory, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	km, ok := r.keyMemories[key(userID, strings.TrimSpace(memoryKey))]
	if !ok {
		return domain.KeyMemory{}, false, nil
	}
	return hydrateKeyMemory(km), true, nil
}

func (r *InMemoryMemoryRepository) GetKeyMemoryByID(_ context.Context, userID, id string) (domain.KeyMemory, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, km := range r.keyMemories {
		if km.UserID == userID && km.ID == strings.TrimSpace(id) {
			return hydrateKeyMemory(km), true, nil
		}
	}
	return domain.KeyMemory{}, false, nil
}

func (r *InMemoryMemoryRepository) CountKeyMemories(_ context.Context, userID string) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, km := range r.keyMemories {
		if km.UserID == userID {
			count++
		}
	}
	return count, nil
}

func (r *InMemoryMemoryRepository) SearchKeyMemories(_ context.Context, input domain.KeyMemoryQuery) ([]domain.KeyMemory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if input.TopK <= 0 {
		input.TopK = 10
	}
	query := strings.ToLower(strings.TrimSpace(input.Query))
	now := time.Now().UTC()
	out := make([]domain.KeyMemory, 0)
	for _, km := range r.keyMemories {
		if km.UserID != input.UserID {
			continue
		}
		km.ActivityAt = domain.DeriveActivityAt(km.ActivityAt, km.UpdatedAt, km.CreatedAt)
		km.TimeTier = domain.ClassifyTimeTier(km.ActivityAt, now)
		if !matchesTemporalKeyMemory(km, input) {
			continue
		}
		score := km.Importance * domain.TimeDecay(domain.MemoryTypeKeyMemory, km.ActivityAt, km.DecayFactor, km.Importance, now)
		if query != "" {
			content := strings.ToLower(km.Key + " " + km.Value + " " + km.Category)
			switch {
			case strings.Contains(content, query):
				score *= 1
			case strings.Contains(content, firstToken(query)):
				score *= 0.8
			default:
				score *= 0.5
			}
		}
		km.EffectiveScore = score
		out = append(out, km)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EffectiveScore == out[j].EffectiveScore {
			return out[i].ActivityAt.After(out[j].ActivityAt)
		}
		return out[i].EffectiveScore > out[j].EffectiveScore
	})
	if len(out) > input.TopK {
		out = out[:input.TopK]
	}
	return out, nil
}

func hydrateKeyMemory(km domain.KeyMemory) domain.KeyMemory {
	km.Importance = domain.ClampImportance(km.Importance)
	km.DecayFactor = domain.ClampDecayFactor(km.DecayFactor)
	km.ActivityAt = domain.DeriveActivityAt(km.ActivityAt, km.UpdatedAt, km.CreatedAt)
	km.TimeTier = domain.ClassifyTimeTier(km.ActivityAt, time.Now().UTC())
	km.EffectiveScore = km.Importance * domain.TimeDecay(domain.MemoryTypeKeyMemory, km.ActivityAt, km.DecayFactor, km.Importance, time.Now().UTC())
	return km
}

func (r *InMemoryMemoryRepository) UpsertKeyMemory(_ context.Context, km domain.KeyMemory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	existing, existed := r.keyMemories[key(km.UserID, km.Key)]
	existingNodeID := stringMetadataValue(existing.Metadata, "node_id")
	if existed {
		if km.ID == "" || km.ID != existing.ID {
			km.ID = existing.ID
		}
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
		if existingNodeID != "" {
			km.Metadata["node_id"] = existingNodeID
		}
		if km.Category == "" {
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
	if km.CreatedAt.IsZero() {
		km.CreatedAt = now
	}
	km.UpdatedAt = now
	km.Importance = domain.ClampImportance(km.Importance)
	if km.Importance == 0 {
		km.Importance = 1
	}
	km.DecayFactor = domain.ClampDecayFactor(km.DecayFactor)
	if km.DecayFactor == 0 {
		km.DecayFactor = 1
	}
	km.ActivityAt = domain.DeriveActivityAt(km.ActivityAt, km.UpdatedAt, km.CreatedAt)
	km.TimeTier = domain.ClassifyTimeTier(km.ActivityAt, now)
	km.EffectiveScore = km.Importance * domain.TimeDecay(domain.MemoryTypeKeyMemory, km.ActivityAt, km.DecayFactor, km.Importance, now)
	if km.Metadata == nil {
		km.Metadata = map[string]any{}
	}
	km.Metadata["activity_at"] = km.ActivityAt.Format(time.RFC3339)
	km.Metadata["time_tier"] = km.TimeTier
	r.keyMemories[key(km.UserID, km.Key)] = km
	nodeID := ""
	if typed, ok := km.Metadata["node_id"].(string); ok {
		nodeID = typed
	}
	if nodeID == "" {
		nodeID = existingNodeID
	}
	if nodeID == "" {
		nodeID = keyMemoryNodeID(km.UserID, km.Key)
		if km.Metadata == nil {
			km.Metadata = map[string]any{}
		}
	}
	km.Metadata["node_id"] = nodeID
	if existed {
		delete(r.documents, key(km.UserID, existing.ID))
		if existingNodeID != "" && existingNodeID != nodeID {
			r.removeRelationsForNodeLocked(km.UserID, existingNodeID)
		}
	}
	r.keyMemories[key(km.UserID, km.Key)] = km
	r.documents[key(km.UserID, km.ID)] = domain.MemoryDocument{
		ID:          km.ID,
		NodeID:      nodeID,
		UserID:      km.UserID,
		Type:        domain.MemoryTypeKeyMemory,
		Content:     km.Key + ": " + km.Value,
		Importance:  km.Importance,
		DecayFactor: km.DecayFactor,
		ActivityAt:  km.ActivityAt,
		TimeTier:    km.TimeTier,
		Metadata: map[string]any{
			"key":           km.Key,
			"value":         km.Value,
			"category":      km.Category,
			"source":        km.Source,
			"review_status": km.ReviewStatus,
			"activity_at":   km.ActivityAt.Format(time.RFC3339),
			"time_tier":     km.TimeTier,
		},
		CreatedAt: km.CreatedAt,
		UpdatedAt: km.UpdatedAt,
	}
	r.syncTemporalRelationsLocked(km.UserID, "", nodeID, km.ActivityAt)
	return nil
}

func (r *InMemoryMemoryRepository) DeleteKeyMemory(_ context.Context, userID, memoryKey string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if km, ok := r.keyMemories[key(userID, memoryKey)]; ok {
		nodeID := stringMetadataValue(km.Metadata, "node_id")
		delete(r.documents, key(userID, km.ID))
		if nodeID != "" {
			r.removeRelationsForNodeLocked(userID, nodeID)
		}
	}
	delete(r.keyMemories, key(userID, memoryKey))
	return nil
}

func (r *InMemoryMemoryRepository) removeRelationsForNodeLocked(userID, nodeID string) {
	filtered := r.relations[:0]
	for _, relation := range r.relations {
		if relation.UserID == userID && (relation.SrcNodeID == nodeID || relation.DstNodeID == nodeID) {
			continue
		}
		filtered = append(filtered, relation)
	}
	r.relations = filtered
}

func (r *InMemoryMemoryRepository) LinkDocuments(_ context.Context, relation domain.MemoryRelation) error {
	relation.UserID = strings.TrimSpace(relation.UserID)
	relation.SrcNodeID = strings.TrimSpace(relation.SrcNodeID)
	relation.DstNodeID = strings.TrimSpace(relation.DstNodeID)
	relation.RelationType = strings.TrimSpace(relation.RelationType)
	if relation.UserID == "" || relation.SrcNodeID == "" || relation.DstNodeID == "" || relation.RelationType == "" {
		return errors.New("relation user, endpoints, and type are required")
	}
	if relation.SrcNodeID == relation.DstNodeID {
		return errors.New("memory relation cannot link a node to itself")
	}
	if relation.Weight < 0 || relation.Weight > 1 {
		return errors.New("memory relation weight must be between 0 and 1")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if relation.ID == "" {
		relation.ID = uuid.NewString()
	}
	if relation.CreatedAt.IsZero() {
		relation.CreatedAt = time.Now().UTC()
	}
	for idx, existing := range r.relations {
		if existing.UserID == relation.UserID &&
			existing.SrcNodeID == relation.SrcNodeID &&
			existing.DstNodeID == relation.DstNodeID &&
			existing.RelationType == relation.RelationType {
			if shouldKeepExistingSnapshot(existing.Metadata, relation.Metadata) {
				return nil
			}
			relation.ID = existing.ID
			r.relations[idx] = relation
			return nil
		}
	}
	r.relations = append(r.relations, relation)
	return nil
}

func (r *InMemoryMemoryRepository) DeleteRelationsByTarget(_ context.Context, userID, targetNodeID, relationType string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	filtered := r.relations[:0]
	for _, relation := range r.relations {
		if relation.UserID == userID && relation.DstNodeID == targetNodeID && relation.RelationType == relationType {
			continue
		}
		filtered = append(filtered, relation)
	}
	r.relations = filtered
	return nil
}

func (r *InMemoryMemoryRepository) ListRelations(_ context.Context, userID, generationID string, nodeIDs []string, depth int) ([]domain.MemoryRelation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	allowed := map[string]struct{}{}
	for _, nodeID := range nodeIDs {
		allowed[nodeID] = struct{}{}
	}

	limitDepth := depth
	if limitDepth <= 0 {
		limitDepth = 1
	}
	frontier := allowed
	visitedRelations := map[string]struct{}{}
	collected := make([]domain.MemoryRelation, 0)
	for hop := 0; hop < limitDepth; hop++ {
		nextFrontier := map[string]struct{}{}
		for _, relation := range r.relations {
			if relation.UserID != userID {
				continue
			}
			if !matchesGenerationValue(userID, generationID, relation.GenerationID, true) {
				continue
			}
			_, srcMatch := frontier[relation.SrcNodeID]
			_, dstMatch := frontier[relation.DstNodeID]
			if !srcMatch && !dstMatch {
				continue
			}
			if _, seen := visitedRelations[relation.ID]; seen {
				continue
			}
			collected = append(collected, relation)
			visitedRelations[relation.ID] = struct{}{}
			if isTemporalRelationType(relation.RelationType) {
				continue
			}
			nextFrontier[relation.SrcNodeID] = struct{}{}
			nextFrontier[relation.DstNodeID] = struct{}{}
		}
		frontier = nextFrontier
	}
	return collected, nil
}

func (r *InMemoryMemoryRepository) ReplaceEvidences(_ context.Context, userID, nodeID string, evidences []domain.MemoryEvidence) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(nodeID) == "" {
		return nil
	}
	next := make([]domain.MemoryEvidence, 0, len(evidences))
	for _, evidence := range evidences {
		if evidence.ID == "" {
			evidence.ID = uuid.NewString()
		}
		if evidence.UserID == "" {
			evidence.UserID = userID
		}
		if evidence.NodeID == "" {
			evidence.NodeID = nodeID
		}
		if evidence.Weight == 0 {
			evidence.Weight = 1
		}
		if evidence.CreatedAt.IsZero() {
			evidence.CreatedAt = time.Now().UTC()
		}
		evidence.Metadata = cloneMetadata(evidence.Metadata)
		next = append(next, evidence)
	}
	r.evidences[key(userID, nodeID)] = next
	return nil
}

func (r *InMemoryMemoryRepository) MergeEvidences(_ context.Context, userID, nodeID string, evidences []domain.MemoryEvidence) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(nodeID) == "" {
		return nil
	}
	items := append([]domain.MemoryEvidence(nil), r.evidences[key(userID, nodeID)]...)
	byID := make(map[string]int, len(items))
	for idx, item := range items {
		byID[item.ID] = idx
	}
	for _, evidence := range evidences {
		if evidence.ID == "" {
			evidence.ID = uuid.NewString()
		}
		if evidence.UserID == "" {
			evidence.UserID = userID
		}
		if evidence.NodeID == "" {
			evidence.NodeID = nodeID
		}
		if evidence.Weight == 0 {
			evidence.Weight = 1
		}
		if evidence.CreatedAt.IsZero() {
			evidence.CreatedAt = time.Now().UTC()
		}
		evidence.Metadata = cloneMetadata(evidence.Metadata)
		if idx, ok := byID[evidence.ID]; ok {
			items[idx] = evidence
			continue
		}
		byID[evidence.ID] = len(items)
		items = append(items, evidence)
	}
	r.evidences[key(userID, nodeID)] = items
	return nil
}

func (r *InMemoryMemoryRepository) ListEvidences(_ context.Context, userID, nodeID string) ([]domain.MemoryEvidence, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := append([]domain.MemoryEvidence(nil), r.evidences[key(userID, nodeID)]...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].SequenceNum == items[j].SequenceNum {
			if items[i].StartMS == items[j].StartMS {
				return items[i].CreatedAt.Before(items[j].CreatedAt)
			}
			return items[i].StartMS < items[j].StartMS
		}
		return items[i].SequenceNum < items[j].SequenceNum
	})
	return items, nil
}

func (r *InMemoryMemoryRepository) ListEvidencesByNodeIDs(_ context.Context, userID string, nodeIDs []string) ([]domain.MemoryEvidence, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	allowed := map[string]struct{}{}
	for _, nodeID := range nodeIDs {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			continue
		}
		allowed[nodeID] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, nil
	}
	items := make([]domain.MemoryEvidence, 0)
	for nodeID := range allowed {
		items = append(items, r.evidences[key(userID, nodeID)]...)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].NodeID == items[j].NodeID {
			if items[i].SequenceNum == items[j].SequenceNum {
				if items[i].StartMS == items[j].StartMS {
					return items[i].CreatedAt.Before(items[j].CreatedAt)
				}
				return items[i].StartMS < items[j].StartMS
			}
			return items[i].SequenceNum < items[j].SequenceNum
		}
		return items[i].NodeID < items[j].NodeID
	})
	return items, nil
}

func (r *InMemoryMemoryRepository) syncFactSupersedesLocked(doc domain.MemoryDocument) error {
	factKey := factKeyFromMetadata(doc.Metadata)
	lineageGroupID := factLineageGroupIDFromMetadata(doc.Metadata)
	if lineageGroupID == "" && (stringMetadataValue(doc.Metadata, "superseded_by_id") != "" || len(supersededFactIDsFromMetadata(doc.Metadata)) > 0) {
		return errors.New("fact supersession requires fact_key or lineage_group_id")
	}
	filtered := make([]domain.MemoryRelation, 0, len(r.relations))
	for _, relation := range r.relations {
		if relation.UserID == doc.UserID && relation.GenerationID == doc.GenerationID && relation.RelationType == domain.MemoryRelationSupersedes && (relation.DstNodeID == doc.NodeID || relation.SrcNodeID == doc.NodeID) {
			continue
		}
		filtered = append(filtered, relation)
	}
	r.relations = filtered

	supersededByID := stringMetadataValue(doc.Metadata, "superseded_by_id")
	doc.Metadata["is_superseded"] = supersededByID != "" || boolMetadataValue(doc.Metadata, "is_superseded")
	r.documents[key(doc.UserID, doc.ID)] = doc

	if supersededByID != "" {
		supersedingNodeID := r.resolveFactNodeIDLocked(doc.UserID, doc.GenerationID, lineageGroupID, supersededByID)
		if supersedingNodeID == "" {
			return fmt.Errorf("fact supersession target %q not found in lineage %q", supersededByID, lineageGroupID)
		}
		if r.factSupersessionWouldCycleLocked(doc.UserID, doc.GenerationID, supersededByID, doc.ID) {
			return errors.New("fact supersession cycle detected")
		}
		r.upsertRelationLocked(domain.MemoryRelation{
			GenerationID: doc.GenerationID,
			UserID:       doc.UserID,
			SrcNodeID:    supersedingNodeID,
			DstNodeID:    doc.NodeID,
			RelationType: domain.MemoryRelationSupersedes,
			Weight:       1,
			Source:       "fact_supersession",
			Metadata: map[string]any{
				"superseded_fact_id":  doc.ID,
				"superseding_fact_id": supersededByID,
				"fact_key":            factKey,
				"lineage_group_id":    lineageGroupID,
			},
		})
	}

	for _, supersededID := range supersededFactIDsFromMetadata(doc.Metadata) {
		if supersededID == "" {
			continue
		}
		if supersededID == doc.ID {
			return errors.New("fact supersession cycle detected")
		}
		if r.factSupersessionWouldCycleLocked(doc.UserID, doc.GenerationID, doc.ID, supersededID) {
			return errors.New("fact supersession cycle detected")
		}
		candidate, ok := r.documents[key(doc.UserID, supersededID)]
		if !ok || candidate.Type != domain.MemoryTypeFact || candidate.GenerationID != doc.GenerationID {
			return fmt.Errorf("fact supersession target %q not found in generation %q", supersededID, doc.GenerationID)
		}
		if lineageGroupID != "" && factLineageGroupIDFromMetadata(candidate.Metadata) != lineageGroupID {
			return fmt.Errorf("fact supersession target %q not found in lineage %q", supersededID, lineageGroupID)
		}
		candidate.Metadata = cloneMetadata(candidate.Metadata)
		candidate.Metadata["superseded_by_id"] = doc.ID
		candidate.Metadata["is_superseded"] = true
		if _, ok := candidate.Metadata["valid_to"]; !ok && !doc.ActivityAt.IsZero() {
			candidate.Metadata["valid_to"] = doc.ActivityAt.Format(time.RFC3339)
		}
		candidate.UpdatedAt = doc.UpdatedAt
		r.documents[key(candidate.UserID, candidate.ID)] = candidate
		r.upsertRelationLocked(domain.MemoryRelation{
			GenerationID: doc.GenerationID,
			UserID:       doc.UserID,
			SrcNodeID:    doc.NodeID,
			DstNodeID:    candidate.NodeID,
			RelationType: domain.MemoryRelationSupersedes,
			Weight:       1,
			Source:       "fact_supersession",
			Metadata: map[string]any{
				"superseded_fact_id":  candidate.ID,
				"superseding_fact_id": doc.ID,
				"fact_key":            factKey,
				"lineage_group_id":    lineageGroupID,
			},
		})
	}

	if lineageGroupID == "" {
		return nil
	}
	for _, candidate := range r.documents {
		if candidate.UserID != doc.UserID || candidate.Type != domain.MemoryTypeFact {
			continue
		}
		if candidate.GenerationID != doc.GenerationID {
			continue
		}
		if lineageGroupID != "" && factLineageGroupIDFromMetadata(candidate.Metadata) != lineageGroupID {
			continue
		}
		if stringMetadataValue(candidate.Metadata, "superseded_by_id") != doc.ID {
			continue
		}
		r.upsertRelationLocked(domain.MemoryRelation{
			GenerationID: doc.GenerationID,
			UserID:       doc.UserID,
			SrcNodeID:    doc.NodeID,
			DstNodeID:    candidate.NodeID,
			RelationType: domain.MemoryRelationSupersedes,
			Weight:       1,
			Source:       "fact_supersession",
			Metadata: map[string]any{
				"superseded_fact_id":  candidate.ID,
				"superseding_fact_id": doc.ID,
				"fact_key":            factKey,
				"lineage_group_id":    lineageGroupID,
			},
		})
	}
	return nil
}

func supersededFactIDsFromMetadata(metadata map[string]any) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	add(stringMetadataValue(metadata, "supersedes_fact_id"))
	for _, value := range stringSliceMetadataValue(metadata, "supersedes_fact_ids") {
		add(value)
	}
	return out
}

func preserveFactSupersessionMetadata(doc, existing domain.MemoryDocument) domain.MemoryDocument {
	if doc.Type != domain.MemoryTypeFact || existing.Type != domain.MemoryTypeFact {
		return doc
	}
	if doc.Metadata == nil {
		doc.Metadata = map[string]any{}
	}
	supersessionStateSpecified := metadataContainsKey(doc.Metadata, "superseded_by_id") || metadataContainsKey(doc.Metadata, "is_superseded")
	if !supersessionStateSpecified {
		for _, field := range []string{"is_superseded", "superseded_by_id"} {
			if value, ok := existing.Metadata[field]; ok {
				doc.Metadata[field] = value
			}
		}
	}
	for _, field := range []string{"valid_from", "valid_to"} {
		if _, ok := doc.Metadata[field]; ok {
			continue
		}
		if value, ok := existing.Metadata[field]; ok {
			doc.Metadata[field] = value
		}
	}
	return doc
}

func metadataContainsKey(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	_, ok := metadata[key]
	return ok
}

func (r *InMemoryMemoryRepository) resolveFactNodeIDLocked(userID, generationID, lineageGroupID, factID string) string {
	if factID == "" {
		return ""
	}
	doc, ok := r.documents[key(userID, factID)]
	if !ok || doc.Type != domain.MemoryTypeFact {
		return ""
	}
	if doc.GenerationID != generationID || factLineageGroupIDFromMetadata(doc.Metadata) != lineageGroupID {
		return ""
	}
	return doc.NodeID
}

func (r *InMemoryMemoryRepository) factSupersessionWouldCycleLocked(userID, generationID, newerFactID, olderFactID string) bool {
	newerFactID = strings.TrimSpace(newerFactID)
	olderFactID = strings.TrimSpace(olderFactID)
	if newerFactID == "" || olderFactID == "" {
		return false
	}
	if newerFactID == olderFactID {
		return true
	}
	seen := map[string]struct{}{}
	current := newerFactID
	for depth := 0; depth < 128 && current != ""; depth++ {
		if current == olderFactID {
			return true
		}
		if _, ok := seen[current]; ok {
			return true
		}
		seen[current] = struct{}{}
		doc, ok := r.documents[key(userID, current)]
		if !ok || doc.Type != domain.MemoryTypeFact || doc.GenerationID != generationID {
			return false
		}
		current = stringMetadataValue(doc.Metadata, "superseded_by_id")
	}
	return current != ""
}

func (r *InMemoryMemoryRepository) nextFactRevisionSeqLocked(doc domain.MemoryDocument) int {
	lineageGroupID := factLineageGroupIDFromMetadata(doc.Metadata)
	if lineageGroupID == "" {
		return 1
	}
	if existing, ok := r.documents[key(doc.UserID, doc.ID)]; ok {
		if seq := factLineageRevisionSeqFromMetadata(existing.Metadata); seq > 0 {
			return seq
		}
	}
	maxSeq := 0
	for _, candidate := range r.documents {
		if candidate.UserID != doc.UserID || candidate.GenerationID != doc.GenerationID || candidate.Type != domain.MemoryTypeFact {
			continue
		}
		if factLineageGroupIDFromMetadata(candidate.Metadata) != lineageGroupID {
			continue
		}
		if seq := factLineageRevisionSeqFromMetadata(candidate.Metadata); seq > maxSeq {
			maxSeq = seq
		}
	}
	return maxSeq + 1
}

func (r *InMemoryMemoryRepository) currentFactLineageHeadIDsLocked(doc domain.MemoryDocument) []string {
	lineageGroupID := factLineageGroupIDFromMetadata(doc.Metadata)
	if lineageGroupID == "" {
		return nil
	}
	type head struct {
		id  string
		seq int
	}
	heads := make([]head, 0)
	for _, candidate := range r.documents {
		if candidate.UserID != doc.UserID || candidate.GenerationID != doc.GenerationID || candidate.Type != domain.MemoryTypeFact || candidate.ID == doc.ID {
			continue
		}
		if factLineageGroupIDFromMetadata(candidate.Metadata) != lineageGroupID {
			continue
		}
		if boolMetadataValue(candidate.Metadata, "is_superseded") || stringMetadataValue(candidate.Metadata, "superseded_by_id") != "" {
			continue
		}
		heads = append(heads, head{id: candidate.ID, seq: factLineageRevisionSeqFromMetadata(candidate.Metadata)})
	}
	sort.Slice(heads, func(i, j int) bool {
		if heads[i].seq == heads[j].seq {
			return heads[i].id < heads[j].id
		}
		return heads[i].seq > heads[j].seq
	})
	out := make([]string, 0, len(heads))
	for _, item := range heads {
		out = append(out, item.id)
	}
	return out
}

func (r *InMemoryMemoryRepository) upsertRelationLocked(relation domain.MemoryRelation) {
	if relation.ID == "" {
		relation.ID = uuid.NewString()
	}
	if relation.CreatedAt.IsZero() {
		relation.CreatedAt = time.Now().UTC()
	}
	for idx, existing := range r.relations {
		if existing.UserID == relation.UserID &&
			existing.GenerationID == relation.GenerationID &&
			existing.SrcNodeID == relation.SrcNodeID &&
			existing.DstNodeID == relation.DstNodeID &&
			existing.RelationType == relation.RelationType {
			relation.ID = existing.ID
			r.relations[idx] = relation
			return
		}
	}
	r.relations = append(r.relations, relation)
}

func (r *InMemoryMemoryRepository) syncTemporalRelationsLocked(userID, generationID, nodeID string, activityAt time.Time) {
	if userID == "" || nodeID == "" {
		return
	}
	activityAt = domain.DeriveActivityAt(activityAt, time.Time{}, time.Now().UTC())
	tier := domain.ClassifyTimeTier(activityAt, time.Now().UTC())
	filtered := r.relations[:0]
	for _, relation := range r.relations {
		if relation.UserID == userID && relation.SrcNodeID == nodeID && relation.GenerationID == generationID && isTemporalIndexRelationType(relation.RelationType) {
			continue
		}
		filtered = append(filtered, relation)
	}
	r.relations = filtered

	tierNodeID := temporalTierNodeID(userID, tier)
	dayNodeID := temporalDayNodeID(userID, activityAt)
	monthNodeID := temporalMonthNodeID(userID, activityAt)
	yearNodeID := temporalYearNodeID(userID, activityAt)

	r.upsertRelationLocked(domain.MemoryRelation{GenerationID: generationID, UserID: userID, SrcNodeID: nodeID, DstNodeID: tierNodeID, RelationType: "indexed_in_tier", Weight: 1, Source: "temporal"})
	r.upsertRelationLocked(domain.MemoryRelation{GenerationID: generationID, UserID: userID, SrcNodeID: nodeID, DstNodeID: dayNodeID, RelationType: "occurred_on_day", Weight: 1, Source: "temporal"})
	r.upsertRelationLocked(domain.MemoryRelation{GenerationID: generationID, UserID: userID, SrcNodeID: dayNodeID, DstNodeID: monthNodeID, RelationType: "day_in_month", Weight: 1, Source: "temporal"})
	r.upsertRelationLocked(domain.MemoryRelation{GenerationID: generationID, UserID: userID, SrcNodeID: monthNodeID, DstNodeID: yearNodeID, RelationType: "month_in_year", Weight: 1, Source: "temporal"})
}

func matchesTemporalInput(doc domain.MemoryDocument, input domain.SearchInput) bool {
	activityAt := domain.DeriveActivityAt(doc.ActivityAt, doc.UpdatedAt, doc.CreatedAt)
	tier := doc.TimeTier
	if tier == "" {
		tier = domain.ClassifyTimeTier(activityAt, time.Now().UTC())
	}
	if len(input.TimeTiers) > 0 {
		matched := false
		for _, candidate := range input.TimeTiers {
			if candidate == tier {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if input.DateFrom != nil && activityAt.Before(input.DateFrom.UTC()) {
		return false
	}
	if input.DateTo != nil && activityAt.After(input.DateTo.UTC()) {
		return false
	}
	return true
}

func matchesTemporalKeyMemory(km domain.KeyMemory, input domain.KeyMemoryQuery) bool {
	if len(input.TimeTiers) > 0 {
		matched := false
		for _, candidate := range input.TimeTiers {
			if candidate == km.TimeTier {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if input.DateFrom != nil && km.ActivityAt.Before(input.DateFrom.UTC()) {
		return false
	}
	if input.DateTo != nil && km.ActivityAt.After(input.DateTo.UTC()) {
		return false
	}
	return true
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func isTemporalRelationType(relationType string) bool {
	switch relationType {
	case "indexed_in_tier", "occurred_on_day", "day_in_month", "month_in_year", domain.MemoryRelationSupersedes:
		return true
	default:
		return false
	}
}

func isTemporalIndexRelationType(relationType string) bool {
	switch relationType {
	case "indexed_in_tier", "occurred_on_day", "day_in_month", "month_in_year":
		return true
	default:
		return false
	}
}

func temporalTierNodeID(userID string, tier domain.TimeTier) string {
	return "time:tier:" + userID + ":" + string(tier)
}

func temporalDayNodeID(userID string, at time.Time) string {
	return "time:day:" + userID + ":" + at.UTC().Format("2006-01-02")
}

func temporalMonthNodeID(userID string, at time.Time) string {
	return "time:month:" + userID + ":" + at.UTC().Format("2006-01")
}

func temporalYearNodeID(userID string, at time.Time) string {
	return "time:year:" + userID + ":" + at.UTC().Format("2006")
}

type InMemoryOutboxRepository struct {
	mu     sync.RWMutex
	events map[string]domain.OutboxEvent
}

func NewInMemoryOutboxRepository() *InMemoryOutboxRepository {
	return &InMemoryOutboxRepository{events: map[string]domain.OutboxEvent{}}
}

func (r *InMemoryOutboxRepository) Enqueue(_ context.Context, event domain.OutboxEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.AvailableAt.IsZero() {
		event.AvailableAt = time.Now().UTC()
	}
	if event.Status == "" {
		event.Status = "pending"
	}
	r.events[event.ID] = event
	return nil
}

func (r *InMemoryOutboxRepository) Pending(_ context.Context, limit int) ([]domain.OutboxEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	events := make([]domain.OutboxEvent, 0)
	for _, event := range r.events {
		if event.ProcessedAt == nil {
			events = append(events, event)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].CreatedAt.Before(events[j].CreatedAt) })
	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

func (r *InMemoryOutboxRepository) MarkProcessed(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	event, ok := r.events[id]
	if !ok {
		return errors.New("outbox event not found")
	}
	now := time.Now().UTC()
	event.ProcessedAt = &now
	event.Status = "processed"
	r.events[id] = event
	return nil
}

func key(parts ...string) string {
	return strings.Join(parts, "::")
}

func keyMemoryNodeID(userID, memoryKey string) string {
	return "node:keymemory:" + userID + ":" + memoryKey
}

func defaultMemoryNodeID(userID string, kind domain.MemoryType, documentID string) string {
	userID = strings.TrimSpace(userID)
	documentID = strings.TrimSpace(documentID)
	if userID == "" {
		userID = "unknown-user"
	}
	if documentID == "" {
		documentID = uuid.NewString()
	}
	return "node:" + string(kind) + ":" + userID + ":" + documentID
}

func matchesMemoryType(value domain.MemoryType, filters []domain.MemoryType) bool {
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

func matchesDocumentGeneration(userID, requestedGenerationID string, doc domain.MemoryDocument) bool {
	if doc.Type == domain.MemoryTypeKeyMemory {
		return true
	}
	return matchesGenerationValue(userID, requestedGenerationID, doc.GenerationID, false)
}

func matchesGenerationValue(userID, requestedGenerationID, actualGenerationID string, includeGenerationless bool) bool {
	requestedGenerationID = strings.TrimSpace(requestedGenerationID)
	actualGenerationID = strings.TrimSpace(actualGenerationID)
	if requestedGenerationID == "" {
		return true
	}
	if actualGenerationID == requestedGenerationID {
		return true
	}
	if !includeGenerationless {
		return false
	}
	return actualGenerationID == ""
}

func currentGenerationID(userID string) string {
	return "gen:current:" + strings.TrimSpace(userID)
}

func conversationSegmentGeneration(userID string, metadata map[string]any) string {
	if generationID := stringMetadataValue(metadata, "memory_generation_id"); generationID != "" {
		return generationID
	}
	return currentGenerationID(userID)
}

func filterConversationSegments(userID, targetGenerationID string, segments []domain.ConversationSegment) []domain.ConversationSegment {
	out := make([]domain.ConversationSegment, 0, len(segments))
	for _, segment := range segments {
		if matchesConversationSegmentGeneration(userID, targetGenerationID, segment.GenerationID) {
			out = append(out, segment)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SegmentIndex == out[j].SegmentIndex {
			return out[i].UpdatedAt.Before(out[j].UpdatedAt)
		}
		return out[i].SegmentIndex < out[j].SegmentIndex
	})
	return out
}

func matchesConversationSegmentGeneration(userID, targetGenerationID, segmentGenerationID string) bool {
	targetGenerationID = strings.TrimSpace(targetGenerationID)
	if targetGenerationID == "" {
		targetGenerationID = currentGenerationID(userID)
	}
	segmentGenerationID = strings.TrimSpace(segmentGenerationID)
	return segmentGenerationID == targetGenerationID
}

func shouldKeepExistingSnapshot(existingMetadata, incomingMetadata map[string]any) bool {
	incomingVersion := sourceSnapshotVersion(incomingMetadata)
	if incomingVersion <= 0 {
		return false
	}
	return sourceSnapshotVersion(existingMetadata) > incomingVersion
}

func sourceSnapshotVersion(metadata map[string]any) int {
	if len(metadata) == 0 {
		return 0
	}
	switch typed := metadata["source_snapshot_version"].(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		value, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return value
		}
	}
	return 0
}

func stringMetadataValue(metadata map[string]any, field string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[field]
	if !ok || value == nil {
		return ""
	}
	typed, ok := value.(string)
	if !ok {
		return ""
	}
	return typed
}

func boolMetadataValue(metadata map[string]any, field string) bool {
	if metadata == nil {
		return false
	}
	value, ok := metadata[field]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func stringSliceMetadataValue(metadata map[string]any, key string) []string {
	if metadata == nil {
		return nil
	}
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
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

func normalizeKeywordTerms(query string) []string {
	fields := strings.Fields(strings.ToLower(query))
	if len(fields) == 0 {
		fields = []string{strings.ToLower(strings.TrimSpace(query))}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		term := strings.TrimSpace(strings.TrimFunc(field, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
		}))
		if !shouldKeepKeywordTerm(term) {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, term)
	}
	return out
}

func buildSummaryKeywordMatch(nodeID, query, content string, keywords []string) (domain.KeywordMatch, bool) {
	terms := normalizeKeywordTerms(query)
	if len(terms) == 0 {
		return domain.KeywordMatch{}, false
	}

	matched := make([]string, 0, len(terms))
	matchedSet := make(map[string]struct{}, len(terms))
	totalWeight := 0.0
	for idx, keyword := range keywords {
		normalized := normalizeSingleKeywordTerm(keyword)
		if normalized == "" {
			continue
		}
		if !containsKeywordTerm(terms, normalized) {
			continue
		}
		if _, ok := matchedSet[normalized]; ok {
			continue
		}
		matchedSet[normalized] = struct{}{}
		matched = append(matched, normalized)
		weight := 1.0 - float64(idx)*0.1
		if weight < 0.1 {
			weight = 0.1
		}
		totalWeight += weight
	}

	normalizedContent := strings.ToLower(strings.TrimSpace(content))
	for _, term := range terms {
		if _, ok := matchedSet[term]; ok {
			continue
		}
		if strings.Contains(normalizedContent, term) {
			matchedSet[term] = struct{}{}
			matched = append(matched, term)
			totalWeight += lexicalKeywordWeight(term)
		}
	}

	if len(matched) == 0 {
		return domain.KeywordMatch{}, false
	}

	return domain.KeywordMatch{
		NodeID:      nodeID,
		Keywords:    matched,
		TotalWeight: totalWeight,
		Score:       keywordMatchScore(totalWeight, len(matched)),
	}, true
}

func normalizeSingleKeywordTerm(value string) string {
	normalized := strings.TrimSpace(strings.ToLower(value))
	if !shouldKeepKeywordTerm(normalized) {
		return ""
	}
	return normalized
}

func containsKeywordTerm(terms []string, target string) bool {
	for _, term := range terms {
		if term == target {
			return true
		}
	}
	return false
}

func lexicalKeywordWeight(term string) float64 {
	for _, r := range term {
		if r > unicode.MaxASCII {
			return 0.75
		}
	}
	if len(term) >= 8 {
		return 0.65
	}
	return 0.55
}

func shouldKeepKeywordTerm(term string) bool {
	if strings.TrimSpace(term) == "" {
		return false
	}
	if _, ok := keywordStopwords[term]; ok {
		return false
	}
	if len(term) == 1 {
		for _, r := range term {
			if r <= unicode.MaxASCII {
				return false
			}
		}
	}
	return true
}

var keywordStopwords = map[string]struct{}{
	"a":        {},
	"an":       {},
	"and":      {},
	"are":      {},
	"as":       {},
	"at":       {},
	"be":       {},
	"for":      {},
	"from":     {},
	"in":       {},
	"includes": {},
	"is":       {},
	"of":       {},
	"on":       {},
	"or":       {},
	"the":      {},
	"their":    {},
	"this":     {},
	"to":       {},
	"user":     {},
	"uses":     {},
	"with":     {},
}

func keywordMatchScore(totalWeight float64, matched int) float64 {
	if matched <= 0 {
		return 0
	}
	score := 0.45 + totalWeight*0.22 + float64(matched-1)*0.04
	if score > 0.95 {
		return 0.95
	}
	return score
}
