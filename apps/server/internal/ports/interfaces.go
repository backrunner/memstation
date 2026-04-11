package ports

import (
	"context"
	"time"

	"memstation/apps/server/internal/domain"
)

type ProviderAdapter interface {
	Name() domain.Provider
	Complete(ctx context.Context, req domain.UnifiedRequest) (*domain.UnifiedResponse, error)
	Stream(ctx context.Context, req domain.UnifiedRequest, emit func(domain.StreamEvent) error) (*domain.UnifiedResponse, error)
}

type EmbeddingClient interface {
	Embed(ctx context.Context, input domain.EmbeddingInput) (domain.EmbeddingResult, error)
}

type VectorStore interface {
	Search(ctx context.Context, input domain.VectorSearchInput) ([]domain.VectorSearchResult, error)
	Upsert(ctx context.Context, input domain.VectorUpsertRequest) error
	DeleteByNode(ctx context.Context, userID, nodeID string) error
	Health(ctx context.Context) error
	ReindexVersion(ctx context.Context, version int) error
}

type ConversationRepository interface {
	PersistExchange(ctx context.Context, conversation domain.ConversationRecord) error
	GetConversation(ctx context.Context, userID, conversationID string) (domain.ConversationRecord, error)
	GetConversationInGeneration(ctx context.Context, userID, conversationID, generationID string) (domain.ConversationRecord, error)
	ListConversations(ctx context.Context, userID string) ([]domain.ConversationRecord, error)
	ListTimedOutConversations(ctx context.Context, before time.Time, limit int) ([]domain.ConversationRecord, error)
	ReplaceSegments(ctx context.Context, userID, conversationID string, segments []domain.ConversationSegment) error
	FinalizeConversation(ctx context.Context, input domain.ConversationFinalizeInput) error
	UpdateConversationState(ctx context.Context, input domain.ConversationStateUpdate) error
}

type MemoryRepository interface {
	Search(ctx context.Context, input domain.SearchInput) (domain.SearchResult, error)
	StoreDocument(ctx context.Context, doc domain.MemoryDocument) error
	DeleteDocument(ctx context.Context, userID, id string, kind domain.MemoryType) error
	DeleteSegmentsByConversation(ctx context.Context, userID, conversationID string, keepIDs []string) ([]string, error)
	DeleteSegmentsByConversationInGeneration(ctx context.Context, userID, generationID, conversationID string, keepIDs []string) ([]string, error)
	GetDocument(ctx context.Context, userID, id string, kind domain.MemoryType) (domain.MemoryDocument, error)
	GetDocumentInGeneration(ctx context.Context, userID, generationID, id string, kind domain.MemoryType) (domain.MemoryDocument, error)
	ListDocuments(ctx context.Context, userID string, memoryTypes []domain.MemoryType, limit int) ([]domain.MemoryDocument, error)
	ListDocumentsInGeneration(ctx context.Context, userID, generationID string, memoryTypes []domain.MemoryType, limit int) ([]domain.MemoryDocument, error)
	ListDocumentsByNodeIDs(ctx context.Context, userID string, nodeIDs []string) ([]domain.MemoryDocument, error)
	ListDocumentsByNodeIDsInGeneration(ctx context.Context, userID, generationID string, nodeIDs []string) ([]domain.MemoryDocument, error)
	ListDocumentsByConversation(ctx context.Context, userID, conversationID string) ([]domain.MemoryDocument, error)
	ListDocumentsByConversationInGeneration(ctx context.Context, userID, generationID, conversationID string) ([]domain.MemoryDocument, error)
	ListConversationDocumentsInGeneration(ctx context.Context, userID, generationID, conversationID string, includeSegments bool) ([]domain.MemoryDocument, error)
	SearchKeywordMatches(ctx context.Context, userID, generationID, query string, limit int) ([]domain.KeywordMatch, error)
	UpdateDocument(ctx context.Context, userID, id string, kind domain.MemoryType, content string) (domain.MemoryDocument, error)
	ListKeyMemories(ctx context.Context, userID string, limit int) ([]domain.KeyMemory, error)
	SearchKeyMemories(ctx context.Context, input domain.KeyMemoryQuery) ([]domain.KeyMemory, error)
	UpsertKeyMemory(ctx context.Context, km domain.KeyMemory) error
	DeleteKeyMemory(ctx context.Context, userID, key string) error
	LinkDocuments(ctx context.Context, relation domain.MemoryRelation) error
	DeleteRelationsByTarget(ctx context.Context, userID, targetNodeID, relationType string) error
	ListRelations(ctx context.Context, userID, generationID string, nodeIDs []string, depth int) ([]domain.MemoryRelation, error)
}

type OutboxRepository interface {
	Enqueue(ctx context.Context, event domain.OutboxEvent) error
	Pending(ctx context.Context, limit int) ([]domain.OutboxEvent, error)
	MarkProcessed(ctx context.Context, id string) error
}

type TaskPublisher interface {
	Publish(ctx context.Context, task domain.TaskMessage) error
}

type MemoryEngine interface {
	Inject(ctx context.Context, req domain.UnifiedRequest) (domain.UnifiedRequest, domain.InjectionResult, error)
	Search(ctx context.Context, input domain.SearchInput) (domain.SearchResult, error)
}

type Encryptor interface {
	Encrypt(ctx context.Context, plaintext []byte) (string, error)
	Decrypt(ctx context.Context, ciphertext string) ([]byte, error)
}
