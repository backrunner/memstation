package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/hibiken/asynq"

	"memstation/apps/server/internal/domain"
	analysisservice "memstation/apps/server/internal/service/analysis"
	conversationservice "memstation/apps/server/internal/service/conversation"
)

var errGenerationSourceSuperseded = errors.New("memory generation source has been superseded")

func handleMemoryRebuildConversation(logger *slog.Logger, services Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		if services.Generations == nil {
			return errors.New("memory generation service is not configured")
		}
		var payload MemoryRebuildConversationPayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		generation, err := services.Generations.GetGeneration(ctx, payload.UserID, payload.GenerationID)
		if err != nil {
			return err
		}
		snapshot, ok, err := services.Generations.GetSourceSnapshot(ctx, payload.UserID, payload.SnapshotID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("source snapshot not found")
		}
		if current, err := services.Generations.IsCurrentGenerationSource(ctx, payload.UserID, payload.GenerationID, payload.SnapshotID); err != nil {
			return err
		} else if !current {
			logger.InfoContext(ctx, "memory rebuild conversation skipped because a newer snapshot is already queued", "generation_id", payload.GenerationID, "snapshot_id", payload.SnapshotID)
			_, _, err = services.Generations.MarkGenerationSourceCompleted(ctx, payload.UserID, payload.GenerationID, payload.SnapshotID, 0)
			return err
		}
		if err := services.Generations.MarkGenerationSourceRunning(ctx, payload.UserID, payload.GenerationID, payload.SnapshotID); err != nil {
			return err
		}
		logger.InfoContext(ctx, "memory rebuild conversation started", "generation_id", payload.GenerationID, "snapshot_id", payload.SnapshotID)
		embeddingCount, err := rebuildSnapshotIntoGeneration(ctx, services, generation, snapshot)
		if errors.Is(err, errGenerationSourceSuperseded) {
			logger.InfoContext(ctx, "memory rebuild conversation stopped because a newer snapshot superseded it", "generation_id", payload.GenerationID, "snapshot_id", payload.SnapshotID)
			_, _, markErr := services.Generations.MarkGenerationSourceCompleted(ctx, payload.UserID, payload.GenerationID, payload.SnapshotID, 0)
			if markErr != nil {
				return markErr
			}
			return nil
		}
		if err != nil {
			_, _ = services.Generations.MarkGenerationSourceFailed(ctx, payload.UserID, payload.GenerationID, payload.SnapshotID, err.Error())
			return err
		}
		_, ready, err := services.Generations.MarkGenerationSourceCompleted(ctx, payload.UserID, payload.GenerationID, payload.SnapshotID, embeddingCount)
		if err != nil {
			return err
		}
		if ready && services.Publisher != nil {
			return services.Publisher.Publish(ctx, domain.TaskMessage{
				Type:      TypeMemoryRebuildActivate,
				Queue:     QueueDefault,
				UniqueKey: TypeMemoryRebuildActivate + ":" + payload.GenerationID,
				Payload: map[string]any{
					"user_id":       payload.UserID,
					"generation_id": payload.GenerationID,
				},
			})
		}
		return nil
	}
}

func handleMemoryRebuildActivate(logger *slog.Logger, services Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		if services.Generations == nil {
			return errors.New("memory generation service is not configured")
		}
		var payload MemoryRebuildActivatePayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		generation, err := services.Generations.ActivateGeneration(ctx, payload.UserID, payload.GenerationID)
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "memory rebuild generation activated", "generation_id", generation.GenerationID, "user_id", generation.UserID)
		return nil
	}
}

