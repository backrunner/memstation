package ports

import (
	"context"

	"memstation/apps/server/internal/domain"
)

type QueryEntityExtractor interface {
	ExtractQueryEntities(context.Context, string, string) (domain.QueryEntities, error)
}
