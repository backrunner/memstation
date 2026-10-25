package sql

import (
	"context"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

func mergeConversationWriteMetadata(stored, incoming map[string]any, status domain.FinalizeStatus) map[string]any {
	out := cloneMetadata(stored)
	if out == nil {
		out = map[string]any{}
	}
	for k, v := range incoming {
		if status != "" && status != domain.FinalizeStatusActive {
			protected := strings.HasPrefix(k, "finaliz") || strings.HasPrefix(k, "summary_") || strings.HasPrefix(k, "analysis_") || strings.HasPrefix(k, "source_") || strings.HasPrefix(k, "memory_") || strings.HasPrefix(k, "embedding_")
			if _, exists := stored[k]; protected && exists {
				continue
			}
		}
		out[k] = v
	}
	return out
}

func normalizeConversationScope(ctx context.Context, c domain.ConversationRecord) (domain.ConversationRecord, error) {
	w, err := security.DataScope(ctx, c.UserID, c.WorkspaceID)
	if err != nil {
		return c, err
	}
	c.WorkspaceID = w
	if access, ok := security.AccessFromContext(ctx); ok {
		if c.AgentID != "" && c.AgentID != access.AgentID {
			return c, domain.ErrAccessDenied
		}
		c.AgentID = access.AgentID
	}
	if c.AgentID == "" {
		c.AgentID = domain.LegacyAgentID(c.UserID)
	}
	c.Metadata = conversationScopeMetadata(c, c.Metadata)
	return c, nil
}

func conversationScopeMetadata(c domain.ConversationRecord, metadata map[string]any) map[string]any {
	metadata = cloneMetadata(metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["workspace_id"] = c.WorkspaceID
	metadata["agent_id"] = c.AgentID
	return metadata
}

func authorizeConversationWriter(ctx context.Context, c domain.ConversationRecord) error {
	if access, ok := security.AccessFromContext(ctx); ok && access.AgentID != c.AgentID {
		return domain.ErrAccessDenied
	}
	return nil
}

func finalizationEvent(c domain.ConversationRecord, input domain.ConversationFinalization) (domain.ConversationStateUpdate, *domain.OutboxEvent, error) {
	if input.Status != domain.FinalizeStatusEnded && input.Status != domain.FinalizeStatusTimeout || input.Reason == "" {
		return domain.ConversationStateUpdate{}, nil, domain.ErrAccessDenied
	}
	metadata := map[string]any{"finalize_reason": input.Reason, "finalize_target": string(input.Status)}
	update := domain.ConversationStateUpdate{UserID: c.UserID, ConversationID: c.ConversationID, Metadata: metadata, UpdatedAt: time.Now().UTC()}
	persist := metadataBoolValue(c.Metadata, "memory_persist_enabled", true)
	retain := metadataBoolValue(c.Metadata, "source_retain_enabled", false)
	if !persist && !retain {
		skipped := domain.ProcessingStatusSkipped
		update.FinalizeStatus = &input.Status
		update.SummaryStatus = &skipped
		update.AnalysisStatus = &skipped
		return update, nil, nil
	}
	ending := domain.FinalizeStatusEnding
	update.FinalizeStatus = &ending
	event := domain.OutboxEvent{ID: "conversation.finalize:" + c.ConversationID, UserID: c.UserID, EventType: "conversation.finalize", AggregateType: "conversation", AggregateID: c.ConversationID, Status: "pending", AvailableAt: update.UpdatedAt, CreatedAt: update.UpdatedAt, Payload: map[string]any{
		"schema_version": 1, "workspace_id": c.WorkspaceID, "agent_id": c.AgentID,
		"user_id": c.UserID, "conversation_id": c.ConversationID, "conversation_revision": c.Revision,
		"finalize_reason": input.Reason, "finalize_status": string(input.Status), "memory_persist_enabled": persist, "source_retain_enabled": retain,
	}}
	return update, &event, nil
}

func metadataBoolValue(metadata map[string]any, name string, fallback bool) bool {
	switch value := metadata[name].(type) {
	case bool:
		return value
	case string:
		if value == "true" {
			return true
		}
		if value == "false" {
			return false
		}
	}
	return fallback
}