func rebuildSnapshotIntoGeneration(ctx context.Context, services Services, generation domain.MemoryGeneration, snapshot domain.MemorySourceSnapshot) (int, error) {
	if services.Memories == nil || services.Vectors == nil {
		return 0, errors.New("memory rebuild services are not fully configured")
	}
	conversation := snapshotConversation(snapshot)
	finalizeReason := stringValue(conversation.Metadata, "finalize_reason")
	segments := applySnapshotMetadataToSegments(
		snapshot,
		applyGenerationToSegments(snapshot.UserID, generation.GenerationID, conversationservice.BuildSegments(snapshot.UserID, snapshot.ConversationID, snapshot.Messages, snapshot.UpdatedAt, conversationservice.DefaultSegmentOptions())),
	)
	if err := ensureCurrentGenerationSource(ctx, services, snapshot.UserID, generation.GenerationID, snapshot.SnapshotID); err != nil {
		return 0, err
	}
	if err := cleanupStaleSegments(ctx, services, snapshot.UserID, generation.GenerationID, snapshot.ConversationID, segments); err != nil {
		return 0, err
	}

	summaryText := buildSummary(snapshot.Messages, nil, nil)
	if summaryText == "" {
		summaryText = conversationservice.SummarizeSegments(segments)
	}
	summaryDoc := applyGenerationToDocument(snapshot.UserID, generation.GenerationID, domain.MemoryDocument{
		ID:             summaryDocumentID(snapshot.ConversationID, 1),
		NodeID:         summaryNodeID(snapshot.ConversationID, 1),
		UserID:         snapshot.UserID,
		ConversationID: snapshot.ConversationID,
		Type:           domain.MemoryTypeSummary,
		Content:        summaryText,
		Importance:     1,
		DecayFactor:    1,
		Metadata: mergeMetadata(snapshotMetadata(snapshot), map[string]any{
			"summary_version":          1,
			"finalize_reason":          finalizeReason,
			"topic_label":              summarizeTopic(summaryText),
			"keywords":                 extractKeywords(summaryText),
			"segment_count":            len(segments),
			"segment_strategy":         "dialogue_window_v1",
			"segment_overlap_messages": conversationservice.DefaultSegmentOptions().Overlap,
		}),
	})
	applyDocumentScore(ctx, services.Scorer, &summaryDoc, conversation.UpdatedAt)
	if err := ensureCurrentGenerationSource(ctx, services, snapshot.UserID, generation.GenerationID, snapshot.SnapshotID); err != nil {
		return 0, err
	}
	if err := services.Memories.StoreDocument(ctx, summaryDoc); err != nil {
		return 0, err
	}
	segmentDocs, err := storeSegmentDocuments(ctx, services, segments, summaryDoc, conversation.UpdatedAt, nil, nil)
	if err != nil {
		return 0, err
	}

	analyzer := services.Analysis
	if analyzer == nil {
		analyzer = analysisservice.NewService()
	}
	result, err := analyzer.AnalyzeConversation(ctx, analysisservice.Input{
		UserID:         snapshot.UserID,
		ConversationID: snapshot.ConversationID,
		Conversation:   conversation,
		Summary:        summaryDoc,
	})
	if err != nil {
		return 0, err
	}
	result.Fact = applyGenerationToDocument(snapshot.UserID, generation.GenerationID, result.Fact)
	result.Impression = applyGenerationToDocument(snapshot.UserID, generation.GenerationID, result.Impression)
	result.Fact.Metadata = mergeMetadata(result.Fact.Metadata, snapshotMetadata(snapshot))
	result.Impression.Metadata = mergeMetadata(result.Impression.Metadata, snapshotMetadata(snapshot))
	result.Relations = applySnapshotMetadataToRelations(snapshot, applyGenerationToRelations(snapshot.UserID, generation.GenerationID, result.Relations))
	applyDocumentScore(ctx, services.Scorer, &result.Fact, conversation.UpdatedAt)
	applyDocumentScore(ctx, services.Scorer, &result.Impression, conversation.UpdatedAt)
	if err := ensureCurrentGenerationSource(ctx, services, snapshot.UserID, generation.GenerationID, snapshot.SnapshotID); err != nil {
		return 0, err
	}
	if err := services.Memories.StoreDocument(ctx, result.Fact); err != nil {
		return 0, err
	}
	if err := services.Memories.StoreDocument(ctx, result.Impression); err != nil {
		return 0, err
	}
	for _, relation := range result.Relations {
		if err := services.Memories.LinkDocuments(ctx, relation); err != nil {
			return 0, err
		}
	}

	docs := make([]domain.MemoryDocument, 0, 2+len(segmentDocs))
	docs = append(docs, summaryDoc)
	docs = append(docs, segmentDocs...)
	docs = append(docs, result.Fact, result.Impression)
	return upsertStoredDocumentEmbeddings(ctx, services, generation, snapshot, docs)
}

func ensureCurrentGenerationSource(ctx context.Context, services Services, userID, generationID, snapshotID string) error {
	if services.Generations == nil {
		return nil
	}
	current, err := services.Generations.IsCurrentGenerationSource(ctx, userID, generationID, snapshotID)
	if err != nil {
		return err
	}
	if !current {
		return errGenerationSourceSuperseded
	}
	return nil
}

func applySnapshotMetadataToSegments(snapshot domain.MemorySourceSnapshot, segments []domain.ConversationSegment) []domain.ConversationSegment {
	if len(segments) == 0 {
		return nil
	}
	out := make([]domain.ConversationSegment, 0, len(segments))
	for _, segment := range segments {
		if segment.Metadata == nil {
			segment.Metadata = map[string]any{}
		}
		segment.Metadata = mergeMetadata(segment.Metadata, snapshotMetadata(snapshot))
		out = append(out, segment)
	}
	return out
}

func applySnapshotMetadataToRelations(snapshot domain.MemorySourceSnapshot, relations []domain.MemoryRelation) []domain.MemoryRelation {
	if len(relations) == 0 {
		return nil
	}
	out := make([]domain.MemoryRelation, 0, len(relations))
	for _, relation := range relations {
		relation.Metadata = mergeMetadata(relation.Metadata, snapshotMetadata(snapshot))
		out = append(out, relation)
	}
	return out
}

func upsertStoredDocumentEmbeddings(ctx context.Context, services Services, generation domain.MemoryGeneration, snapshot domain.MemorySourceSnapshot, docs []domain.MemoryDocument) (int, error) {
	embeddingCount := 0
	for _, doc := range docs {
		if err := ensureCurrentGenerationSource(ctx, services, snapshot.UserID, generation.GenerationID, snapshot.SnapshotID); err != nil {
			return embeddingCount, err
		}
		stored, ok, err := currentStoredDocumentForEmbedding(ctx, services, doc)
		if err != nil {
			return embeddingCount, err
		}
		if !ok || sourceSnapshotVersionValue(stored.Metadata) != snapshot.SnapshotVersion {
			continue
		}
		if err := services.Vectors.Upsert(ctx, domain.VectorUpsertRequest{
			UserID:           stored.UserID,
			GenerationID:     generation.GenerationID,
			NodeID:           stored.NodeID,
			NodeType:         string(stored.Type),
			EmbeddingVersion: generation.EmbeddingVersion,
			EmbeddingModel:   generation.EmbeddingModel,
			Content:          embeddingContentForDocument(stored),
		}); err != nil {
			return embeddingCount, err
		}
		embeddingCount++
	}
	return embeddingCount, nil
}
