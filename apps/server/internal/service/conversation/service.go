package conversation

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/observability"
	"memstation/apps/server/internal/ports"
)

type Service struct {
	repo             ports.ConversationRepository
	outbox           ports.OutboxRepository
	mu               sync.RWMutex
	messageThreshold int
	tokenThreshold   int
}

func NewService(repo ports.ConversationRepository, outbox ports.OutboxRepository, messageThreshold, tokenThreshold int) *Service {
	return &Service{repo: repo, outbox: outbox, messageThreshold: messageThreshold, tokenThreshold: tokenThreshold}
}

func (s *Service) UpdateThresholds(messageThreshold, tokenThreshold int) {
	s.mu.Lock()
	s.messageThreshold = messageThreshold
	s.tokenThreshold = tokenThreshold
	s.mu.Unlock()
}

func (s *Service) PersistExchange(ctx context.Context, req domain.UnifiedRequest, resp *domain.UnifiedResponse) (string, error) {
	ctx, span := observability.TraceSpan(ctx, "conversation.persist_exchange",
		attribute.String("conversation.user_id", req.UserID),
		attribute.String("conversation.protocol", string(req.Protocol)),
	)
	defer span.End()

	conversationID := req.ConversationID
	if conversationID == "" {
		conversationID = uuid.NewString()
	}
	existing, _ := s.repo.GetConversation(ctx, req.UserID, conversationID)
	record := domain.ConversationRecord{
		ConversationID: conversationID,
		UserID:         req.UserID,
		RequestFormat:  string(req.Protocol),
		Provider:       req.Provider,
		Mode:           req.Mode,
		FinalizeStatus: domain.FinalizeStatusActive,
		SummaryStatus:  domain.ProcessingStatusPending,
		AnalysisStatus: domain.ProcessingStatusPending,
		Messages:       append([]domain.UnifiedMessage{}, req.Messages...),
		Response:       resp,
		Metadata: mergeMetadata(req.RequestMetadata, map[string]any{
			"conversation_id":         conversationID,
			"request_id":              req.RequestID,
			"session_id":              req.SessionID,
			"memory_persist_enabled":  req.PersistMemoryEnabled(),
			"tool_write_enabled":      req.ToolWriteEnabled,
			"request_received_at_utc": req.ReceivedAt.Format(time.RFC3339),
		}),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if !req.PersistMemoryEnabled() {
		record.SummaryStatus = domain.ProcessingStatusSkipped
		record.AnalysisStatus = domain.ProcessingStatusSkipped
	}
	if existing.ConversationID != "" {
		record.CreatedAt = existing.CreatedAt
		if existing.FinalizeStatus != "" {
			record.FinalizeStatus = existing.FinalizeStatus
		}
		if existing.SummaryStatus != "" {
			record.SummaryStatus = existing.SummaryStatus
		}
		if existing.AnalysisStatus != "" {
			record.AnalysisStatus = existing.AnalysisStatus
		}
		record.Metadata = mergeMetadata(existing.Metadata, record.Metadata)
	}
	if resp != nil {
		record.Usage = resp.Usage
		record.Messages = append(record.Messages, resp.Message)
	}
	if err := s.repo.PersistExchange(ctx, record); err != nil {
		return "", err
	}
	return conversationID, nil
}

func (s *Service) FinalizeIfNeeded(ctx context.Context, req domain.UnifiedRequest, resp *domain.UnifiedResponse) error {
	ctx, span := observability.TraceSpan(ctx, "conversation.finalize_if_needed",
		attribute.String("conversation.id", req.ConversationID),
		attribute.String("conversation.user_id", req.UserID),
	)
	defer span.End()

	shouldFinalize, targetStatus, reason := s.shouldFinalize(req, resp)
	if !shouldFinalize {
		return nil
	}
	current, err := s.repo.GetConversation(ctx, req.UserID, req.ConversationID)
	if err == nil {
		switch current.FinalizeStatus {
		case domain.FinalizeStatusEnding, domain.FinalizeStatusEnded, domain.FinalizeStatusTimeout, domain.FinalizeStatusFailed:
			return nil
		}
	}
	ending := domain.FinalizeStatusEnding
	if err := s.repo.UpdateConversationState(ctx, domain.ConversationStateUpdate{
		ConversationID: req.ConversationID,
		UserID:         req.UserID,
		FinalizeStatus: &ending,
		Metadata: map[string]any{
			"finalize_reason": reason,
			"finalize_target": string(targetStatus),
		},
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	if !req.PersistMemoryEnabled() {
		skipped := domain.ProcessingStatusSkipped
		return s.repo.UpdateConversationState(ctx, domain.ConversationStateUpdate{
			ConversationID: req.ConversationID,
			UserID:         req.UserID,
			FinalizeStatus: &targetStatus,
			SummaryStatus:  &skipped,
			AnalysisStatus: &skipped,
			Metadata: map[string]any{
				"finalize_reason":        reason,
				"finalize_target":        string(targetStatus),
				"memory_persist_enabled": false,
			},
			UpdatedAt: time.Now().UTC(),
		})
	}
	return s.outbox.Enqueue(ctx, domain.OutboxEvent{
		ID:            "conversation.finalize:" + req.ConversationID,
		UserID:        req.UserID,
		EventType:     "conversation.finalize",
		AggregateType: "conversation",
		AggregateID:   req.ConversationID,
		Payload: map[string]any{
			"conversation_id": req.ConversationID,
			"user_id":         req.UserID,
			"finalize_reason": reason,
			"finalize_status": string(targetStatus),
		},
		Status:      "pending",
		AvailableAt: time.Now().UTC(),
	})
}

func (s *Service) GetConversation(ctx context.Context, userID, conversationID string) (domain.ConversationRecord, error) {
	return s.repo.GetConversation(ctx, userID, conversationID)
}

func (s *Service) ListConversations(ctx context.Context, userID string) ([]domain.ConversationRecord, error) {
	return s.repo.ListConversations(ctx, userID)
}

func (s *Service) FinalizeTimedOut(ctx context.Context, before time.Time) error {
	ctx, span := observability.TraceSpan(ctx, "conversation.finalize_timed_out")
	defer span.End()

	conversations, err := s.repo.ListTimedOutConversations(ctx, before, 100)
	if err != nil {
		return err
	}
	for _, conversation := range conversations {
		if !metadataBool(conversation.Metadata, "memory_persist_enabled", true) {
			targetStatus := domain.FinalizeStatusTimeout
			skipped := domain.ProcessingStatusSkipped
			if err := s.repo.UpdateConversationState(ctx, domain.ConversationStateUpdate{
				ConversationID: conversation.ConversationID,
				UserID:         conversation.UserID,
				FinalizeStatus: &targetStatus,
				SummaryStatus:  &skipped,
				AnalysisStatus: &skipped,
				Metadata: map[string]any{
					"finalize_reason":        "timeout",
					"finalize_target":        string(domain.FinalizeStatusTimeout),
					"memory_persist_enabled": false,
				},
				UpdatedAt: time.Now().UTC(),
			}); err != nil {
				return err
			}
			continue
		}
		ending := domain.FinalizeStatusEnding
		if err := s.repo.UpdateConversationState(ctx, domain.ConversationStateUpdate{
			ConversationID: conversation.ConversationID,
			UserID:         conversation.UserID,
			FinalizeStatus: &ending,
			Metadata: map[string]any{
				"finalize_reason": "timeout",
				"finalize_target": string(domain.FinalizeStatusTimeout),
			},
			UpdatedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		if err := s.outbox.Enqueue(ctx, domain.OutboxEvent{
			ID:            "conversation.finalize:" + conversation.ConversationID,
			UserID:        conversation.UserID,
			EventType:     "conversation.finalize",
			AggregateType: "conversation",
			AggregateID:   conversation.ConversationID,
			Payload: map[string]any{
				"conversation_id": conversation.ConversationID,
				"user_id":         conversation.UserID,
				"finalize_reason": "timeout",
				"finalize_status": string(domain.FinalizeStatusTimeout),
			},
			Status:      "pending",
			AvailableAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) shouldFinalize(req domain.UnifiedRequest, resp *domain.UnifiedResponse) (bool, domain.FinalizeStatus, string) {
	s.mu.RLock()
	messageThreshold := s.messageThreshold
	tokenThreshold := s.tokenThreshold
	s.mu.RUnlock()

	if req.EndConversation {
		return true, domain.FinalizeStatusEnded, "explicit"
	}
	if messageThreshold > 0 && len(req.Messages) >= messageThreshold {
		return true, domain.FinalizeStatusEnded, "message_threshold"
	}
	if resp != nil && tokenThreshold > 0 && resp.Usage.TotalTokens >= tokenThreshold {
		return true, domain.FinalizeStatusEnded, "token_threshold"
	}
	return false, domain.FinalizeStatusActive, ""
}

func mergeMetadata(existing, next map[string]any) map[string]any {
	if len(existing) == 0 && len(next) == 0 {
		return nil
	}
	out := map[string]any{}
	for key, value := range existing {
		out[key] = value
	}
	for key, value := range next {
		out[key] = value
	}
	return out
}

func metadataBool(metadata map[string]any, key string, fallback bool) bool {
	if metadata == nil {
		return fallback
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch typed {
		case "true":
			return true
		case "false":
			return false
		}
	}
	return fallback
}
