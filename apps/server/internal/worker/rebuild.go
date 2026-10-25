package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"memstation/apps/server/internal/security"

	"github.com/hibiken/asynq"

	"memstation/apps/server/internal/domain"
	analysisservice "memstation/apps/server/internal/service/analysis"
	conversationservice "memstation/apps/server/internal/service/conversation"
)

var errGenerationSourceSuperseded = errors.New("memory generation source has been superseded")

func handleMemoryRebuildConversation(logger *slog.Logger, services Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		ctx, scopeErr := bindTaskScope(ctx, task, services)
		if scopeErr != nil {
			return scopeErr
		}
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
		if generation.Status == domain.MemoryGenerationStatusDiscarded || generation.Status == domain.MemoryGenerationStatusActivated {
			return nil
		}
		if generation.Status == domain.MemoryGenerationStatusCompleted {
			return completeRebuildSource(ctx, services, payload, 0)
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
			return completeRebuildSource(ctx, services, payload, 0)
		}
		if err := services.Generations.MarkGenerationSourceRunning(ctx, payload.UserID, payload.GenerationID, payload.SnapshotID); err != nil {
			return err
		}
		if generation.Metadata["rebuild_mode"] == "preserve_authoritative_records" {
			return completeRebuildSource(ctx, services, payload, 0)
		}
		logger.InfoContext(ctx, "memory rebuild conversation started", "generation_id", payload.GenerationID, "snapshot_id", payload.SnapshotID)
		embeddingCount, err := rebuildSnapshotIntoGeneration(ctx, services, generation, snapshot)
		if errors.Is(err, errGenerationSourceSuperseded) {
			logger.InfoContext(ctx, "memory rebuild conversation stopped because a newer snapshot superseded it", "generation_id", payload.GenerationID, "snapshot_id", payload.SnapshotID)
			return completeRebuildSource(ctx, services, payload, 0)
		}
		if err != nil {
			_, _ = services.Generations.MarkGenerationSourceFailed(ctx, payload.UserID, payload.GenerationID, payload.SnapshotID, err.Error())
			return err
		}
		return completeRebuildSource(ctx, services, payload, embeddingCount)
	}
}

func completeRebuildSource(ctx context.Context, services Services, payload MemoryRebuildConversationPayload, embeddingCount int) error {
	_, ready, err := services.Generations.MarkGenerationSourceCompleted(ctx, payload.UserID, payload.GenerationID, payload.SnapshotID, embeddingCount)
	if err != nil {
		return err
	}
	if ready && services.Publisher != nil && !services.Generations.DurableTasks() {
		return services.Publisher.Publish(ctx, domain.TaskMessage{
			Type: TypeMemoryRebuildActivate, Queue: QueueDefault,
			UniqueKey: TypeMemoryRebuildActivate + ":" + payload.GenerationID,
			Payload:   map[string]any{"user_id": payload.UserID, "workspace_id": security.WorkspaceID(ctx, payload.UserID), "generation_id": payload.GenerationID},
		})
	}
	return nil
}

