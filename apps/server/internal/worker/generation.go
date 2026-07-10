package worker

import (
	"context"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

func resolveProcessingGeneration(ctx context.Context, services Services, userID, explicitGenerationID string) (domain.MemoryGeneration, error) {
	if services.Generations == nil {
		generationID := strings.TrimSpace(explicitGenerationID)
		if generationID == "" {
			generationID = currentGenerationID(userID)
		}
		return domain.MemoryGeneration{
			GenerationID:      generationID,
			UserID:            userID,
			Status:            domain.MemoryGenerationStatusActivated,
			IsActive:          true,
			EmbeddingVersion:  1,
			EmbeddingModel:    services.EmbeddingModel,
			ActivateOnSuccess: true,
		}, nil
	}
	generation, err := services.Generations.ResolveGeneration(ctx, userID, explicitGenerationID)
	if err != nil {
		return domain.MemoryGeneration{}, err
	}
	if generation.EmbeddingVersion <= 0 {
		generation.EmbeddingVersion = 1
	}
	if strings.TrimSpace(generation.EmbeddingModel) == "" {
		generation.EmbeddingModel = services.EmbeddingModel
	}
	return generation, nil
}

func applyGenerationToDocument(userID, generationID string, doc domain.MemoryDocument) domain.MemoryDocument {
	doc.GenerationID = generationID
	doc.ID = scopeStableID(userID, generationID, doc.ID)
	doc.NodeID = scopeStableID(userID, generationID, doc.NodeID)
	if doc.Metadata == nil {
		doc.Metadata = map[string]any{}
	}
	if generationID != "" {
		doc.Metadata["generation_id"] = generationID
	}
	return doc
}

func applyGenerationToRelations(userID, generationID string, relations []domain.MemoryRelation) []domain.MemoryRelation {
	if len(relations) == 0 {
		return nil
	}
	out := make([]domain.MemoryRelation, 0, len(relations))
	for _, relation := range relations {
		relation.GenerationID = generationID
		relation.SrcNodeID = scopeStableID(userID, generationID, relation.SrcNodeID)
		relation.DstNodeID = scopeStableID(userID, generationID, relation.DstNodeID)
		if relation.Metadata == nil {
			relation.Metadata = map[string]any{}
		}
		if generationID != "" {
			relation.Metadata["generation_id"] = generationID
		}
		out = append(out, relation)
	}
	return out
}

func conversationSourceRetainEnabled(conversation domain.ConversationRecord) bool {
	if conversation.Metadata == nil {
		return false
	}
	value, ok := conversation.Metadata["source_retain_enabled"]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func snapshotConversation(snapshot domain.MemorySourceSnapshot) domain.ConversationRecord {
	metadata := cloneMap(snapshot.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["source_snapshot_id"] = snapshot.SnapshotID
	metadata["source_snapshot_version"] = snapshot.SnapshotVersion
	metadata["source_hash"] = snapshot.SourceHash
	return domain.ConversationRecord{
		ConversationID: snapshot.ConversationID,
		UserID:         snapshot.UserID,
		RequestFormat:  snapshot.RequestFormat,
		Provider:       snapshot.Provider,
		Mode:           snapshot.Mode,
		FinalizeStatus: snapshot.FinalizeStatus,
		Metadata:       metadata,
		Usage:          snapshot.Usage,
		Messages:       append([]domain.UnifiedMessage{}, snapshot.Messages...),
		Parts:          append([]domain.MessagePart{}, snapshot.Parts...),
		Artifacts:      append([]domain.MessagePartArtifact{}, snapshot.Artifacts...),
		CreatedAt:      snapshot.CreatedAt,
		UpdatedAt:      snapshot.UpdatedAt,
	}
}

func snapshotMetadata(snapshot domain.MemorySourceSnapshot) map[string]any {
	return map[string]any{
		"source_snapshot_id":      snapshot.SnapshotID,
		"source_snapshot_version": snapshot.SnapshotVersion,
		"source_hash":             snapshot.SourceHash,
		"source_snapshot_at":      snapshot.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func mergeMetadata(parts ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, part := range parts {
		for key, value := range part {
			out[key] = value
		}
	}
	return out
}

func currentGenerationID(userID string) string {
	return "gen:current:" + strings.TrimSpace(userID)
}
