package aibroker

import (
	"context"
	"errors"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
)

var ErrInvalidUsageQuery = errors.New("invalid usage query")

func (s *UsageService) Report(ctx context.Context, q domain.AIUsageQuery) (domain.AIUsageReport, error) {
	if err := security.RequireCapability(ctx, domain.CapabilityUsageRead); err != nil {
		return domain.AIUsageReport{}, err
	}
	var err error
	q, err = normalizeUsageQuery(q)
	if err != nil {
		return domain.AIUsageReport{}, err
	}

	r, ok := s.ledger.(ports.AIUsageReporter)
	if !ok {
		return domain.AIUsageReport{}, errors.New("usage reporting unavailable")
	}
	out, err := r.AIUsageReport(ctx, q)
	if err != nil {
		return out, err
	}
	if err = security.RequireCapability(ctx, domain.CapabilityUsageRead); err != nil {
		return domain.AIUsageReport{}, err
	}
	return out, nil
}

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
	if call.UsageAuthority != "broker" {
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

func (s *UsageService) FilteredList(ctx context.Context, q domain.AIUsageQuery, after string, limit int) ([]domain.AICall, error) {
	if err := security.RequireCapability(ctx, domain.CapabilityUsageRead); err != nil {
		return nil, err
	}
	r, ok := s.ledger.(interface {
		FilteredAICalls(context.Context, domain.AIUsageQuery, string, int) ([]domain.AICall, error)
	})
	if !ok {
		return nil, errors.New("filtered usage unavailable")
	}
	var err error
	q, err = normalizeUsageQuery(q)
	if err != nil {
		return nil, err
	}
	return r.FilteredAICalls(ctx, q, after, limit)
}

func normalizeUsageQuery(q domain.AIUsageQuery) (domain.AIUsageQuery, error) {
	if q.Until.IsZero() {
		q.Until = time.Now().UTC()
	}
	if q.Since.IsZero() {
		q.Since = q.Until.Add(-30 * 24 * time.Hour)
	}
	if !q.Until.After(q.Since) || q.Until.Sub(q.Since) > 366*24*time.Hour || len(q.Stage) > 120 || len(q.Model) > 256 || len(q.Strategy) > 32 || len(q.Status) > 32 {
		return q, ErrInvalidUsageQuery
	}
	return q, nil
}
