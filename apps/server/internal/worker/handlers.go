package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	analysisservice "memstation/apps/server/internal/service/analysis"
	conversationservice "memstation/apps/server/internal/service/conversation"
	memoryservice "memstation/apps/server/internal/service/memory"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	multimodalservice "memstation/apps/server/internal/service/multimodal"
)

const (
	summaryChunkMaxTokens     = 6000
	summaryChunkOverlapTokens = 600
)

type Services struct {
	Conversations        ports.ConversationRepository
	MediaStore           ports.MediaRepository
	Memories             ports.MemoryRepository
	Generations          *memorygenservice.Service
	Vectors              ports.VectorStore
	Publisher            ports.TaskPublisher
	Analysis             *analysisservice.Service
	Multimodal           *multimodalservice.Service
	Scorer               *memoryservice.Scorer
	EmbeddingModel       string
	AllowedKeyMemoryKeys map[string]struct{}
	RecordAudit          func(context.Context, domain.AuditEvent)
	InvalidateKeyMemory  func(context.Context, string)
}

func NewServeMux(logger *slog.Logger, services Services) *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeConversationFinalize, handleConversationFinalize(logger, services))
	mux.HandleFunc(TypeMediaExtract, handleMediaExtract(logger, services))
	mux.HandleFunc(TypeSummaryGenerate, handleSummaryGenerate(logger, services))
	mux.HandleFunc(TypeEmbeddingGenerate, handleEmbeddingGenerate(logger, services))
	mux.HandleFunc(TypeAnalysisExtract, handleAnalysisExtract(logger, services))
	mux.HandleFunc(TypeKeyMemoryReconcile, handleKeyMemoryReconcile(logger, services))
	mux.HandleFunc(TypeMemoryRebuildConversation, handleMemoryRebuildConversation(logger, services))
	mux.HandleFunc(TypeMemoryRebuildActivate, handleMemoryRebuildActivate(logger, services))
	return mux
}

func handleConversationFinalize(logger *slog.Logger, services Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		payload := map[string]any{}
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		conversationID := stringValue(payload, "conversation_id")
		userID := stringValue(payload, "user_id")
		finalizeReason := stringValue(payload, "finalize_reason")
		finalizeStatus := finalizeStatusValue(payload)
		logger.InfoContext(ctx, "conversation finalize task received", "payload", string(task.Payload()))
		conversation, err := services.Conversations.GetConversation(ctx, userID, conversationID)
		if err != nil {
			return err
		}
		stateMetadata := map[string]any{
			"finalize_reason": finalizeReason,
			"finalized_at":    time.Now().UTC().Format(time.RFC3339),
		}
		if conversationSourceRetainEnabled(conversation) && services.Generations != nil {
			snapshot, _, err := services.Generations.CaptureSourceSnapshot(ctx, conversation)
			if err != nil {
				return err
			}
			for key, value := range snapshotMetadata(snapshot) {
				stateMetadata[key] = value
			}
			if candidate, ok, err := services.Generations.GetInProgressGeneration(ctx, userID); err != nil {
				return err
			} else if ok {
				source, added, err := services.Generations.AddSnapshotToGeneration(ctx, candidate.GenerationID, snapshot)
				if err != nil {
					return err
				}
				if added {
					if err := services.Publisher.Publish(ctx, domain.TaskMessage{
						Type:      TypeMemoryRebuildConversation,
						Queue:     QueueBulk,
						UniqueKey: TypeMemoryRebuildConversation + ":" + candidate.GenerationID + ":" + snapshot.SnapshotID,
						Payload: map[string]any{
							"user_id":       userID,
							"generation_id": candidate.GenerationID,
							"snapshot_id":   source.SnapshotID,
						},
					}); err != nil {
						return err
					}
				}
			}
		}
		if !conversationMemoryPersistEnabled(conversation) {
			skipped := domain.ProcessingStatusSkipped
			if err := services.Conversations.UpdateConversationState(ctx, domain.ConversationStateUpdate{
				ConversationID: conversationID,
				UserID:         userID,
				FinalizeStatus: &finalizeStatus,
				SummaryStatus:  &skipped,
				AnalysisStatus: &skipped,
				Metadata:       mergeMetadata(stateMetadata, map[string]any{"memory_persist_enabled": false}),
			}); err != nil {
				return err
			}
			if !conversationSourceRetainEnabled(conversation) || services.Multimodal == nil {
				return nil
			}
			nextType := TypeSummaryGenerate
			uniqueKey := TypeSummaryGenerate + ":" + conversationID
			if services.Multimodal != nil {
				nextType = TypeMediaExtract
				uniqueKey = TypeMediaExtract + ":" + conversationID
			}
			return services.Publisher.Publish(ctx, domain.TaskMessage{
				Type:      nextType,
				Queue:     QueueDefault,
				UniqueKey: uniqueKey,
				Payload: map[string]any{
					"conversation_id": conversationID,
					"user_id":         userID,
					"finalize_reason": finalizeReason,
				},
			})
		}
		generation, err := resolveProcessingGeneration(ctx, services, userID, "")
		if err != nil {
			return err
		}
		summaryStatus := conversation.SummaryStatus
		if summaryStatus == "" || summaryStatus == domain.ProcessingStatusPending || summaryStatus == domain.ProcessingStatusFailed {
			summaryStatus = domain.ProcessingStatusQueued
		}
		if err := services.Conversations.UpdateConversationState(ctx, domain.ConversationStateUpdate{
			ConversationID: conversationID,
			UserID:         userID,
			FinalizeStatus: &finalizeStatus,
			SummaryStatus:  &summaryStatus,
			Metadata: mergeMetadata(stateMetadata, map[string]any{
				"memory_generation_id": generation.GenerationID,
				"embedding_version":    generation.EmbeddingVersion,
				"embedding_model":      generation.EmbeddingModel,
			}),
		}); err != nil {
			return err
		}
		if conversation.SummaryStatus == domain.ProcessingStatusCompleted || conversation.SummaryStatus == domain.ProcessingStatusRunning || conversation.SummaryStatus == domain.ProcessingStatusQueued {
			return nil
		}
		nextType := TypeSummaryGenerate
		uniqueKey := TypeSummaryGenerate + ":" + conversationID
		if services.Multimodal != nil {
			nextType = TypeMediaExtract
			uniqueKey = TypeMediaExtract + ":" + conversationID
		}
		return services.Publisher.Publish(ctx, domain.TaskMessage{
			Type:      nextType,
			Queue:     QueueDefault,
			UniqueKey: uniqueKey,
			Payload: map[string]any{
				"conversation_id":   conversationID,
				"user_id":           userID,
				"finalize_reason":   finalizeReason,
				"generation_id":     generation.GenerationID,
				"embedding_version": generation.EmbeddingVersion,
				"embedding_model":   generation.EmbeddingModel,
			},
		})
	}
}

