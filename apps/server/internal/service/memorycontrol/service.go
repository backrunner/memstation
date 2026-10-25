package memorycontrol

import (
	"context"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
)

type Service struct {
	repo   ports.MemoryControlRepository
	replay func(string, string) error
}

func NewService(repo ports.MemoryControlRepository, replay func(string, string) error) *Service {
	return &Service{repo: repo, replay: replay}
}

func (s *Service) Overview(ctx context.Context, after string, limit int) (domain.MemoryControlOverview, error) {
	if !security.AdminFromContext(ctx) {
		return domain.MemoryControlOverview{}, domain.ErrAccessDenied
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repo.Overview(ctx, after, limit)
}

func (s *Service) Replay(ctx context.Context, id string) error {
	if !security.AdminFromContext(ctx) || s.replay == nil {
		return domain.ErrAccessDenied
	}
	return s.repo.ReplayJob(ctx, id, s.replay)
}
