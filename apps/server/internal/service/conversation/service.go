package conversation

import (
	"context"
	"errors"
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
	existing, err := s.repo.GetConversation(ctx, req.UserID, conversationID)
	if err != nil && !errors.Is(err, domain.ErrConversationNotFound) {
		return "", err
	}
	record := domain.ConversationRecord{
		ExpectedRevision: req.ExpectedConversationRevision,
		WorkspaceID:      req.WorkspaceID,
		AgentID:          req.AgentID,
		ConversationID:   conversationID,
		UserID:           req.UserID,
		RequestFormat:    string(req.Protocol),
		Provider:         req.Provider,
		Mode:             req.Mode,
		FinalizeStatus:   domain.FinalizeStatusActive,
		SummaryStatus:    domain.ProcessingStatusPending,
		AnalysisStatus:   domain.ProcessingStatusPending,
		Messages:         append([]domain.UnifiedMessage{}, req.Messages...),
		Response:         resp,
		Metadata: mergeMetadata(req.RequestMetadata, map[string]any{
			"conversation_id":         conversationID,
			"request_id":              req.RequestID,
			"session_id":              req.SessionID,
			"memory_persist_enabled":  req.PersistMemoryEnabled(),
			"source_retain_enabled":   req.SourceRetainEnabled(false),
			"tool_write_enabled":      req.ToolWriteEnabled,
			"request_received_at_utc": req.ReceivedAt.Format(time.RFC3339),
		}),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if !req.PersistMemoryEnabled() && !req.SourceRetainEnabled(false) {
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
	return s.repo.RequestFinalization(ctx, domain.ConversationFinalization{UserID: req.UserID, WorkspaceID: req.WorkspaceID, ConversationID: req.ConversationID, Status: targetStatus, Reason: reason}, s.outbox)
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
		if err := s.repo.RequestFinalization(ctx, domain.ConversationFinalization{UserID: conversation.UserID, WorkspaceID: conversation.WorkspaceID, ConversationID: conversation.ConversationID, Status: domain.FinalizeStatusTimeout, Reason: "timeout", TimeoutBefore: &before}, s.outbox); err != nil {
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