func handleMediaExtract(logger *slog.Logger, services Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		var payload MediaExtractPayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		conversation, err := services.Conversations.GetConversation(ctx, payload.UserID, payload.ConversationID)
		if err != nil {
			return err
		}
		if services.Multimodal == nil {
			if conversationMemoryPersistEnabled(conversation) {
				return services.Publisher.Publish(ctx, domain.TaskMessage{
					Type:      TypeSummaryGenerate,
					Queue:     QueueDefault,
					UniqueKey: TypeSummaryGenerate + ":" + payload.ConversationID,
					Payload: map[string]any{
						"conversation_id":   payload.ConversationID,
						"user_id":           payload.UserID,
						"summary_version":   1,
						"finalize_reason":   payload.FinalizeReason,
						"generation_id":     payload.GenerationID,
						"embedding_version": payload.EmbeddingVersion,
						"embedding_model":   payload.EmbeddingModel,
					},
				})
			}
			return nil
		}

		parts, err := services.Multimodal.MaterializeConversation(ctx, conversation)
		if err != nil {
			return err
		}
		artifacts, err := services.Multimodal.ExtractConversation(ctx, conversation, parts)
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "media extracted", "conversation_id", payload.ConversationID, "part_count", len(parts), "artifact_count", len(artifacts))
		if err := services.Conversations.UpdateConversationState(ctx, domain.ConversationStateUpdate{
			ConversationID: payload.ConversationID,
			UserID:         payload.UserID,
			Metadata: map[string]any{
				"part_count":            len(parts),
				"artifact_count":        len(artifacts),
				"timeline_materialized": true,
				"timeline_modalities":   modalitiesFromParts(parts),
			},
		}); err != nil {
			return err
		}
		if !conversationMemoryPersistEnabled(conversation) {
			return nil
		}
		return services.Publisher.Publish(ctx, domain.TaskMessage{
			Type:      TypeSummaryGenerate,
			Queue:     QueueDefault,
			UniqueKey: TypeSummaryGenerate + ":" + payload.ConversationID,
			Payload: map[string]any{
				"conversation_id":   payload.ConversationID,
				"user_id":           payload.UserID,
				"summary_version":   1,
				"finalize_reason":   payload.FinalizeReason,
				"generation_id":     payload.GenerationID,
				"embedding_version": payload.EmbeddingVersion,
				"embedding_model":   payload.EmbeddingModel,
			},
		})
	}
}

