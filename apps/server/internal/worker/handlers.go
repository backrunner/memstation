package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hibiken/asynq"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	analysisservice "memstation/apps/server/internal/service/analysis"
	conversationservice "memstation/apps/server/internal/service/conversation"
	memoryservice "memstation/apps/server/internal/service/memory"
)

type Services struct {
	Conversations        ports.ConversationRepository
	Memories             ports.MemoryRepository
	Vectors              ports.VectorStore
	Publisher            ports.TaskPublisher
	Analysis             *analysisservice.Service
	Scorer               *memoryservice.Scorer
	EmbeddingModel       string
	AllowedKeyMemoryKeys map[string]struct{}
	RecordAudit          func(context.Context, domain.AuditEvent)
	InvalidateKeyMemory  func(context.Context, string)
}

func NewServeMux(logger *slog.Logger, services Services) *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeConversationFinalize, handleConversationFinalize(logger, services))
	mux.HandleFunc(TypeSummaryGenerate, handleSummaryGenerate(logger, services))
	mux.HandleFunc(TypeEmbeddingGenerate, handleEmbeddingGenerate(logger, services))
	mux.HandleFunc(TypeAnalysisExtract, handleAnalysisExtract(logger, services))
	mux.HandleFunc(TypeKeyMemoryReconcile, handleKeyMemoryReconcile(logger, services))
	mux.HandleFunc(TypeEmbeddingRebuild, handleEmbeddingRebuild(logger, services))
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
		if !conversationMemoryPersistEnabled(conversation) {
			skipped := domain.ProcessingStatusSkipped
			return services.Conversations.UpdateConversationState(ctx, domain.ConversationStateUpdate{
				ConversationID: conversationID,
				UserID:         userID,
				FinalizeStatus: &finalizeStatus,
				SummaryStatus:  &skipped,
				AnalysisStatus: &skipped,
				Metadata: map[string]any{
					"finalize_reason":        finalizeReason,
					"finalized_at":           time.Now().UTC().Format(time.RFC3339),
					"memory_persist_enabled": false,
				},
			})
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
			Metadata: map[string]any{
				"finalize_reason": finalizeReason,
				"finalized_at":    time.Now().UTC().Format(time.RFC3339),
			},
		}); err != nil {
			return err
		}
		if conversation.SummaryStatus == domain.ProcessingStatusCompleted || conversation.SummaryStatus == domain.ProcessingStatusRunning || conversation.SummaryStatus == domain.ProcessingStatusQueued {
			return nil
		}
		return services.Publisher.Publish(ctx, domain.TaskMessage{
			Type:      TypeSummaryGenerate,
			Queue:     QueueDefault,
			UniqueKey: TypeSummaryGenerate + ":" + conversationID,
			Payload: map[string]any{
				"conversation_id": conversationID,
				"user_id":         userID,
				"summary_version": 1,
				"finalize_reason": finalizeReason,
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
		segments := conversationservice.BuildSegments(payload.UserID, payload.ConversationID, conversation.Messages, conversation.UpdatedAt, conversationservice.DefaultSegmentOptions())
		if err := services.Conversations.ReplaceSegments(ctx, payload.UserID, payload.ConversationID, segments); err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, true)
			return err
		}
		if err := cleanupStaleSegments(ctx, services, payload.UserID, payload.ConversationID, segments); err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, true)
			return err
		}
		conversation.Segments = segments
		summaryText := buildSummary(conversation.Messages)
		if summaryText == "" {
			summaryText = conversationservice.SummarizeSegments(segments)
		}
		if summaryText == "" {
			summaryText = "No summary content available."
		}
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
			},
		}
		applyDocumentScore(ctx, services.Scorer, &doc, conversation.UpdatedAt)
		logger.InfoContext(ctx, "summary stored", "conversation_id", payload.ConversationID)
		if err := services.Memories.StoreDocument(ctx, doc); err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, true)
			return err
		}
		segmentDocs, err := storeSegmentDocuments(ctx, services, segments, doc, conversation.UpdatedAt)
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
				"summary_document_id": doc.ID,
				"segment_count":       len(segments),
				"segment_strategy":    "dialogue_window_v1",
			},
		}); err != nil {
			return err
		}
		if err := services.Publisher.Publish(ctx, domain.TaskMessage{
			Type:      TypeEmbeddingGenerate,
			Queue:     QueueDefault,
			UniqueKey: TypeEmbeddingGenerate + ":" + doc.NodeID + ":1",
			Payload: map[string]any{
				"user_id":           payload.UserID,
				"node_id":           doc.NodeID,
				"node_type":         string(doc.Type),
				"embedding_version": 1,
				"content":           embeddingContentForDocument(doc),
			},
		}); err != nil {
			return err
		}
		for _, segmentDoc := range segmentDocs {
			if err := services.Publisher.Publish(ctx, domain.TaskMessage{
				Type:      TypeEmbeddingGenerate,
				Queue:     QueueDefault,
				UniqueKey: TypeEmbeddingGenerate + ":" + segmentDoc.NodeID + ":1",
				Payload: map[string]any{
					"user_id":           payload.UserID,
					"node_id":           segmentDoc.NodeID,
					"node_type":         string(segmentDoc.Type),
					"embedding_version": 1,
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
				"user_id":         payload.UserID,
				"conversation_id": payload.ConversationID,
				"summary_ids":     []string{doc.ID},
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
		content := payload.Content
		if content == "" {
			content = payload.NodeID
		}
		return services.Vectors.Upsert(ctx, domain.VectorUpsertRequest{
			UserID:           payload.UserID,
			NodeID:           payload.NodeID,
			NodeType:         payload.NodeType,
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
		docs, err := services.Memories.ListDocumentsByConversation(ctx, payload.UserID, payload.ConversationID)
		if err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, false)
			return err
		}
		var summaryDoc domain.MemoryDocument
		if len(docs) > 0 {
			summaryDoc = docs[0]
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
		})
		if err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, false)
			return err
		}
		applyDocumentScore(ctx, services.Scorer, &result.Fact, conversation.UpdatedAt)
		applyDocumentScore(ctx, services.Scorer, &result.Impression, conversation.UpdatedAt)
		if err := services.Memories.StoreDocument(ctx, result.Fact); err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, false)
			return err
		}
		if err := services.Memories.StoreDocument(ctx, result.Impression); err != nil {
			markConversationFailed(ctx, services, payload.UserID, payload.ConversationID, false)
			return err
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
			},
		}); err != nil {
			return err
		}
		followups := []domain.TaskMessage{
			{
				Type:      TypeEmbeddingGenerate,
				Queue:     QueueDefault,
				UniqueKey: TypeEmbeddingGenerate + ":" + result.Fact.NodeID + ":1",
				Payload: map[string]any{
					"user_id":           payload.UserID,
					"node_id":           result.Fact.NodeID,
					"node_type":         string(result.Fact.Type),
					"embedding_version": 1,
					"content":           result.Fact.Content,
				},
			},
			{
				Type:      TypeEmbeddingGenerate,
				Queue:     QueueDefault,
				UniqueKey: TypeEmbeddingGenerate + ":" + result.Impression.NodeID + ":1",
				Payload: map[string]any{
					"user_id":           payload.UserID,
					"node_id":           result.Impression.NodeID,
					"node_type":         string(result.Impression.Type),
					"embedding_version": 1,
					"content":           result.Impression.Content,
				},
			},
			{
				Type:      TypeKeyMemoryReconcile,
				Queue:     QueueLow,
				UniqueKey: TypeKeyMemoryReconcile + ":" + payload.ConversationID,
				Payload: map[string]any{
					"user_id":         payload.UserID,
					"conversation_id": payload.ConversationID,
				},
			},
		}
		for _, followup := range followups {
			if err := services.Publisher.Publish(ctx, followup); err != nil {
				return err
			}
		}
		return nil
	}
}

