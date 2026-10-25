package ports

import (
	"context"

	"memstation/apps/server/internal/domain"
)

type MemoryControlRepository interface {
	Overview(context.Context, string, int) (domain.MemoryControlOverview, error)
	ReplayJob(context.Context, string, func(string, string) error) error
}
