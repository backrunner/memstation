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
	r, e := CompleteWithBudget(context.Background(), a, req, DefaultMaxOutputTokens)
	if e != nil || r.Message.Text != `{"complete":true}` || r.Usage.TotalTokens != 17198 || len(a.requests) != 2 || a.requests[1].MaxOutputTokens != DefaultMaxOutputTokens || !reflect.DeepEqual(a.requests[0].Messages, a.requests[1].Messages) {
		t.Fatal(r, e, a.requests)
	}
}

func TestBudgetExhaustionIsBoundedAndNeverAcceptsPartialJSON(t *testing.T) {
	a := &budgetAdapter{responses: []domain.UnifiedResponse{{FinishReason: "length", Message: domain.UnifiedMessage{Text: `{}`}}, {FinishReason: "length", Message: domain.UnifiedMessage{Text: `{}`}}}}
	_, e := CompleteWithBudget(context.Background(), a, domain.UnifiedRequest{MaxOutputTokens: 2048}, 8192)
	if !errors.Is(e, ErrOutputBudgetExhausted) || len(a.requests) != 2 || a.requests[1].MaxOutputTokens != 8192 {
		t.Fatal(e, a.requests)
	}
}

func TestBudgetDoesNotRetryMalformedStopOrCancelledCalls(t *testing.T) {
	a := &budgetAdapter{responses: []domain.UnifiedResponse{{FinishReason: "stop", Message: domain.UnifiedMessage{Text: "invalid json"}}}}
	_, e := CompleteWithBudget(context.Background(), a, domain.UnifiedRequest{MaxOutputTokens: 4096}, DefaultMaxOutputTokens)
	if e != nil || len(a.requests) != 1 {
		t.Fatal(e, a.requests)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = CompleteWithBudget(ctx, a, domain.UnifiedRequest{MaxOutputTokens: 4096}, DefaultMaxOutputTokens)
	if !errors.Is(e, context.Canceled) || len(a.requests) != 1 {
		t.Fatal(e)
	}
}

func TestConfiguredLargeBudgetNeverSilentlyShrinksAndStaysBounded(t *testing.T) {
	a := &budgetAdapter{responses: []domain.UnifiedResponse{{FinishReason: "length"}, {FinishReason: "stop", Message: domain.UnifiedMessage{Text: `{"complete":true}`}}}}
	_, err := CompleteWithBudget(context.Background(), a, domain.UnifiedRequest{MaxOutputTokens: 32768}, 131072)
	if err != nil || len(a.requests) != 2 || a.requests[0].MaxOutputTokens != 32768 || a.requests[1].MaxOutputTokens != 131072 {
		t.Fatal(err, a.requests)
	}
	a.requests = nil
	if _, err = CompleteWithBudget(context.Background(), a, domain.UnifiedRequest{MaxOutputTokens: 131073}, 131072); err == nil || len(a.requests) != 0 {
		t.Fatal("oversized request silently lowered", err, a.requests)
	}
}

// A failed retry still consumed the first response's output budget. Callers
// retain its usage for diagnostics but must never accept its partial answer.
type failedBudgetRetryAdapter struct {
	recordingAdapter
	calls   int
	cancel  context.CancelFunc
	failure error
}

func (a *failedBudgetRetryAdapter) Complete(_ context.Context, _ domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	a.calls++
	if a.calls == 1 {
		if a.cancel != nil {
			a.cancel()
		}
		return &domain.UnifiedResponse{FinishReason: "length", Usage: domain.UsageRecord{InputTokens: 3, OutputTokens: 8, TotalTokens: 11}}, nil
	}
	return nil, a.failure
}

func TestBudgetPreservesUsageWhenRetryFails(t *testing.T) {
	for _, mode := range []string{"transport", "cancelled", "empty"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := &failedBudgetRetryAdapter{}
			switch mode {
			case "transport":
				a.failure = context.DeadlineExceeded
			case "cancelled":
				a.cancel = cancel
			}
			response, err := CompleteWithBudget(ctx, a, domain.UnifiedRequest{MaxOutputTokens: 8}, 16)
			if err == nil || response == nil || response.Usage.TotalTokens != 11 || response.FinishReason != "length" {
				t.Fatalf("lost consumed usage or accepted incomplete response: %#v, %v", response, err)
			}
			if mode == "cancelled" && (!errors.Is(err, context.Canceled) || a.calls != 1) {
				t.Fatal(err, a.calls)
			}
			if mode == "transport" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}

func TestBudgetRejectsNilSuccessfulResponse(t *testing.T) {
	a := &failedBudgetRetryAdapter{calls: 1}
	response, err := CompleteWithBudget(context.Background(), a, domain.UnifiedRequest{MaxOutputTokens: 8}, 16)
	if response != nil || err == nil || a.calls != 2 {
		t.Fatal(response, err, a.calls)
	}
}
