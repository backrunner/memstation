package conversation

import (
	"context"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
)

type HybridConversationRepository struct {
	db       runtimeconfig.DBHandle
	fallback *InMemoryConversationRepository
}

func NewHybridConversationRepository(db runtimeconfig.DBHandle) *HybridConversationRepository {
	return &HybridConversationRepository{
		db:       db,
		fallback: NewInMemoryConversationRepository(),
	}
}

func (r *HybridConversationRepository) PersistExchange(ctx context.Context, conversation domain.ConversationRecord) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresConversationRepository(db).PersistExchange(ctx, conversation)
	}
	return r.fallback.PersistExchange(ctx, conversation)
}

func (r *HybridConversationRepository) GetConversation(ctx context.Context, userID, conversationID string) (domain.ConversationRecord, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresConversationRepository(db).GetConversation(ctx, userID, conversationID)
	}
	return r.fallback.GetConversation(ctx, userID, conversationID)
}

func (r *HybridConversationRepository) GetConversationInGeneration(ctx context.Context, userID, conversationID, generationID string) (domain.ConversationRecord, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresConversationRepository(db).GetConversationInGeneration(ctx, userID, conversationID, generationID)
	}
	return r.fallback.GetConversationInGeneration(ctx, userID, conversationID, generationID)
}

func (r *HybridConversationRepository) ListConversations(ctx context.Context, userID string) ([]domain.ConversationRecord, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresConversationRepository(db).ListConversations(ctx, userID)
	}
	return r.fallback.ListConversations(ctx, userID)
}

func (r *HybridConversationRepository) ListTimedOutConversations(ctx context.Context, before time.Time, limit int) ([]domain.ConversationRecord, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresConversationRepository(db).ListTimedOutConversations(ctx, before, limit)
	}
	return r.fallback.ListTimedOutConversations(ctx, before, limit)
}

func (r *HybridConversationRepository) ReplaceSegments(ctx context.Context, userID, conversationID string, segments []domain.ConversationSegment) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresConversationRepository(db).ReplaceSegments(ctx, userID, conversationID, segments)
	}
	return r.fallback.ReplaceSegments(ctx, userID, conversationID, segments)
}

func (r *HybridConversationRepository) FinalizeConversation(ctx context.Context, input domain.ConversationFinalizeInput) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresConversationRepository(db).FinalizeConversation(ctx, input)
	}
	return r.fallback.FinalizeConversation(ctx, input)
}

func (r *HybridConversationRepository) UpdateConversationState(ctx context.Context, input domain.ConversationStateUpdate) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresConversationRepository(db).UpdateConversationState(ctx, input)
	}
	return r.fallback.UpdateConversationState(ctx, input)
}

type HybridOutboxRepository struct {
	db       runtimeconfig.DBHandle
	fallback *InMemoryOutboxRepository
}

func NewHybridOutboxRepository(db runtimeconfig.DBHandle) *HybridOutboxRepository {
	return &HybridOutboxRepository{
		db:       db,
		fallback: NewInMemoryOutboxRepository(),
	}
}

func (r *HybridOutboxRepository) Enqueue(ctx context.Context, event domain.OutboxEvent) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresOutboxRepository(db).Enqueue(ctx, event)
	}
	return r.fallback.Enqueue(ctx, event)
}

func (r *HybridOutboxRepository) Pending(ctx context.Context, limit int) ([]domain.OutboxEvent, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresOutboxRepository(db).Pending(ctx, limit)
	}
	return r.fallback.Pending(ctx, limit)
}

func (r *HybridOutboxRepository) MarkProcessed(ctx context.Context, id string) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresOutboxRepository(db).MarkProcessed(ctx, id)
	}
	return r.fallback.MarkProcessed(ctx, id)
}
