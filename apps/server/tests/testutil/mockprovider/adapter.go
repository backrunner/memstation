package mockprovider

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"memstation/apps/server/internal/domain"
)

type Adapter struct {
	Provider domain.Provider
}

func New(provider domain.Provider) *Adapter {
	return &Adapter{Provider: provider}
}

func (a *Adapter) Name() domain.Provider {
	return a.Provider
}

func (a *Adapter) Complete(_ context.Context, req domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	text := fmt.Sprintf("MemStation %s mock adapter handled %d message(s) in %s mode.", a.Provider, len(req.Messages), req.Mode)
	return &domain.UnifiedResponse{
		ResponseID: uuid.NewString(),
		Protocol:   req.Protocol,
		Provider:   a.Provider,
		Model:      req.Model,
		Message: domain.UnifiedMessage{
			Role: domain.RoleAssistant,
			Text: text,
			Blocks: []domain.ContentBlock{{
				Type: domain.ContentBlockText,
				Text: text,
			}},
		},
		Usage: domain.UsageRecord{
			InputTokens:  len(req.Messages) * 32,
			OutputTokens: 24,
			TotalTokens:  len(req.Messages)*32 + 24,
		},
		CreatedAt: time.Now().UTC(),
		Raw: map[string]any{
			"provider": string(a.Provider),
			"mock":     true,
		},
	}, nil
}

func (a *Adapter) Stream(_ context.Context, req domain.UnifiedRequest, emit func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
	text := fmt.Sprintf("MemStation %s mock adapter handled %d message(s) in %s mode.", a.Provider, len(req.Messages), req.Mode)
	responseID := uuid.NewString()
	createdAt := time.Now().UTC()

	events := []domain.StreamEvent{
		{
			Type:       domain.StreamEventResponseStart,
			ResponseID: responseID,
			Provider:   a.Provider,
			Model:      req.Model,
			Role:       domain.RoleAssistant,
			CreatedAt:  createdAt,
		},
		{
			Type:       domain.StreamEventTextDelta,
			ResponseID: responseID,
			Provider:   a.Provider,
			Model:      req.Model,
			Role:       domain.RoleAssistant,
			Delta:      text,
			CreatedAt:  createdAt,
		},
		{
			Type:       domain.StreamEventUsage,
			ResponseID: responseID,
			Provider:   a.Provider,
			Model:      req.Model,
			Role:       domain.RoleAssistant,
			Usage: domain.UsageRecord{
				InputTokens:  len(req.Messages) * 32,
				OutputTokens: 24,
				TotalTokens:  len(req.Messages)*32 + 24,
			},
			CreatedAt: createdAt,
		},
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return nil, err
		}
	}

	return &domain.UnifiedResponse{
		ResponseID: responseID,
		Protocol:   req.Protocol,
		Provider:   a.Provider,
		Model:      req.Model,
		Message: domain.UnifiedMessage{
			Role: domain.RoleAssistant,
			Text: text,
			Blocks: []domain.ContentBlock{{
				Type: domain.ContentBlockText,
				Text: text,
			}},
		},
		Usage: domain.UsageRecord{
			InputTokens:  len(req.Messages) * 32,
			OutputTokens: 24,
			TotalTokens:  len(req.Messages)*32 + 24,
		},
		CreatedAt: createdAt,
		Raw: map[string]any{
			"provider": string(a.Provider),
			"mock":     true,
		},
	}, nil
}
