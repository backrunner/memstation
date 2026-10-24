package ports

import (
	"context"

	"memstation/apps/server/internal/domain"
)

type MemoryBrowseRepository interface {
	BrowseDocuments(context.Context, domain.MemoryBrowseQuery, *domain.MemoryPosition) ([]domain.MemoryDocument, error)
}