func handleSummaryGenerate(logger *slog.Logger, services Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		var payload SummaryPayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		generation, err := resolveProcessingGeneration(ctx, services, payload.UserID, payload.GenerationID)
		if err != nil {
			return err
		}
		payload.GenerationID = generation.GenerationID
		if payload.EmbeddingVersion <= 0 {
			payload.EmbeddingVersion = generation.EmbeddingVersion
		}
		if strings.TrimSpace(payload.EmbeddingModel) == "" {
			payload.EmbeddingModel = generation.EmbeddingModel
		}
		conversation, err := services.Conversations.GetConversation(ctx, payload.UserID, payload.ConversationID)
		if err != nil {
			return err
		}
		if conversation.SummaryStatus != domain.ProcessingStatusCompleted {
			running := domain.ProcessingStatusRunning
			if err := services.Conversations.UpdateConversationState(ctx, domain.ConversationStateUpdate{
				ConversationID: payload.ConversationID,
				UserID:         payload.UserID,
				SummaryStatus:  &running,
				Metadata: map[string]any{
					"summary_started_at": time.Now().UTC().Format(time.RFC3339),
				},
			}); err != nil {
				return err
			}
		}
		parts, artifacts, err := ensureConversationTimeline(ctx, services, conversation)
		if err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, true)
			return err
		}
		conversation.Parts = parts
		conversation.Artifacts = artifacts
		segments := applyGenerationToSegments(payload.UserID, payload.GenerationID, conversationservice.BuildSegmentsFromTimeline(payload.UserID, payload.ConversationID, conversation.Messages, parts, artifacts, conversation.UpdatedAt, conversationservice.DefaultSegmentOptions()))
		if err := services.Conversations.ReplaceSegments(ctx, payload.UserID, payload.ConversationID, segments); err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, true)
			return err
		}
		if err := cleanupStaleSegments(ctx, services, payload.UserID, payload.GenerationID, payload.ConversationID, segments); err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, true)
			return err
		}
		conversation.Segments = segments
		summaryText := buildSummary(conversation.Messages, parts, artifacts)
		if summaryText == "" {
			summaryText = conversationservice.SummarizeSegments(segments)
		}
		chunkCount := summaryChunkCount(conversation.Messages, parts, artifacts)
		summaryEvidences := buildNodeEvidences(payload.UserID, payload.GenerationID, "", payload.ConversationID, parts, artifacts, nil, nil)
		doc := domain.MemoryDocument{
			ID:             summaryDocumentID(payload.ConversationID, payload.SummaryVersion),
			NodeID:         summaryNodeID(payload.ConversationID, payload.SummaryVersion),
			UserID:         payload.UserID,
			ConversationID: payload.ConversationID,
			Type:           domain.MemoryTypeSummary,
			Content:        summaryText,
			Importance:     1,
			DecayFactor:    1,
			Metadata: map[string]any{
				"summary_version":          payload.SummaryVersion,
				"finalize_reason":          payload.FinalizeReason,
				"topic_label":              summarizeTopic(summaryText),
				"keywords":                 extractKeywords(summaryText),
				"segment_count":            len(segments),
				"segment_strategy":         "dialogue_window_v1",
				"segment_overlap_messages": conversationservice.DefaultSegmentOptions().Overlap,
				"summary_chunk_count":      chunkCount,
				"summary_chunk_max_tokens": summaryChunkMaxTokens,
				"summary_chunk_overlap":    summaryChunkOverlapTokens,
				"modalities":               modalitiesFromParts(parts),
				"evidence_count":           len(summaryEvidences),
				"source_part_count":        len(parts),
				"has_source_blob":          hasSourceBlob(parts),
			},
		}
		doc = applyGenerationToDocument(payload.UserID, payload.GenerationID, doc)
		applyDocumentScore(ctx, services.Scorer, &doc, conversation.UpdatedAt)
		logger.InfoContext(ctx, "summary stored", "conversation_id", payload.ConversationID)
		if err := services.Memories.StoreDocument(ctx, doc); err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, true)
			return err
		}
		if err := storeDocumentEvidences(ctx, services, payload.UserID, payload.GenerationID, doc.NodeID, payload.ConversationID, summaryEvidences); err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, true)
			return err
		}
		segmentDocs, err := storeSegmentDocuments(ctx, services, segments, doc, conversation.UpdatedAt, parts, artifacts)
		if err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, true)
			return err
		}
		completed := domain.ProcessingStatusCompleted
		analysisQueued := domain.ProcessingStatusQueued
		if err := services.Conversations.UpdateConversationState(ctx, domain.ConversationStateUpdate{
			ConversationID: payload.ConversationID,
			UserID:         payload.UserID,
			SummaryStatus:  &completed,
			AnalysisStatus: &analysisQueued,
			Metadata: map[string]any{
				"summary_document_id":  doc.ID,
				"segment_count":        len(segments),
				"segment_strategy":     "dialogue_window_v1",
				"memory_generation_id": payload.GenerationID,
			},
		}); err != nil {
			return err
		}
		if err := services.Publisher.Publish(ctx, domain.TaskMessage{
			Type:      TypeEmbeddingGenerate,
			Queue:     QueueDefault,
			UniqueKey: TypeEmbeddingGenerate + ":" + payload.GenerationID + ":" + doc.NodeID + ":" + itoa(payload.EmbeddingVersion),
			Payload: map[string]any{
				"user_id":           payload.UserID,
				"generation_id":     payload.GenerationID,
				"node_id":           doc.NodeID,
				"node_type":         string(doc.Type),
				"embedding_version": payload.EmbeddingVersion,
				"embedding_model":   payload.EmbeddingModel,
				"content":           embeddingContentForDocument(doc),
			},
		}); err != nil {
			return err
		}
		for _, segmentDoc := range segmentDocs {
			if err := services.Publisher.Publish(ctx, domain.TaskMessage{
				Type:      TypeEmbeddingGenerate,
				Queue:     QueueDefault,
				UniqueKey: TypeEmbeddingGenerate + ":" + payload.GenerationID + ":" + segmentDoc.NodeID + ":" + itoa(payload.EmbeddingVersion),
				Payload: map[string]any{
					"user_id":           payload.UserID,
					"generation_id":     payload.GenerationID,
					"node_id":           segmentDoc.NodeID,
					"node_type":         string(segmentDoc.Type),
					"embedding_version": payload.EmbeddingVersion,
					"embedding_model":   payload.EmbeddingModel,
					"content":           embeddingContentForDocument(segmentDoc),
				},
			}); err != nil {
				return err
			}
		}
		return services.Publisher.Publish(ctx, domain.TaskMessage{
			Type:      TypeAnalysisExtract,
			Queue:     QueueLow,
			UniqueKey: TypeAnalysisExtract + ":" + payload.ConversationID,
			Payload: map[string]any{
				"user_id":           payload.UserID,
				"conversation_id":   payload.ConversationID,
				"summary_ids":       []string{doc.ID},
				"generation_id":     payload.GenerationID,
				"embedding_version": payload.EmbeddingVersion,
				"embedding_model":   payload.EmbeddingModel,
			},
		})
	}
}

