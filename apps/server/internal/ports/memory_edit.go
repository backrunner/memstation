package ports

import (
	"context"

	"memstation/apps/server/internal/domain"
)

// ConditionalMemoryRepository commits the precondition, mutation, audit and
// durable embedding work together. It must not fall back to an unchecked write.
type ConditionalMemoryRepository interface {
	UpdateDocumentConditional(context.Context, string, string, domain.MemoryType, string, domain.MemoryWriteCondition) (domain.MemoryDocument, error)
	UpsertKeyMemoryConditional(context.Context, domain.KeyMemory, domain.MemoryWriteCondition) error
	DeleteKeyMemoryConditional(context.Context, string, string, domain.MemoryWriteCondition) error
}
