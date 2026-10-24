package admin

import (
	"context"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/service/browse"
)

func (s *Service) BrowseConversations(ctx context.Context, input domain.RecordBrowseQuery) (domain.RecordPage[domain.ConversationListItem], error) {
	position, scope, err := browse.Prepare(ctx, "conversations", &input)
	if err != nil {
		return domain.RecordPage[domain.ConversationListItem]{}, err
	}
	repo, ok := s.conversations.(ports.ConversationBrowseRepository)
	if !ok {
		return domain.RecordPage[domain.ConversationListItem]{}, errors.New("conversation browsing unavailable")
	}
	limit := input.Limit
	input.Limit++
	items, err := repo.BrowseConversations(ctx, input, position)
	if err != nil {
		return domain.RecordPage[domain.ConversationListItem]{}, err
	}
	return browse.Page(items, limit, scope, func(item domain.ConversationListItem) domain.RecordPosition {
		return domain.RecordPosition{UpdatedAt: item.UpdatedAt, ID: item.ConversationID}
	}), nil
}
