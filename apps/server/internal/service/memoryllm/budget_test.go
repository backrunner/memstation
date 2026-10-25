package memoryllm

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"memstation/apps/server/internal/domain"
)

type budgetAdapter struct {
	recordingAdapter
	responses []domain.UnifiedResponse
	requests  []domain.UnifiedRequest
}

func (a *budgetAdapter) Complete(_ context.Context, r domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	a.requests = append(a.requests, r)
	v := a.responses[len(a.requests)-1]
	return &v, nil
}

func TestBudgetRetriesTruncationWithSameEvidenceAndCountsUsage(t *testing.T) {
	a := &budgetAdapter{responses: []domain.UnifiedResponse{{FinishReason: "length", Message: domain.UnifiedMessage{Text: `{"partial":true}`}, Usage: domain.UsageRecord{InputTokens: 3, OutputTokens: 8192, TotalTokens: 8195}}, {FinishReason: "stop", Message: domain.UnifiedMessage{Text: `{"complete":true}`}, Usage: domain.UsageRecord{InputTokens: 3, OutputTokens: 9000, TotalTokens: 9003}}}}
	req := domain.UnifiedRequest{MaxOutputTokens: 8192, JSONOutput: true, Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "original evidence"}}}
	r, e := CompleteWithBudget(context.Background(), a, req)
	if e != nil || r.Message.Text != `{"complete":true}` || r.Usage.TotalTokens != 17198 || len(a.requests) != 2 || a.requests[1].MaxOutputTokens != 16384 || !reflect.DeepEqual(a.requests[0].Messages, a.requests[1].Messages) {
		t.Fatal(r, e, a.requests)
	}
}

func TestBudgetExhaustionIsBoundedAndNeverAcceptsPartialJSON(t *testing.T) {
	a := &budgetAdapter{responses: []domain.UnifiedResponse{{FinishReason: "length", Message: domain.UnifiedMessage{Text: `{}`}}, {FinishReason: "length", Message: domain.UnifiedMessage{Text: `{}`}}}}
	_, e := CompleteWithBudget(context.Background(), a, domain.UnifiedRequest{MaxOutputTokens: 2048})
	if !errors.Is(e, ErrOutputBudgetExhausted) || len(a.requests) != 2 || a.requests[1].MaxOutputTokens != 8192 {
		t.Fatal(e, a.requests)
	}
}

func TestBudgetDoesNotRetryMalformedStopOrCancelledCalls(t *testing.T) {
	a := &budgetAdapter{responses: []domain.UnifiedResponse{{FinishReason: "stop", Message: domain.UnifiedMessage{Text: "invalid json"}}}}
	_, e := CompleteWithBudget(context.Background(), a, domain.UnifiedRequest{MaxOutputTokens: 4096})
	if e != nil || len(a.requests) != 1 {
		t.Fatal(e, a.requests)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = CompleteWithBudget(ctx, a, domain.UnifiedRequest{MaxOutputTokens: 4096})
	if !errors.Is(e, context.Canceled) || len(a.requests) != 1 {
		t.Fatal(e)
	}
}