func handleMemoryRebuildActivate(logger *slog.Logger, services Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		ctx, scopeErr := bindTaskScope(ctx, task, services)
		if scopeErr != nil {
			return scopeErr
		}
		if services.Generations == nil {
			return errors.New("memory generation service is not configured")
		}
		var payload MemoryRebuildActivatePayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		generation, err := services.Generations.ActivateCompletedGeneration(ctx, payload.UserID, payload.GenerationID)
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "memory rebuild activation processed", "generation_id", generation.GenerationID, "user_id", generation.UserID, "status", generation.Status)
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
		applyGenerationToSegments(snapshot.UserID, generation.GenerationID, conversationservice.BuildSegmentsFromTimeline(snapshot.UserID, snapshot.ConversationID, snapshot.Messages, snapshot.Parts, snapshot.Artifacts, snapshot.UpdatedAt, conversationservice.DefaultSegmentOptions())),
	)
	if err := ensureCurrentGenerationSource(ctx, services, snapshot.UserID, generation.GenerationID, snapshot.SnapshotID); err != nil {
		return 0, err
	}
	if err := cleanupStaleSegments(ctx, services, snapshot.UserID, generation.GenerationID, snapshot.ConversationID, segments); err != nil {
		return 0, err
	}

	if services.Analysis == nil {
		return 0, errors.New("memory LLM is not configured")
	}
	generated, err := summarizeWithCheckpoint(ctx, services, analysisservice.Input{UserID: snapshot.UserID, ConversationID: snapshot.ConversationID, Conversation: conversation}, scopeStableID(snapshot.UserID, generation.GenerationID, summaryDocumentID(snapshot.ConversationID, 1)), generation.GenerationID)
	if err != nil {
		return 0, err
	}
	summaryText := generated.Summary
	chunkCount := generated.ChunkCount
	summaryEvidences := buildNodeEvidences(snapshot.UserID, generation.GenerationID, "", snapshot.ConversationID, snapshot.Parts, snapshot.Artifacts, nil, nil)
	summaryDoc := applyGenerationToDocument(snapshot.UserID, generation.GenerationID, domain.MemoryDocument{
		ID:             summaryDocumentID(snapshot.ConversationID, 1),
		NodeID:         summaryNodeID(snapshot.ConversationID, 1),
		UserID:         snapshot.UserID,
		ConversationID: snapshot.ConversationID,
		Type:           domain.MemoryTypeSummary,
		Content:        summaryText,
		Importance:     generated.Importance,
		DecayFactor:    generated.DecayFactor,
		Metadata: mergeMetadata(snapshotMetadata(snapshot), map[string]any{
			"summary_version":             1,
			"finalize_reason":             finalizeReason,
			"topic_label":                 generated.TopicLabel,
			"keywords":                    generated.Keywords,
			"extraction_method":           "llm",
			"_llm_summary_checkpoint":     newSummaryCheckpoint(conversation, generated),
			"segment_count":               len(segments),
			"segment_strategy":            "dialogue_window_v1",
			"segment_overlap_messages":    conversationservice.DefaultSegmentOptions().Overlap,
			"summary_chunk_count":         chunkCount,
			"summary_chunk_max_bytes":     16000,
			"summary_chunk_overlap_bytes": 0,
			"modalities":                  modalitiesFromParts(snapshot.Parts),
			"evidence_count":              len(summaryEvidences),
			"source_part_count":           len(snapshot.Parts),
			"has_source_blob":             hasSourceBlob(snapshot.Parts),
		}),
	})
	if err := applyDocumentScore(ctx, services.Scorer, &summaryDoc, conversation.UpdatedAt); err != nil {
		return 0, err
	}
	if err := ensureCurrentGenerationSource(ctx, services, snapshot.UserID, generation.GenerationID, snapshot.SnapshotID); err != nil {
		return 0, err
	}
	summaryDoc, err = storeSummaryWithCheckpoint(ctx, services, summaryDoc)
	if err != nil {
		return 0, fmt.Errorf("store rebuilt summary: %w", err)
	}
	if err := storeDocumentEvidences(ctx, services, snapshot.UserID, generation.GenerationID, summaryDoc.NodeID, snapshot.ConversationID, summaryEvidences); err != nil {
		return 0, err
	}
	segmentDocs, err := storeSegmentDocuments(ctx, services, segments, summaryDoc, conversation.UpdatedAt, snapshot.Parts, snapshot.Artifacts)
	if err != nil {
		return 0, fmt.Errorf("store rebuilt segments: %w", err)
	}

	analyzer := services.Analysis
	if analyzer == nil {
		return 0, errors.New("memory LLM is not configured")
	}
	existingFacts, err := services.Memories.ListDocumentsInGeneration(ctx, snapshot.UserID, generation.GenerationID, []domain.MemoryType{domain.MemoryTypeFact}, 500)
	if err != nil {
		return 0, err
	}
	result, err := analyzeWithCheckpoint(ctx, services, analysisservice.Input{
		UserID:         snapshot.UserID,
		ConversationID: snapshot.ConversationID,
		Conversation:   conversation,
		Summary:        summaryDoc,
		ExistingFacts:  existingFacts,
	})
	if err != nil {
		return 0, fmt.Errorf("analyze rebuilt source: %w", err)
	}
	factDocs := result.Facts
	if len(factDocs) == 0 && result.Fact.ID != "" {
		factDocs = []domain.MemoryDocument{result.Fact}
	}
	factDocs = append([]domain.MemoryDocument(nil), factDocs...)
	for idx := range factDocs {
		factDocs[idx] = applyGenerationToDocument(snapshot.UserID, generation.GenerationID, factDocs[idx])
		factDocs[idx].Metadata = mergeMetadata(factDocs[idx].Metadata, snapshotMetadata(snapshot))
		if err := applyDocumentScore(ctx, services.Scorer, &factDocs[idx], conversation.UpdatedAt); err != nil {
			return 0, err
		}
		if err := services.Memories.StoreDocument(ctx, factDocs[idx]); err != nil {
			return 0, fmt.Errorf("store rebuilt fact: %w", err)
		}
		if len(summaryEvidences) > 0 {
			factEvidences := groundedNodeEvidences(factDocs[idx], summaryEvidences)
			if err := services.Memories.ReplaceEvidences(ctx, snapshot.UserID, factDocs[idx].NodeID, factEvidences); err != nil {
				return 0, err
			}
		}
	}
	if len(factDocs) > 0 {
		result.Fact = factDocs[0]
	}
	result.Facts = factDocs
	result.Relations = applySnapshotMetadataToRelations(snapshot, applyGenerationToRelations(snapshot.UserID, generation.GenerationID, result.Relations))
	if result.Impression.ID != "" {
		result.Impression = applyGenerationToDocument(snapshot.UserID, generation.GenerationID, result.Impression)
		result.Impression.Metadata = mergeMetadata(result.Impression.Metadata, snapshotMetadata(snapshot))
		if err := applyDocumentScore(ctx, services.Scorer, &result.Impression, conversation.UpdatedAt); err != nil {
			return 0, err
		}
		if err := ensureCurrentGenerationSource(ctx, services, snapshot.UserID, generation.GenerationID, snapshot.SnapshotID); err != nil {
			return 0, err
		}
		if err := services.Memories.StoreDocument(ctx, result.Impression); err != nil {
			return 0, fmt.Errorf("store rebuilt impression: %w", err)
		}
		if len(summaryEvidences) > 0 {
			impressionEvidences := groundedNodeEvidences(result.Impression, summaryEvidences)
			if err := services.Memories.ReplaceEvidences(ctx, snapshot.UserID, result.Impression.NodeID, impressionEvidences); err != nil {
				return 0, err
			}
		}

	}
	genericDocs := append([]domain.MemoryDocument(nil), result.GenericItems...)
	for idx := range genericDocs {
		genericDocs[idx] = applyGenerationToDocument(snapshot.UserID, generation.GenerationID, genericDocs[idx])
		genericDocs[idx].Metadata = mergeMetadata(genericDocs[idx].Metadata, snapshotMetadata(snapshot))
		genericDocs[idx] = mergeCanonicalEntityDocument(ctx, services, genericDocs[idx])
		if err := applyDocumentScore(ctx, services.Scorer, &genericDocs[idx], conversation.UpdatedAt); err != nil {
			return 0, err
		}
		if err := ensureCurrentGenerationSource(ctx, services, snapshot.UserID, generation.GenerationID, snapshot.SnapshotID); err != nil {
			return 0, err
		}
		if err := services.Memories.StoreDocument(ctx, genericDocs[idx]); err != nil {
			return 0, fmt.Errorf("store rebuilt generic record: %w", err)
		}
		if len(summaryEvidences) > 0 {
			itemEvidences := groundedNodeEvidences(genericDocs[idx], summaryEvidences)
			var evidenceErr error
			if genericDocs[idx].Type == domain.MemoryTypeEntity {
				evidenceErr = services.Memories.MergeEvidences(ctx, snapshot.UserID, genericDocs[idx].NodeID, itemEvidences)
			} else {
				evidenceErr = services.Memories.ReplaceEvidences(ctx, snapshot.UserID, genericDocs[idx].NodeID, itemEvidences)
			}
			if evidenceErr != nil {
				return 0, evidenceErr
			}
		}
	}
	result.GenericItems = genericDocs
	for _, relation := range result.Relations {
		enriched, evidenceErr := relationWithEvidence(ctx, services, relation)
		if evidenceErr != nil {
			return 0, evidenceErr
		}
		relation = enriched
		if err := services.Memories.LinkDocuments(ctx, relation); err != nil {
			return 0, err
		}
	}

	docs := make([]domain.MemoryDocument, 0, 1+len(segmentDocs)+len(factDocs)+1+len(genericDocs))
	docs = append(docs, summaryDoc)
	docs = append(docs, segmentDocs...)
	docs = append(docs, factDocs...)
	if result.Impression.ID != "" {
		docs = append(docs, result.Impression)
	}
	docs = append(docs, genericDocs...)
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