func handleEmbeddingGenerate(logger *slog.Logger, services Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		var payload EmbeddingPayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		logger.InfoContext(ctx, "embedding task received", "node_id", payload.NodeID, "node_type", payload.NodeType)
		userID := payload.UserID
		nodeType := payload.NodeType
		content := payload.Content
		if current, ok, err := currentStoredDocumentForEmbeddingByNode(ctx, services, payload.UserID, payload.GenerationID, payload.NodeID, payload.NodeType); err != nil {
			return err
		} else if ok {
			userID = current.UserID
			nodeType = string(current.Type)
			content = embeddingContentForDocument(current)
		}
		if content == "" {
			content = payload.NodeID
		}
		return services.Vectors.Upsert(ctx, domain.VectorUpsertRequest{
			UserID:           userID,
			GenerationID:     payload.GenerationID,
			NodeID:           payload.NodeID,
			NodeType:         nodeType,
			EmbeddingVersion: payload.EmbeddingVersion,
			EmbeddingModel:   payload.EmbeddingModel,
			Content:          content,
		})
	}
}

func handleAnalysisExtract(logger *slog.Logger, services Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		var payload AnalysisPayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		generation, err := resolveProcessingGeneration(ctx, services, payload.UserID, payload.GenerationID)
		if err != nil {
			return err
		}
		payload.GenerationID = generation.GenerationID
		if payload.EmbeddingVersion <= 0 {
			payload.EmbeddingVersion = generation.EmbeddingVersion
		}
		if strings.TrimSpace(payload.EmbeddingModel) == "" {
			payload.EmbeddingModel = generation.EmbeddingModel
		}
		conversation, err := services.Conversations.GetConversation(ctx, payload.UserID, payload.ConversationID)
		if err != nil {
			return err
		}
		if conversation.AnalysisStatus != domain.ProcessingStatusCompleted {
			running := domain.ProcessingStatusRunning
			if err := services.Conversations.UpdateConversationState(ctx, domain.ConversationStateUpdate{
				ConversationID: payload.ConversationID,
				UserID:         payload.UserID,
				AnalysisStatus: &running,
			}); err != nil {
				return err
			}
		}
		docs, err := services.Memories.ListDocumentsByConversationInGeneration(ctx, payload.UserID, payload.GenerationID, payload.ConversationID)
		if err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, false)
			return err
		}
		summaryDoc := selectSummaryDocument(docs, payload.SummaryIDs)
		existingFacts, err := services.Memories.ListDocumentsInGeneration(ctx, payload.UserID, payload.GenerationID, []domain.MemoryType{domain.MemoryTypeFact}, 500)
		if err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, false)
			return err
		}
		analyzer := services.Analysis
		if analyzer == nil {
			analyzer = analysisservice.NewService()
		}

		result, err := analyzer.AnalyzeConversation(ctx, analysisservice.Input{
			UserID:         payload.UserID,
			ConversationID: payload.ConversationID,
			Conversation:   conversation,
			Summary:        summaryDoc,
			ExistingFacts:  existingFacts,
		})
		if err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, false)
			return err
		}
		result.Fact = applyGenerationToDocument(payload.UserID, payload.GenerationID, result.Fact)
		result.Impression = applyGenerationToDocument(payload.UserID, payload.GenerationID, result.Impression)
		result.Relations = applyGenerationToRelations(payload.UserID, payload.GenerationID, result.Relations)
		applyDocumentScore(ctx, services.Scorer, &result.Fact, conversation.UpdatedAt)
		applyDocumentScore(ctx, services.Scorer, &result.Impression, conversation.UpdatedAt)
		if err := services.Memories.StoreDocument(ctx, result.Fact); err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, false)
			return err
		}
		if evidences, err := services.Memories.ListEvidences(ctx, payload.UserID, summaryDoc.NodeID); err == nil && len(evidences) > 0 {
			factEvidences := cloneNodeEvidences(payload.UserID, payload.GenerationID, result.Fact.NodeID, evidences)
			if err := services.Memories.ReplaceEvidences(ctx, payload.UserID, result.Fact.NodeID, factEvidences); err != nil {
				markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, false)
				return err
			}
		}
		if err := services.Memories.StoreDocument(ctx, result.Impression); err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, false)
			return err
		}
		if evidences, err := services.Memories.ListEvidences(ctx, payload.UserID, summaryDoc.NodeID); err == nil && len(evidences) > 0 {
			impressionEvidences := cloneNodeEvidences(payload.UserID, payload.GenerationID, result.Impression.NodeID, evidences)
			if err := services.Memories.ReplaceEvidences(ctx, payload.UserID, result.Impression.NodeID, impressionEvidences); err != nil {
				markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, false)
				return err
			}
		}
		for _, relation := range result.Relations {
			if err := services.Memories.LinkDocuments(ctx, relation); err != nil {
				markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, false)
				return err
			}
		}
		logger.InfoContext(ctx, "analysis stored", "conversation_id", payload.ConversationID)
		completed := domain.ProcessingStatusCompleted
		if err := services.Conversations.UpdateConversationState(ctx, domain.ConversationStateUpdate{
			ConversationID: payload.ConversationID,
			UserID:         payload.UserID,
			AnalysisStatus: &completed,
			Metadata: map[string]any{
				"analysis_fact_id":       result.Fact.ID,
				"analysis_impression_id": result.Impression.ID,
				"memory_generation_id":   payload.GenerationID,
			},
		}); err != nil {
			return err
		}
		followups := []domain.TaskMessage{
			{
				Type:      TypeEmbeddingGenerate,
				Queue:     QueueDefault,
				UniqueKey: TypeEmbeddingGenerate + ":" + payload.GenerationID + ":" + result.Fact.NodeID + ":" + itoa(payload.EmbeddingVersion),
				Payload: map[string]any{
					"user_id":           payload.UserID,
					"generation_id":     payload.GenerationID,
					"node_id":           result.Fact.NodeID,
					"node_type":         string(result.Fact.Type),
					"embedding_version": payload.EmbeddingVersion,
					"embedding_model":   payload.EmbeddingModel,
					"content":           result.Fact.Content,
				},
			},
			{
				Type:      TypeEmbeddingGenerate,
				Queue:     QueueDefault,
				UniqueKey: TypeEmbeddingGenerate + ":" + payload.GenerationID + ":" + result.Impression.NodeID + ":" + itoa(payload.EmbeddingVersion),
				Payload: map[string]any{
					"user_id":           payload.UserID,
					"generation_id":     payload.GenerationID,
					"node_id":           result.Impression.NodeID,
					"node_type":         string(result.Impression.Type),
					"embedding_version": payload.EmbeddingVersion,
					"embedding_model":   payload.EmbeddingModel,
					"content":           result.Impression.Content,
				},
			},
		}
		if generation.IsActive {
			followups = append(followups, domain.TaskMessage{
				Type:      TypeKeyMemoryReconcile,
				Queue:     QueueLow,
				UniqueKey: TypeKeyMemoryReconcile + ":" + payload.ConversationID,
				Payload: map[string]any{
					"user_id":         payload.UserID,
					"conversation_id": payload.ConversationID,
					"generation_id":   payload.GenerationID,
				},
			})
		}
		for _, followup := range followups {
			if err := services.Publisher.Publish(ctx, followup); err != nil {
				return err
			}
		}
		if !conversationSourceRetainEnabled(conversation) && services.Multimodal != nil {
			if err := services.Multimodal.CleanupConversationBlobs(ctx, payload.UserID, payload.ConversationID); err != nil {
				return err
			}
		}
		return nil
	}
}

