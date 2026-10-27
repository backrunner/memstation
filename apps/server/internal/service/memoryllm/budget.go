package memoryllm

import (
	"context"
	"errors"
	"fmt"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

var ErrOutputBudgetExhausted = errors.New("memory LLM output budget exhausted")

const (
	DefaultMinOutputTokens    = 16384
	DefaultMaxOutputTokens    = 65536
	MaxConfiguredOutputTokens = 262144
)

// CompleteWithBudget retries only explicit output truncation, once, with a
// larger bounded budget. Reasoning models can spend their entire output budget
// before producing any JSON. Never accept a partial result or repair it locally.
// Both attempts share the caller's deadline; reported usage includes both.
func CompleteWithBudget(ctx context.Context, adapter ports.ProviderAdapter, request domain.UnifiedRequest, maxOutputTokens int) (*domain.UnifiedResponse, error) {
	if request.MaxOutputTokens <= 0 || request.MaxOutputTokens > maxOutputTokens || maxOutputTokens > MaxConfiguredOutputTokens {
		return nil, errors.New("invalid bounded LLM output budget")
	}
	usage := domain.UsageRecord{}
	var last *domain.UnifiedResponse
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		response, err := adapter.Complete(ctx, request)
		if response != nil {
			usage.InputTokens += response.Usage.InputTokens
			usage.OutputTokens += response.Usage.OutputTokens
			usage.TotalTokens += response.Usage.TotalTokens
			response.Usage = usage
			last = response
		}
		if err != nil {
			return last, err
		}
		if response == nil {
			return last, errors.New("memory LLM returned no response")
		}
		if response.FinishReason != "length" && response.FinishReason != "max_tokens" {
			return response, nil
		}
		if attempt == 1 || request.MaxOutputTokens >= maxOutputTokens {
			return response, fmt.Errorf("%w at %d tokens", ErrOutputBudgetExhausted, request.MaxOutputTokens)
		}
		request.MaxOutputTokens = maxOutputTokens
	}
	panic("unreachable bounded generation")
}
