package memory

import (
	"context"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/service/browse"
)

func (s *Service) BrowseKeyMemories(ctx context.Context, input domain.RecordBrowseQuery) (domain.RecordPage[domain.KeyMemory], error) {
	position, scope, err := browse.Prepare(ctx, "key_memories", &input)
	if err != nil {
		return domain.RecordPage[domain.KeyMemory]{}, err
	}
	repo, ok := s.repo.(ports.KeyMemoryBrowseRepository)
	if !ok {
		return domain.RecordPage[domain.KeyMemory]{}, errors.New("key memory browsing unavailable")
	}
	limit := input.Limit
	input.Limit++
	items, err := repo.BrowseKeyMemories(ctx, input, position)
	if err != nil {
		return domain.RecordPage[domain.KeyMemory]{}, err
	}
	return browse.Page(items, limit, scope, func(item domain.KeyMemory) domain.RecordPosition {
		return domain.RecordPosition{UpdatedAt: item.UpdatedAt, ID: item.ID}
	}), nil
}
