package ports

import (
	"context"

	"memstation/apps/server/internal/domain"
)

type AICallRepository interface {
	StartAICall(context.Context, domain.AICall) error
	FinishAICall(context.Context, domain.AICall) error
	AICall(context.Context, string) (domain.AICall, error)
	AICalls(context.Context, string, int) ([]domain.AICall, error)
	EmbeddingIdentity(context.Context, string, string, int, domain.EmbeddingIdentity) (domain.EmbeddingIdentity, error)
}
type MemoryModelRuntime interface {
	Adapter(string) (ProviderAdapter, string, error)
}
