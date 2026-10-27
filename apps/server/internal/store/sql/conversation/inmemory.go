package conversation

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	conversationservice "memstation/apps/server/internal/service/conversation"
	recordutilstore "memstation/apps/server/internal/store/sql/internal/recordutil"

	"github.com/google/uuid"
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

func (r *InMemoryConversationRepository) PersistExchange(ctx context.Context, conversation domain.ConversationRecord) error {
	var err error
	conversation, err = normalizeConversationScope(ctx, conversation)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, stored := range r.conversations {
		if stored.ConversationID == conversation.ConversationID && stored.UserID != conversation.UserID {
			return domain.ErrAccessDenied
		}
	}
	existing, ok := r.conversations[recordutilstore.Key(conversation.UserID, conversation.ConversationID)]
	if conversation.ExpectedRevision != nil && existing.Revision != *conversation.ExpectedRevision {
		return domain.ErrAccessConflict
	}
	if ok && (existing.WorkspaceID != conversation.WorkspaceID || existing.AgentID != conversation.AgentID) {
		return domain.ErrAccessDenied
	}
	conversation.Revision = existing.Revision + 1
	conversation.ExpectedRevision = nil

	if ok {
		if conversation.CreatedAt.IsZero() {
			conversation.CreatedAt = existing.CreatedAt
		}
		conversation.FinalizeStatus = existing.FinalizeStatus
		conversation.SummaryStatus = existing.SummaryStatus
		conversation.AnalysisStatus = existing.AnalysisStatus
		conversation.Metadata = mergeConversationWriteMetadata(existing.Metadata, conversation.Metadata, existing.FinalizeStatus)
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
			conversation.WorkspaceID,
			conversationSegmentGeneration(conversation.WorkspaceID, conversation.Metadata),
			r.segments[recordutilstore.Key(conversation.UserID, conversation.ConversationID)],
		)
	}
	r.conversations[recordutilstore.Key(conversation.UserID, conversation.ConversationID)] = conversation
	return nil
}

func (r *InMemoryConversationRepository) GetConversation(ctx context.Context, userID, conversationID string) (domain.ConversationRecord, error) {
	return r.GetConversationInGeneration(ctx, userID, conversationID, "")
}