func handleKeyMemoryReconcile(logger *slog.Logger, services Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		var payload KeyMemoryReconcilePayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		userID := strings.TrimSpace(payload.UserID)
		conversationID := strings.TrimSpace(payload.ConversationID)
		generationID := strings.TrimSpace(payload.GenerationID)
		if generationID == "" {
			generation, err := resolveProcessingGeneration(ctx, services, userID, "")
			if err != nil {
				return err
			}
			generationID = generation.GenerationID
		}
		if !isAllowedKey(services.AllowedKeyMemoryKeys, "recent_conversation") {
			return fmt.Errorf("key memory write is not allowed for key %q", "recent_conversation")
		}
		keyMemory := domain.KeyMemory{
			ID:       keyMemoryID(userID, "recent_conversation"),
			UserID:   userID,
			Key:      "recent_conversation",
			Value:    conversationID,
			Category: "system",
			Source:   "worker",
			Metadata: map[string]any{
				"node_id":         keyMemoryNodeID(userID, "recent_conversation"),
				"generation_id":   generationID,
				"conversation_id": conversationID,
			},
		}
		logger.InfoContext(ctx, "key memory reconciled", "conversation_id", conversationID)
		if err := services.Memories.UpsertKeyMemory(ctx, keyMemory); err != nil {
			return err
		}
		if err := syncConversationKeyMemoryRelations(ctx, services.Memories, keyMemory, conversationID, generationID); err != nil {
			return err
		}
		if services.InvalidateKeyMemory != nil {
			services.InvalidateKeyMemory(ctx, userID)
		}
		recordAudit(ctx, services, domain.AuditEvent{
			ActorType:  "system",
			ActorID:    "worker",
			Action:     "keymemory_reconcile",
			TargetType: "key_memory",
			TargetID:   keyMemory.ID,
			Payload: map[string]any{
				"key":             keyMemory.Key,
				"conversation_id": conversationID,
			},
		})
		return nil
	}
}

