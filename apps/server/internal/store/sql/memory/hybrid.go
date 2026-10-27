package memory

import (
	"context"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
)

type HybridMemoryRepository struct {
	db       runtimeconfig.DBHandle
	fallback *InMemoryMemoryRepository
}

func (r *HybridMemoryRepository) HasTransactionalMemoryOutbox() bool {
	return dbutilstore.CurrentDB(r.db) != nil
}

func NewHybridMemoryRepository(db runtimeconfig.DBHandle) *HybridMemoryRepository {
	return &HybridMemoryRepository{
		db:       db,
		fallback: NewInMemoryMemoryRepository(),
	}
}

func (r *HybridMemoryRepository) Search(ctx context.Context, input domain.SearchInput) (domain.SearchResult, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).Search(ctx, input)
	}
	return r.fallback.Search(ctx, input)
}

func (r *HybridMemoryRepository) StoreDocument(ctx context.Context, doc domain.MemoryDocument) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).StoreDocument(ctx, doc)
	}
	return r.fallback.StoreDocument(ctx, doc)
}

func (r *HybridMemoryRepository) DeleteDocument(ctx context.Context, userID, id string, kind domain.MemoryType) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).DeleteDocument(ctx, userID, id, kind)
	}
	return r.fallback.DeleteDocument(ctx, userID, id, kind)
}

func (r *HybridMemoryRepository) DeleteSegmentsByConversation(ctx context.Context, userID, conversationID string, keepIDs []string) ([]string, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).DeleteSegmentsByConversation(ctx, userID, conversationID, keepIDs)
	}
	return r.fallback.DeleteSegmentsByConversation(ctx, userID, conversationID, keepIDs)
}

func (r *HybridMemoryRepository) DeleteSegmentsByConversationInGeneration(ctx context.Context, userID, generationID, conversationID string, keepIDs []string) ([]string, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).DeleteSegmentsByConversationInGeneration(ctx, userID, generationID, conversationID, keepIDs)
	}
	return r.fallback.DeleteSegmentsByConversationInGeneration(ctx, userID, generationID, conversationID, keepIDs)
}

func (r *HybridMemoryRepository) GetDocument(ctx context.Context, userID, id string, kind domain.MemoryType) (domain.MemoryDocument, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).GetDocument(ctx, userID, id, kind)
	}
	return r.fallback.GetDocument(ctx, userID, id, kind)
}

func (r *HybridMemoryRepository) GetDocumentInGeneration(ctx context.Context, userID, generationID, id string, kind domain.MemoryType) (domain.MemoryDocument, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).GetDocumentInGeneration(ctx, userID, generationID, id, kind)
	}
	return r.fallback.GetDocumentInGeneration(ctx, userID, generationID, id, kind)
}

func (r *HybridMemoryRepository) ListDocuments(ctx context.Context, userID string, memoryTypes []domain.MemoryType, limit int) ([]domain.MemoryDocument, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListDocuments(ctx, userID, memoryTypes, limit)
	}
	return r.fallback.ListDocuments(ctx, userID, memoryTypes, limit)
}

func (r *HybridMemoryRepository) ListDocumentsInGeneration(ctx context.Context, userID, generationID string, memoryTypes []domain.MemoryType, limit int) ([]domain.MemoryDocument, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListDocumentsInGeneration(ctx, userID, generationID, memoryTypes, limit)
	}
	return r.fallback.ListDocumentsInGeneration(ctx, userID, generationID, memoryTypes, limit)
}

func (r *HybridMemoryRepository) ListDocumentsByNodeIDs(ctx context.Context, userID string, nodeIDs []string) ([]domain.MemoryDocument, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListDocumentsByNodeIDs(ctx, userID, nodeIDs)
	}
	return r.fallback.ListDocumentsByNodeIDs(ctx, userID, nodeIDs)
}

func (r *HybridMemoryRepository) ListDocumentsByNodeIDsInGeneration(ctx context.Context, userID, generationID string, nodeIDs []string) ([]domain.MemoryDocument, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListDocumentsByNodeIDsInGeneration(ctx, userID, generationID, nodeIDs)
	}
	return r.fallback.ListDocumentsByNodeIDsInGeneration(ctx, userID, generationID, nodeIDs)
}

func (r *HybridMemoryRepository) ListDocumentsByConversation(ctx context.Context, userID, conversationID string) ([]domain.MemoryDocument, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListDocumentsByConversation(ctx, userID, conversationID)
	}
	return r.fallback.ListDocumentsByConversation(ctx, userID, conversationID)
}

func (r *HybridMemoryRepository) ListDocumentsByConversationInGeneration(ctx context.Context, userID, generationID, conversationID string) ([]domain.MemoryDocument, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListDocumentsByConversationInGeneration(ctx, userID, generationID, conversationID)
	}
	return r.fallback.ListDocumentsByConversationInGeneration(ctx, userID, generationID, conversationID)
}

func (r *HybridMemoryRepository) ListConversationDocumentsInGeneration(ctx context.Context, userID, generationID, conversationID string, includeSegments bool) ([]domain.MemoryDocument, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListConversationDocumentsInGeneration(ctx, userID, generationID, conversationID, includeSegments)
	}
	return r.fallback.ListConversationDocumentsInGeneration(ctx, userID, generationID, conversationID, includeSegments)
}