func (r *InMemoryConversationRepository) GetConversationInGeneration(ctx context.Context, userID, conversationID, generationID string) (domain.ConversationRecord, error) {
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return domain.ConversationRecord{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	conversation, ok := r.conversations[recordutilstore.Key(userID, conversationID)]
	if !ok || conversation.WorkspaceID != workspace {
		return domain.ConversationRecord{}, domain.ErrConversationNotFound
	}
	resolvedGenerationID := strings.TrimSpace(generationID)
	if resolvedGenerationID == "" {
		resolvedGenerationID = conversationSegmentGeneration(security.WorkspaceID(ctx, userID), conversation.Metadata)
	}
	conversation.Segments = filterConversationSegments(
		security.WorkspaceID(ctx, userID),
		resolvedGenerationID,
		r.segments[recordutilstore.Key(userID, conversationID)],
	)
	conversation.Metadata = recordutilstore.CloneMetadata(conversation.Metadata)
	if conversation.Metadata == nil {
		conversation.Metadata = map[string]any{}
	}
	conversation.Metadata["memory_generation_id"] = resolvedGenerationID
	return conversation, nil
}

func (r *InMemoryConversationRepository) ListConversations(ctx context.Context, userID string) ([]domain.ConversationRecord, error) {
	workspace, err := security.DataScope(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.ConversationRecord, 0)
	for _, conversation := range r.conversations {
		if conversation.UserID == userID && conversation.WorkspaceID == workspace {
			out = append(out, conversation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (r *InMemoryConversationRepository) ListTimedOutConversations(ctx context.Context, before time.Time, limit int) ([]domain.ConversationRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.ConversationRecord, 0)
	for _, conversation := range r.conversations {
		if conversation.FinalizeStatus == domain.FinalizeStatusActive && conversation.WorkspaceID == security.WorkspaceID(ctx, conversation.UserID) && conversation.UpdatedAt.Before(before) {
			out = append(out, conversation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.Before(out[j].UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *InMemoryConversationRepository) ReplaceSegments(ctx context.Context, userID, conversationID string, segments []domain.ConversationSegment) error {
	if _, err := r.GetConversation(ctx, userID, conversationID); err != nil {
		return err
	}
	for _, segment := range segments {
		if segment.UserID != userID || segment.ConversationID != conversationID {
			return domain.ErrAccessDenied
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	targetGenerationID := recordutilstore.CurrentGenerationID(security.WorkspaceID(ctx, userID))
	if len(segments) > 0 && strings.TrimSpace(segments[0].GenerationID) != "" {
		targetGenerationID = strings.TrimSpace(segments[0].GenerationID)
	} else if conversation, ok := r.conversations[recordutilstore.Key(userID, conversationID)]; ok {
		targetGenerationID = conversationSegmentGeneration(security.WorkspaceID(ctx, userID), conversation.Metadata)
	}
	for _, segment := range segments {
		if segment.GenerationID != "" && segment.GenerationID != targetGenerationID {
			return domain.ErrAccessDenied
		}
	}

	next := make([]domain.ConversationSegment, 0, len(r.segments[recordutilstore.Key(userID, conversationID)])+len(segments))
	for _, existing := range r.segments[recordutilstore.Key(userID, conversationID)] {
		if matchesConversationSegmentGeneration(security.WorkspaceID(ctx, userID), targetGenerationID, existing.GenerationID) {
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
	r.segments[recordutilstore.Key(userID, conversationID)] = next
	if conversation, ok := r.conversations[recordutilstore.Key(userID, conversationID)]; ok {
		if conversation.Metadata == nil {
			conversation.Metadata = map[string]any{}
		}
		conversation.Metadata["memory_generation_id"] = targetGenerationID
		conversation.Segments = filterConversationSegments(security.WorkspaceID(ctx, userID), targetGenerationID, next)
		r.conversations[recordutilstore.Key(userID, conversationID)] = conversation
	}
	return nil
}

func (r *InMemoryConversationRepository) FinalizeConversation(ctx context.Context, input domain.ConversationFinalizeInput) error {
	return r.UpdateConversationState(ctx, domain.ConversationStateUpdate{UserID: input.UserID, ConversationID: input.ConversationID, FinalizeStatus: &input.Status, Metadata: map[string]any{"finalize_reason": input.Reason}})
}

func (r *InMemoryConversationRepository) UpdateConversationState(ctx context.Context, input domain.ConversationStateUpdate) error {
	workspace, err := security.DataScope(ctx, input.UserID, "")
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.conversations[recordutilstore.Key(input.UserID, input.ConversationID)]
	if !ok || c.WorkspaceID != workspace {
		return domain.ErrConversationNotFound
	}
	if err = authorizeConversationWriter(ctx, c); err != nil {
		return err
	}
	r.conversations[recordutilstore.Key(input.UserID, input.ConversationID)] = applyConversationState(c, input)
	return nil
}

func applyConversationState(c domain.ConversationRecord, input domain.ConversationStateUpdate) domain.ConversationRecord {
	if input.FinalizeStatus != nil {
		c.FinalizeStatus = *input.FinalizeStatus
	}
	if input.SummaryStatus != nil {
		c.SummaryStatus = *input.SummaryStatus
	}
	if input.AnalysisStatus != nil {
		c.AnalysisStatus = *input.AnalysisStatus
	}
	metadata := conversationScopeMetadata(c, c.Metadata)
	for k, v := range input.Metadata {
		metadata[k] = v
	}
	c.Metadata = conversationScopeMetadata(c, metadata)
	c.UpdatedAt = input.UpdatedAt
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = time.Now().UTC()
	}
	return c
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

func conversationSegmentGeneration(workspaceID string, metadata map[string]any) string {
	if generationID := recordutilstore.StringMetadataValue(metadata, "memory_generation_id"); generationID != "" {
		return generationID
	}
	return recordutilstore.CurrentGenerationID(workspaceID)
}

func filterConversationSegments(workspaceID, targetGenerationID string, segments []domain.ConversationSegment) []domain.ConversationSegment {
	out := make([]domain.ConversationSegment, 0, len(segments))
	for _, segment := range segments {
		if matchesConversationSegmentGeneration(workspaceID, targetGenerationID, segment.GenerationID) {
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

func matchesConversationSegmentGeneration(workspaceID, targetGenerationID, segmentGenerationID string) bool {
	targetGenerationID = strings.TrimSpace(targetGenerationID)
	if targetGenerationID == "" {
		targetGenerationID = recordutilstore.CurrentGenerationID(workspaceID)
	}
	segmentGenerationID = strings.TrimSpace(segmentGenerationID)
	return segmentGenerationID == targetGenerationID
}

func ShouldKeepExistingSnapshot(existingMetadata, incomingMetadata map[string]any) bool {
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
