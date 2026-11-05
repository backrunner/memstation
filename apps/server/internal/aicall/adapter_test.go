package aicall

import (
	"context"
	"errors"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

type fixtureLedger struct{ calls []domain.AICall }

func (l *fixtureLedger) StartAICall(context.Context, domain.AICall) error { return nil }
func (l *fixtureLedger) FinishAICall(_ context.Context, c domain.AICall) error {
	l.calls = append(l.calls, c)
	return nil
}

func (l *fixtureLedger) AICall(context.Context, string) (domain.AICall, error) {
	return domain.AICall{}, nil
}

func (l *fixtureLedger) AICalls(context.Context, string, int) ([]domain.AICall, error) {
	return l.calls, nil
}

func (l *fixtureLedger) EmbeddingIdentity(context.Context, string, string, int, domain.EmbeddingIdentity) (domain.EmbeddingIdentity, error) {
	return domain.EmbeddingIdentity{}, nil
}

type fixtureAdapter struct {
	response *domain.UnifiedResponse
	err      error
}

func (f fixtureAdapter) Name() domain.Provider { return domain.ProviderOpenAICompatible }
func (f fixtureAdapter) Complete(context.Context, domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	return f.response, f.err
}

func (f fixtureAdapter) Stream(context.Context, domain.UnifiedRequest, func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
	return nil, nil
}

func TestDirectUsageKeepsUnknownSeparateFromZeroAndRetainsAttempts(t *testing.T) {
	ctx, err := security.WithPersonalDataScope(context.Background(), "owner", domain.PersonalWorkspaceID("owner"))
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithJob(WithMetadata(ctx, "analysis.extract", "hybrid"), "job", 2)
	ledger := &fixtureLedger{}
	adapter := Adapter{Ledger: ledger, Base: fixtureAdapter{response: &domain.UnifiedResponse{ResponseID: "upstream", Usage: domain.UsageRecord{InputTokens: 123, OutputTokens: 20}}}}
	if _, err = adapter.Complete(ctx, domain.UnifiedRequest{UserID: "owner", Model: "configured"}); err != nil {
		t.Fatal(err)
	}
	adapter.Base = fixtureAdapter{err: errors.New("timeout")}
	if _, err = adapter.Complete(ctx, domain.UnifiedRequest{UserID: "owner", Model: "configured"}); err == nil {
		t.Fatal("upstream error swallowed")
	}
	if len(ledger.calls) != 2 || ledger.calls[0].ID == ledger.calls[1].ID || !ledger.calls[0].UsageKnown || ledger.calls[1].UsageKnown || ledger.calls[1].Status != "unknown" {
		t.Fatalf("incorrect attempt ledger %+v", ledger.calls)
	}
	for _, c := range ledger.calls {
		if c.Stage != "analysis.extract" || c.Strategy != "hybrid" || c.Attempt != 2 || c.ReportedCost != "" {
			t.Fatalf("lost policy/attempt or invented price %+v", c)
		}
	}
}