func (r *HybridMemoryRepository) SearchKeywordMatches(ctx context.Context, userID, generationID, query string, limit int, allowedTypes ...domain.MemoryType) ([]domain.KeywordMatch, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).SearchKeywordMatches(ctx, userID, generationID, query, limit, allowedTypes...)
	}
	return r.fallback.SearchKeywordMatches(ctx, userID, generationID, query, limit, allowedTypes...)
}

func (r *HybridMemoryRepository) UpdateDocument(ctx context.Context, userID, id string, kind domain.MemoryType, content string) (domain.MemoryDocument, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).UpdateDocument(ctx, userID, id, kind, content)
	}
	return r.fallback.UpdateDocument(ctx, userID, id, kind, content)
}

func (r *HybridMemoryRepository) ListFactLineage(ctx context.Context, userID, generationID, factID string) (domain.FactLineage, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListFactLineage(ctx, userID, generationID, factID)
	}
	return r.fallback.ListFactLineage(ctx, userID, generationID, factID)
}

func (r *HybridMemoryRepository) ListFactLineageByKey(ctx context.Context, userID, generationID, factKey string) (domain.FactLineage, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListFactLineageByKey(ctx, userID, generationID, factKey)
	}
	return r.fallback.ListFactLineageByKey(ctx, userID, generationID, factKey)
}

func (r *HybridMemoryRepository) ListKeyMemories(ctx context.Context, userID string, limit int) ([]domain.KeyMemory, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListKeyMemories(ctx, userID, limit)
	}
	return r.fallback.ListKeyMemories(ctx, userID, limit)
}

func (r *HybridMemoryRepository) GetKeyMemory(ctx context.Context, userID, key string) (domain.KeyMemory, bool, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).GetKeyMemory(ctx, userID, key)
	}
	return r.fallback.GetKeyMemory(ctx, userID, key)
}

func (r *HybridMemoryRepository) GetKeyMemoryByID(ctx context.Context, userID, id string) (domain.KeyMemory, bool, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).GetKeyMemoryByID(ctx, userID, id)
	}
	return r.fallback.GetKeyMemoryByID(ctx, userID, id)
}

func (r *HybridMemoryRepository) CountKeyMemories(ctx context.Context, userID string) (int, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).CountKeyMemories(ctx, userID)
	}
	return r.fallback.CountKeyMemories(ctx, userID)
}

func (r *HybridMemoryRepository) SearchKeyMemories(ctx context.Context, input domain.KeyMemoryQuery) ([]domain.KeyMemory, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).SearchKeyMemories(ctx, input)
	}
	return r.fallback.SearchKeyMemories(ctx, input)
}

func (r *HybridMemoryRepository) UpsertKeyMemory(ctx context.Context, km domain.KeyMemory) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).UpsertKeyMemory(ctx, km)
	}
	return r.fallback.UpsertKeyMemory(ctx, km)
}

func (r *HybridMemoryRepository) DeleteKeyMemory(ctx context.Context, userID, key string) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).DeleteKeyMemory(ctx, userID, key)
	}
	return r.fallback.DeleteKeyMemory(ctx, userID, key)
}

func (r *HybridMemoryRepository) LinkDocuments(ctx context.Context, relation domain.MemoryRelation) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).LinkDocuments(ctx, relation)
	}
	return r.fallback.LinkDocuments(ctx, relation)
}

func (r *HybridMemoryRepository) DeleteRelationsByTarget(ctx context.Context, userID, targetNodeID, relationType string) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).DeleteRelationsByTarget(ctx, userID, targetNodeID, relationType)
	}
	return r.fallback.DeleteRelationsByTarget(ctx, userID, targetNodeID, relationType)
}

func (r *HybridMemoryRepository) ListRelations(ctx context.Context, userID, generationID string, nodeIDs []string, depth int) ([]domain.MemoryRelation, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListRelations(ctx, userID, generationID, nodeIDs, depth)
	}
	return r.fallback.ListRelations(ctx, userID, generationID, nodeIDs, depth)
}

func (r *HybridMemoryRepository) ReplaceEvidences(ctx context.Context, userID, nodeID string, evidences []domain.MemoryEvidence) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ReplaceEvidences(ctx, userID, nodeID, evidences)
	}
	return r.fallback.ReplaceEvidences(ctx, userID, nodeID, evidences)
}

func (r *HybridMemoryRepository) MergeEvidences(ctx context.Context, userID, nodeID string, evidences []domain.MemoryEvidence) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).MergeEvidences(ctx, userID, nodeID, evidences)
	}
	return r.fallback.MergeEvidences(ctx, userID, nodeID, evidences)
}

func (r *HybridMemoryRepository) ListEvidences(ctx context.Context, userID, nodeID string) ([]domain.MemoryEvidence, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListEvidences(ctx, userID, nodeID)
	}
	return r.fallback.ListEvidences(ctx, userID, nodeID)
}

func (r *HybridMemoryRepository) ListEvidencesByNodeIDs(ctx context.Context, userID string, nodeIDs []string) ([]domain.MemoryEvidence, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListEvidencesByNodeIDs(ctx, userID, nodeIDs)
	}
	return r.fallback.ListEvidencesByNodeIDs(ctx, userID, nodeIDs)
}