func syncConversationKeyMemoryRelations(ctx context.Context, repo ports.MemoryRepository, km domain.KeyMemory, conversationID, generationID string) error {
	if repo == nil || km.UserID == "" || conversationID == "" {
		return nil
	}
	nodeID, _ := km.Metadata["node_id"].(string)
	if nodeID == "" {
		return nil
	}
	if err := repo.DeleteRelationsByTarget(ctx, km.UserID, nodeID, "promoted_to_key_memory"); err != nil {
		return err
	}
	docs, err := repo.ListDocumentsByConversationInGeneration(ctx, km.UserID, generationID, conversationID)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		if doc.NodeID == "" || doc.NodeID == nodeID {
			continue
		}
		if err := repo.LinkDocuments(ctx, domain.MemoryRelation{
			GenerationID: doc.GenerationID,
			UserID:       km.UserID,
			SrcNodeID:    doc.NodeID,
			DstNodeID:    nodeID,
			RelationType: "promoted_to_key_memory",
			Weight:       1,
			Source:       "worker",
			Metadata: map[string]any{
				"key":             km.Key,
				"conversation_id": conversationID,
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func buildSummary(messages []domain.UnifiedMessage, parts []domain.MessagePart, artifacts []domain.MessagePartArtifact) string {
	if chunks := summaryChunks(messages, parts, artifacts); len(chunks) > 1 {
		return summarizeChunks(chunks)
	}
	promptSummary := strings.TrimSpace(conversationservice.BuildMemoryPromptInput(messages))
	timelineSummary := strings.TrimSpace(conversationservice.SummarizeMessagesWithTimeline(messages, parts, artifacts))
	switch {
	case timelineSummary != "" && promptSummary != "" && timelineSummary != promptSummary:
		if strings.Contains(strings.ToLower(promptSummary), "tool ") || strings.Contains(strings.ToLower(promptSummary), "tool_") {
			return truncate(timelineSummary+" | "+promptSummary, 700)
		}
		return timelineSummary
	case timelineSummary != "":
		return timelineSummary
	case promptSummary != "":
		return promptSummary
	}
	return ""
}

func summaryChunkCount(messages []domain.UnifiedMessage, parts []domain.MessagePart, artifacts []domain.MessagePartArtifact) int {
	chunks := summaryChunks(messages, parts, artifacts)
	if len(chunks) == 0 {
		return 1
	}
	return len(chunks)
}

func summaryChunks(messages []domain.UnifiedMessage, parts []domain.MessagePart, artifacts []domain.MessagePartArtifact) []string {
	source := buildSummaryChunkSource(messages, parts, artifacts)
	if estimateSummaryTokens(source) <= summaryChunkMaxTokens {
		if strings.TrimSpace(source) == "" {
			return nil
		}
		return []string{source}
	}
	return chunkTextByTokens(source, summaryChunkMaxTokens, summaryChunkOverlapTokens)
}

func buildSummaryChunkSource(messages []domain.UnifiedMessage, parts []domain.MessagePart, artifacts []domain.MessagePartArtifact) string {
	if len(parts) > 0 {
		artifactIndex := make(map[string][]domain.MessagePartArtifact, len(parts))
		for _, artifact := range artifacts {
			artifactIndex[artifact.PartID] = append(artifactIndex[artifact.PartID], artifact)
		}
		lines := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Role == domain.RoleSystem {
				continue
			}
			textParts := make([]string, 0, 1+len(artifactIndex[part.PartID]))
			for _, artifact := range artifactIndex[part.PartID] {
				if text := strings.TrimSpace(artifact.TextContent); text != "" {
					textParts = append(textParts, text)
				}
			}
			if len(textParts) == 0 {
				if text := strings.TrimSpace(firstNonEmptyWorkerString(part.TextContent, part.RawPayload)); text != "" {
					textParts = append(textParts, text)
				}
			}
			if len(textParts) == 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s: %s", part.Role, strings.Join(textParts, " | ")))
		}
		return strings.Join(lines, "\n")
	}

	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.Role == domain.RoleSystem {
			continue
		}
		text := messageTextForSummaryChunk(message)
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", message.Role, text))
	}
	return strings.Join(lines, "\n")
}

func messageTextForSummaryChunk(message domain.UnifiedMessage) string {
	parts := make([]string, 0, 1+len(message.Blocks))
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range parts {
			if existing == value {
				return
			}
		}
		parts = append(parts, value)
	}
	add(message.Text)
	for _, block := range message.Blocks {
		if block.Type == domain.ContentBlockText {
			add(block.Text)
		}
	}
	if len(message.ToolCalls) > 0 {
		names := make([]string, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			if name := strings.TrimSpace(firstNonEmptyWorkerString(call.Name, call.ID)); name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			add("tool calls: " + strings.Join(names, ", "))
		}
	}
	if len(message.ToolResults) > 0 {
		names := make([]string, 0, len(message.ToolResults))
		for _, result := range message.ToolResults {
			if name := strings.TrimSpace(firstNonEmptyWorkerString(result.Name, result.ToolCallID)); name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			add("tool results: " + strings.Join(names, ", "))
		}
	}
	return strings.Join(parts, " | ")
}

func chunkTextByTokens(source string, maxTokens int, overlapTokens int) []string {
	words := strings.Fields(source)
	if len(words) == 0 {
		return nil
	}
	if maxTokens <= 0 {
		maxTokens = summaryChunkMaxTokens
	}
	if len(words) <= maxTokens && estimateSummaryTokens(source) <= maxTokens {
		return []string{strings.Join(words, " ")}
	}
	if len(words) <= 1 {
		return chunkTextByChars(source, maxTokens*4, overlapTokens*4)
	}
	if overlapTokens < 0 {
		overlapTokens = 0
	}
	if overlapTokens >= maxTokens {
		overlapTokens = maxTokens / 10
	}
	stride := maxTokens - overlapTokens
	chunks := make([]string, 0, len(words)/stride+1)
	for start := 0; start < len(words); start += stride {
		end := min(start+maxTokens, len(words))
		chunks = append(chunks, strings.Join(words[start:end], " "))
		if end == len(words) {
			break
		}
	}
	return chunks
}

func chunkTextByChars(source string, maxChars int, overlapChars int) []string {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	if maxChars <= 0 {
		maxChars = summaryChunkMaxTokens * 4
	}
	if len(source) <= maxChars {
		return []string{source}
	}
	if overlapChars < 0 {
		overlapChars = 0
	}
	if overlapChars >= maxChars {
		overlapChars = maxChars / 10
	}
	stride := maxChars - overlapChars
	chunks := make([]string, 0, len(source)/stride+1)
	for start := 0; start < len(source); start += stride {
		end := min(start+maxChars, len(source))
		chunks = append(chunks, source[start:end])
		if end == len(source) {
			break
		}
	}
	return chunks
}

func summarizeChunks(chunks []string) string {
	if len(chunks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(chunks))
	for idx, chunk := range chunks {
		parts = append(parts, fmt.Sprintf("Chunk %d/%d: %s", idx+1, len(chunks), compactSummaryChunk(chunk)))
	}
	return strings.Join(parts, " | ")
}

func compactSummaryChunk(chunk string) string {
	chunk = normalizeWhitespace(chunk)
	if len(chunk) <= 1400 {
		return chunk
	}
	return chunk[:900] + " ... " + chunk[len(chunk)-400:]
}

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func estimateSummaryTokens(value string) int {
	return max(len(strings.Fields(value)), len(value)/4)
}

func embeddingContentForDocument(doc domain.MemoryDocument) string {
	content := strings.TrimSpace(doc.Content)
	if doc.Type != domain.MemoryTypeSummary {
		if content == "" {
			return doc.NodeID
		}
		return content
	}

	parts := make([]string, 0, 3)
	if content != "" {
		parts = append(parts, content)
	}
	if topic := stringValue(doc.Metadata, "topic_label"); topic != "" {
		parts = append(parts, "Topic: "+topic)
	}
	if keywords := stringSliceValue(doc.Metadata, "keywords"); len(keywords) > 0 {
		parts = append(parts, "Keywords: "+strings.Join(keywords, ", "))
	}
	if len(parts) == 0 {
		return doc.NodeID
	}
	return strings.Join(parts, "\n")
}

func summarizeTopic(summary string) string {
	keywords := extractKeywords(summary)
	if len(keywords) == 0 {
		return "general"
	}
	if len(keywords) > 2 {
		keywords = keywords[:2]
	}
	return strings.Join(keywords, ", ")
}

func applyDocumentScore(ctx context.Context, scorer *memoryservice.Scorer, doc *domain.MemoryDocument, referenceTime time.Time) {
	if doc == nil {
		return
	}
	input := memoryservice.ScoreInput{
		UserID:         doc.UserID,
		MemoryType:     doc.Type,
		Content:        doc.Content,
		ConversationID: doc.ConversationID,
		Metadata:       cloneMap(doc.Metadata),
		ReferenceTime:  domain.DeriveActivityAt(doc.ActivityAt, referenceTime, referenceTime),
	}
	score := memoryservice.MemoryScore{
		Importance:  doc.Importance,
		DecayFactor: doc.DecayFactor,
		ActivityAt:  domain.DeriveActivityAt(doc.ActivityAt, referenceTime, referenceTime),
		Reason:      "preset",
	}
	if score.Importance == 0 {
		score.Importance = 1
	}
	if score.DecayFactor == 0 {
		score.DecayFactor = 1
	}
	if scorer != nil {
		score = scorer.Score(ctx, input)
	}
	doc.Importance = domain.ClampImportance(score.Importance)
	doc.DecayFactor = domain.ClampDecayFactor(score.DecayFactor)
	doc.ActivityAt = domain.DeriveActivityAt(score.ActivityAt, referenceTime, time.Now().UTC())
	doc.TimeTier = domain.ClassifyTimeTier(doc.ActivityAt, time.Now().UTC())
	if doc.Metadata == nil {
		doc.Metadata = map[string]any{}
	}
	doc.Metadata["activity_at"] = doc.ActivityAt.Format(time.RFC3339)
	doc.Metadata["time_tier"] = doc.TimeTier
	doc.Metadata["score_reason"] = score.Reason
}

func extractKeywords(summary string) []string {
	seen := map[string]struct{}{}
	words := strings.Fields(strings.ToLower(summary))
	out := make([]string, 0, 5)
	for _, word := range words {
		word = strings.Trim(word, ".,;:!?()[]{}\"'")
		if len(word) < 4 {
			continue
		}
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		out = append(out, word)
		if len(out) == 5 {
			break
		}
	}
	return out
}

func cloneMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func selectSummaryDocument(docs []domain.MemoryDocument, summaryIDs []string) domain.MemoryDocument {
	if len(docs) == 0 {
		return domain.MemoryDocument{}
	}
	if len(summaryIDs) > 0 {
		allowed := make(map[string]struct{}, len(summaryIDs))
		for _, id := range summaryIDs {
			id = strings.TrimSpace(id)
			if id != "" {
				allowed[id] = struct{}{}
			}
		}
		for _, doc := range docs {
			if doc.Type != domain.MemoryTypeSummary {
				continue
			}
			if _, ok := allowed[doc.ID]; ok {
				return doc
			}
		}
	}
	for _, doc := range docs {
		if doc.Type == domain.MemoryTypeSummary {
			return doc
		}
	}
	return domain.MemoryDocument{}
}

func currentStoredDocumentForEmbedding(ctx context.Context, services Services, expected domain.MemoryDocument) (domain.MemoryDocument, bool, error) {
	return currentStoredDocumentForEmbeddingByNode(ctx, services, expected.UserID, expected.GenerationID, expected.NodeID, string(expected.Type))
}

func currentStoredDocumentForEmbeddingByNode(ctx context.Context, services Services, userID, generationID, nodeID, nodeType string) (domain.MemoryDocument, bool, error) {
	if services.Memories == nil || strings.TrimSpace(nodeID) == "" {
		return domain.MemoryDocument{}, false, nil
	}
	documents, err := services.Memories.ListDocumentsByNodeIDsInGeneration(ctx, userID, generationID, []string{nodeID})
	if err != nil {
		return domain.MemoryDocument{}, false, err
	}
	for _, doc := range documents {
		if doc.NodeID != nodeID {
			continue
		}
		if nodeType != "" && string(doc.Type) != nodeType {
			continue
		}
		return doc, true, nil
	}
	return domain.MemoryDocument{}, false, nil
}

func sourceSnapshotVersionValue(metadata map[string]any) int {
	if len(metadata) == 0 {
		return 0
	}
	switch typed := metadata["source_snapshot_version"].(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		value, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return value
		}
	}
	return 0
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return fmt.Sprintf("%s...", value[:limit])
}

