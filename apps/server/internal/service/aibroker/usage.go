package aibroker

import (
	"context"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
)

type UsageService struct {
	ledger ports.AICallRepository
	broker *Client
}

func NewUsageService(ledger ports.AICallRepository, clients ...*Client) *UsageService {
	s := &UsageService{ledger: ledger}
	if len(clients) > 0 {
		s.broker = clients[0]
	}
	return s
}

func (s *UsageService) List(ctx context.Context, after string, limit int) ([]domain.AICall, error) {
	if err := security.RequireCapability(ctx, domain.CapabilityUsageRead); err != nil {
		return nil, err
	}
	out, err := s.ledger.AICalls(ctx, after, limit)
	if err != nil {
		return nil, err
	}
	if err = security.RequireCapability(ctx, domain.CapabilityUsageRead); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *UsageService) Reconcile(ctx context.Context, id string) (domain.AICall, error) {
	if err := security.RequireCapability(ctx, domain.CapabilityUsageRead); err != nil {
		return domain.AICall{}, err
	}
	call, err := s.ledger.AICall(ctx, id)
	if err != nil {
		return call, err
	}
	if call.Status != "unknown" {
		return call, nil
	}
	if s.broker == nil {
		return domain.AICall{}, domain.ErrAccessDenied
	}
	call, err = s.broker.reconcile(ctx, call)
	if err != nil {
		return domain.AICall{}, err
	}
	if err = security.RequireCapability(ctx, domain.CapabilityUsageRead); err != nil {
		return domain.AICall{}, err
	}
	return call, nil
}
