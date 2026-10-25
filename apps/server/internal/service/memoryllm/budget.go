package memoryllm

import (
	"context"
	"errors"
	"fmt"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

var ErrOutputBudgetExhausted = errors.New("memory LLM output budget exhausted")

// CompleteWithBudget retries only explicit output truncation, once, with a
// larger bounded budget. Reasoning models can spend their entire output budget
// before producing any JSON. Never accept a partial result or repair it locally.
// Both attempts share the caller's deadline; reported usage includes both.
func CompleteWithBudget(ctx context.Context, adapter ports.ProviderAdapter, request domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	if request.MaxOutputTokens <= 0 || request.MaxOutputTokens > 16384 {
		return nil, errors.New("invalid bounded LLM output budget")
	}
	usage := domain.UsageRecord{}
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, err := adapter.Complete(ctx, request)
		if response != nil {
			usage.InputTokens += response.Usage.InputTokens
			usage.OutputTokens += response.Usage.OutputTokens
			usage.TotalTokens += response.Usage.TotalTokens
			response.Usage = usage
		}
		if err != nil || response == nil {
			return response, err
		}
		if response.FinishReason != "length" && response.FinishReason != "max_tokens" {
			return response, nil
		}
		if attempt == 1 || request.MaxOutputTokens >= 16384 {
			return response, fmt.Errorf("%w at %d tokens", ErrOutputBudgetExhausted, request.MaxOutputTokens)
		}
		request.MaxOutputTokens = min(16384, max(8192, request.MaxOutputTokens*2))
	}
	panic("unreachable bounded generation")
}
