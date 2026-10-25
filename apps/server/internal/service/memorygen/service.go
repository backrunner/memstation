package memorygen

import (
	"context"
	"encoding/json"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	conversationservice "memstation/apps/server/internal/service/conversation"
	"memstation/apps/server/internal/store/sql/memorygeneration"

	"github.com/google/uuid"
)

type (
	CreateRebuildInput = domain.CreateMemoryRebuildInput
	UserSourceStats    = domain.MemorySourceStats
)

// Service prepares source material; persistence and atomic lifecycle transitions
// are delegated to the generation repository.
type Service struct{ *memorygeneration.Repository }

func NewService(db runtimeconfig.DBHandle, model string) *Service {
	return &Service{Repository: memorygeneration.NewRepository(db, model)}
}

func CurrentGenerationID(userID string) string { return memorygeneration.CurrentGenerationID(userID) }

func (s *Service) CreateRebuildGeneration(ctx context.Context, in CreateRebuildInput) (domain.MemoryGeneration, []domain.MemoryGenerationSource, error) {
	if in.Scope != "conversation" && security.AdminFromContext(ctx) {
		var err error
		ctx, err = s.FullWorkspaceContext(ctx, in.UserID)
		if err != nil {
			return domain.MemoryGeneration{}, nil, err
		}
	}
	return s.Repository.CreateRebuildGeneration(ctx, in)
}

func (s *Service) CaptureSourceSnapshot(ctx context.Context, conversation domain.ConversationRecord) (domain.MemorySourceSnapshot, bool, error) {
	workspace, err := security.DataScope(ctx, conversation.UserID, conversation.WorkspaceID)
	if err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	conversation.WorkspaceID = workspace
	conversation.Parts, err = domain.NormalizeMessageParts(conversation.UserID, workspace, conversation.ConversationID, conversation.Parts)
	if err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	conversation.Artifacts, err = domain.NormalizePartArtifacts(conversation.UserID, workspace, conversation.ConversationID, conversation.Artifacts)
	if err != nil {
		return domain.MemorySourceSnapshot{}, false, err
	}
	return s.Repository.CaptureSourceSnapshot(ctx, snapshotFromConversation(conversation))
}

func snapshotFromConversation(conversation domain.ConversationRecord) domain.MemorySourceSnapshot {
	now := time.Now().UTC()
	metadata := cloneMap(conversation.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["captured_from_conversation"] = conversation.ConversationID
	metadata["trusted_context_mode"] = conversation.ContextMode
	metadata["trusted_partition_id"] = conversation.PartitionID
	metadata["captured_at"] = now.Format(time.RFC3339)
	messages := conversationservice.PrepareMessagesForStorage(conversation.Messages)
	parts := append([]domain.MessagePart(nil), conversation.Parts...)
	artifacts := append([]domain.MessagePartArtifact(nil), conversation.Artifacts...)
	return domain.MemorySourceSnapshot{
		UserID:         conversation.UserID,
		WorkspaceID:    conversation.WorkspaceID,
		ConversationID: conversation.ConversationID,
		SourceHash: sourceHash(map[string]any{
			"request_format": conversation.RequestFormat,
			"provider":       conversation.Provider,
			"mode":           conversation.Mode,
			"messages":       messagesWithoutDerivedPromptText(messages),
			"parts":          parts,
			"artifacts":      artifacts,
		}),
		RequestFormat:  conversation.RequestFormat,
		Provider:       conversation.Provider,
		Mode:           conversation.Mode,
		FinalizeStatus: conversation.FinalizeStatus,
		Metadata:       metadata,
		Usage:          conversation.Usage,
		MessageCount:   len(messages),
		Messages:       messages,
		Parts:          parts,
		Artifacts:      artifacts,
	}
}

func messagesWithoutDerivedPromptText(messages []domain.UnifiedMessage) []domain.UnifiedMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]domain.UnifiedMessage, 0, len(messages))
	for _, message := range messages {
		cloned := message
		if len(message.Metadata) > 0 {
			cloned.Metadata = cloneMap(message.Metadata)
			delete(cloned.Metadata, conversationservice.MemoryPromptTextMetadataKey)
			if len(cloned.Metadata) == 0 {
				cloned.Metadata = nil
			}
		}
		out = append(out, cloned)
	}
	return out
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for k, v := range value {
		out[k] = v
	}
	return out
}

func sourceHash(value any) string {
	raw, _ := json.Marshal(value)
	return uuid.NewSHA1(uuid.NameSpaceOID, raw).String()
}