func itoa(value int) string {
	return fmt.Sprintf("%d", value)
}

func stringValue(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok {
		return ""
	}
	if typed, ok := value.(string); ok {
		return typed
	}
	return fmt.Sprintf("%v", value)
}

func stringSliceValue(payload map[string]any, key string) []string {
	value, ok := payload[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

func finalizeStatusValue(payload map[string]any) domain.FinalizeStatus {
	switch domain.FinalizeStatus(stringValue(payload, "finalize_status")) {
	case domain.FinalizeStatusTimeout:
		return domain.FinalizeStatusTimeout
	case domain.FinalizeStatusFailed:
		return domain.FinalizeStatusFailed
	default:
		return domain.FinalizeStatusEnded
	}
}

func markConversationFailed(ctx context.Context, services Services, userID, conversationID string, summaryFailed bool) {
	if services.Conversations == nil {
		return
	}
	failed := domain.ProcessingStatusFailed
	update := domain.ConversationStateUpdate{
		ConversationID: conversationID,
		UserID:         userID,
	}
	if summaryFailed {
		update.SummaryStatus = &failed
	} else {
		update.AnalysisStatus = &failed
	}
	_ = services.Conversations.UpdateConversationState(ctx, update)
}

func summaryDocumentID(conversationID string, version int) string {
	if version <= 0 {
		version = 1
	}
	return fmt.Sprintf("summary:%s:v%d", conversationID, version)
}

func summaryNodeID(conversationID string, version int) string {
	if version <= 0 {
		version = 1
	}
	return fmt.Sprintf("node:summary:%s:v%d", conversationID, version)
}

func keyMemoryID(userID, key string) string {
	return fmt.Sprintf("keymemory:%s:%s", userID, key)
}

func keyMemoryNodeID(userID, key string) string {
	return fmt.Sprintf("node:keymemory:%s:%s", userID, key)
}

func isAllowedKey(allowed map[string]struct{}, key string) bool {
	if len(allowed) == 0 {
		return false
	}
	_, ok := allowed[key]
	return ok
}

func recordAudit(ctx context.Context, services Services, event domain.AuditEvent) {
	if services.RecordAudit == nil {
		return
	}
	services.RecordAudit(ctx, event)
}

func conversationMemoryPersistEnabled(conversation domain.ConversationRecord) bool {
	if conversation.Metadata == nil {
		return true
	}
	value, ok := conversation.Metadata["memory_persist_enabled"]
	if !ok || value == nil {
		return true
	}
	typed, ok := value.(bool)
	if ok {
		return typed
	}
	text, ok := value.(string)
	if ok {
		return text != "false"
	}
	return true
}