func handleKeyMemoryReconcile(logger *slog.Logger, services Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		payload := map[string]any{}
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		userID, _ := payload["user_id"].(string)
		conversationID, _ := payload["conversation_id"].(string)
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
				"node_id": keyMemoryNodeID(userID, "recent_conversation"),
			},
		}
		logger.InfoContext(ctx, "key memory reconciled", "conversation_id", conversationID)
		if err := services.Memories.UpsertKeyMemory(ctx, keyMemory); err != nil {
			return err
		}
		if err := syncConversationKeyMemoryRelations(ctx, services.Memories, keyMemory, conversationID); err != nil {
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

func syncConversationKeyMemoryRelations(ctx context.Context, repo ports.MemoryRepository, km domain.KeyMemory, conversationID string) error {
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
	docs, err := repo.ListDocumentsByConversation(ctx, km.UserID, conversationID)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		if doc.NodeID == "" || doc.NodeID == nodeID {
			continue
		}
		if err := repo.LinkDocuments(ctx, domain.MemoryRelation{
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

func handleEmbeddingRebuild(logger *slog.Logger, services Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		var payload EmbeddingRebuildPayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		if payload.EmbeddingVersion <= 0 {
			payload.EmbeddingVersion = 1
		}

		logger.InfoContext(ctx, "embedding rebuild task received", "user_id", payload.UserID, "embedding_version", payload.EmbeddingVersion)

		if payload.UserID != "" && services.Memories != nil && services.Publisher != nil {
			docs, err := services.Memories.ListDocuments(ctx, payload.UserID, []domain.MemoryType{
				domain.MemoryTypeSummary,
				domain.MemoryTypeSegment,
				domain.MemoryTypeImpression,
				domain.MemoryTypeFact,
				domain.MemoryTypeKeyMemory,
			}, 1000)
			if err != nil {
				return err
			}
			seen := map[string]struct{}{}
			for _, doc := range docs {
				if doc.NodeID == "" {
					continue
				}
				if _, ok := seen[doc.NodeID]; ok {
					continue
				}
				seen[doc.NodeID] = struct{}{}
				if err := services.Publisher.Publish(ctx, domain.TaskMessage{
					Type:      TypeEmbeddingGenerate,
					Queue:     QueueBulk,
					UniqueKey: TypeEmbeddingGenerate + ":" + doc.NodeID + ":" + itoa(payload.EmbeddingVersion),
					Payload: map[string]any{
						"user_id":           doc.UserID,
						"node_id":           doc.NodeID,
						"node_type":         string(doc.Type),
						"embedding_version": payload.EmbeddingVersion,
						"content":           embeddingContentForDocument(doc),
					},
				}); err != nil {
					return err
				}
			}
		}

		if services.Vectors != nil {
			return services.Vectors.ReindexVersion(ctx, payload.EmbeddingVersion)
		}
		return nil
	}
}

func buildSummary(messages []domain.UnifiedMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.Role == domain.RoleAssistant || message.Role == domain.RoleUser {
			text := message.Text
			if text == "" && len(message.Blocks) > 0 {
				text = message.Blocks[0].Text
			}
			if text != "" {
				parts = append(parts, string(message.Role)+": "+text)
			}
		}
	}
	return truncate(strings.Join(parts, " | "), 400)
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
