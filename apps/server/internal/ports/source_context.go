package ports

import (
	"context"

	"memstation/apps/server/internal/domain"
)

// SourceContextReader reads only retained source turns from an authorized
// conversation. Original sources are generation independent; the memory engine
// validates the anchor node's generation before invoking this reader.
type SourceContextReader interface {
	ReadSourceWindow(context.Context, domain.SourceWindowInput) ([]domain.SourceTurn, error)
}
