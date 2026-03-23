package usage

import (
	"context"
	sqlpkg "database/sql"

	"memstation/apps/server/internal/ports"
)

type Service struct {
	db            *sqlpkg.DB
	conversations ports.ConversationRepository
}

func NewService(db *sqlpkg.DB, conversations ports.ConversationRepository) *Service {
	return &Service{db: db, conversations: conversations}
}

func (s *Service) Totals(ctx context.Context, userID string) (map[string]int, error) {
	if s.db != nil {
		row := s.db.QueryRowContext(ctx, `
            SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COALESCE(SUM(total_tokens), 0)
            FROM token_usages
            WHERE user_id = $1
        `, userID)
		var inputTokens int
		var outputTokens int
		var totalTokens int
		if err := row.Scan(&inputTokens, &outputTokens, &totalTokens); err != nil {
			return nil, err
		}
		return map[string]int{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
			"total_tokens":  totalTokens,
		}, nil
	}

	conversations, err := s.conversations.ListConversations(ctx, userID)
	if err != nil {
		return nil, err
	}
	totals := map[string]int{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}
	for _, conversation := range conversations {
		totals["input_tokens"] += conversation.Usage.InputTokens
		totals["output_tokens"] += conversation.Usage.OutputTokens
		totals["total_tokens"] += conversation.Usage.TotalTokens
	}
	return totals, nil
}
