package ports

import (
	"context"

	"memstation/apps/server/internal/domain"
)

type ConversationBrowseRepository interface {
	BrowseConversations(context.Context, domain.RecordBrowseQuery, *domain.RecordPosition) ([]domain.ConversationListItem, error)
}

type KeyMemoryBrowseRepository interface {
	BrowseKeyMemories(context.Context, domain.RecordBrowseQuery, *domain.RecordPosition) ([]domain.KeyMemory, error)
}
