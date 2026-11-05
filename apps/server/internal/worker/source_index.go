package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	analysisservice "memstation/apps/server/internal/service/analysis"
	conversationservice "memstation/apps/server/internal/service/conversation"
	"memstation/apps/server/internal/service/processing"

	"github.com/hibiken/asynq"
)

func enqueueDocumentEmbedding(ctx context.Context, s Services, doc domain.MemoryDocument, g domain.MemoryGeneration) error {
	return s.Publisher.Publish(ctx, domain.TaskMessage{Type: TypeEmbeddingGenerate, Queue: QueueDefault, UniqueKey: TypeEmbeddingGenerate + ":" + g.GenerationID + ":" + doc.NodeID + ":" + itoa(g.EmbeddingVersion), Payload: map[string]any{"user_id": doc.UserID, "conversation_id": doc.ConversationID, "generation_id": g.GenerationID, "node_id": doc.NodeID, "node_type": string(doc.Type), "embedding_version": g.EmbeddingVersion, "embedding_model": g.EmbeddingModel}})
}

// Source indexing does not call a generation, scoring or media extraction model.
func indexOriginalSources(ctx context.Context, s Services, c domain.ConversationRecord, g domain.MemoryGeneration) (domain.ConversationRecord, error) {
	if s.Multimodal != nil {
		parts, err := s.Multimodal.MaterializeConversation(ctx, c)
		if err != nil {
			return c, err
		}
		c.Parts = parts
	}
	segments := applyGenerationToSegments(c.UserID, g.GenerationID, conversationservice.BuildSegmentsFromTimeline(c.UserID, c.ConversationID, c.Messages, c.Parts, c.Artifacts, c.UpdatedAt, conversationservice.DefaultSegmentOptions()))
	if err := s.Conversations.ReplaceSegments(ctx, c.UserID, c.ConversationID, segments); err != nil {
		return c, err
	}
	if err := cleanupStaleSegments(ctx, s, c.UserID, g.GenerationID, c.ConversationID, segments); err != nil {
		return c, err
	}
	docs, err := storeSegmentDocuments(ctx, s, segments, domain.MemoryDocument{Metadata: map[string]any{"source_conversation_revision": c.Revision}}, c.UpdatedAt, c.Parts, c.Artifacts, c.Messages)
	if err != nil {
		return c, err
	}
	for _, doc := range docs {
		if err := enqueueDocumentEmbedding(ctx, s, doc, g); err != nil {
			return c, err
		}
	}
	c.Segments = segments
	err = s.Conversations.UpdateConversationState(ctx, domain.ConversationStateUpdate{UserID: c.UserID, ConversationID: c.ConversationID, Metadata: map[string]any{"source_index_status": "stored", "source_index_generation": g.GenerationID, "source_index_revision": c.Revision, "processing_strategy": s.Processing.Config().Strategy, "processing_policy_id": s.Processing.Config().Identity()}})
	return c, err
}

// sourceIndexCurrent reports whether source.index embedded this exact revision. A
// message appended since then rebuilds segments that still need their own vectors.
func sourceIndexCurrent(c domain.ConversationRecord, generationID string) bool {
	return stringValue(c.Metadata, "source_index_generation") == generationID && fmt.Sprint(c.Metadata["source_index_revision"]) == fmt.Sprint(c.Revision)
}

func handleSourceIndex(_ *slog.Logger, s Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		ctx, err := bindTaskScope(ctx, t, s)
		if err != nil {
			return err
		}
		var p SummaryPayload
		if err = json.Unmarshal(t.Payload(), &p); err != nil {
			return err
		}
		c, err := s.Conversations.GetConversation(ctx, p.UserID, p.ConversationID)
		if err != nil {
			return err
		}
		if !conversationMemoryPersistEnabled(c) {
			return nil
		}
		g, err := resolveProcessingGeneration(ctx, s, p.UserID, p.GenerationID)
		if err != nil {
			return err
		}
		c, err = indexOriginalSources(ctx, s, c, g)
		if err != nil {
			markConversationFailed(ctx, s, p.UserID, p.ConversationID, true)
			return err
		}
		promote, reason, err := extractLightweight(ctx, s, c, g, "")
		if err != nil {
			markConversationFailed(ctx, s, p.UserID, p.ConversationID, true)
			return errors.Join(err, s.Conversations.UpdateConversationState(ctx, domain.ConversationStateUpdate{UserID: c.UserID, ConversationID: c.ConversationID, Metadata: map[string]any{"lightweight_status": "failed"}}))
		}
		metadata := map[string]any{"processing_strategy": s.Processing.Config().Strategy, "processing_policy_id": s.Processing.Config().Identity(), "semantic_status": reason}
		metadata["lightweight_status"] = "completed"
		if promote {
			metadata["semantic_status"] = "queued"
			if err = s.Conversations.UpdateConversationState(ctx, domain.ConversationStateUpdate{UserID: c.UserID, ConversationID: c.ConversationID, Metadata: metadata}); err != nil {
				return err
			}
			next := TypeSummaryGenerate
			if s.Multimodal != nil {
				next = TypeMediaExtract
			}
			return s.Publisher.Publish(ctx, domain.TaskMessage{Type: next, Queue: QueueDefault, UniqueKey: next + ":" + c.ConversationID, Payload: map[string]any{"user_id": c.UserID, "conversation_id": c.ConversationID, "generation_id": g.GenerationID, "summary_version": 1, "finalize_reason": p.FinalizeReason, "embedding_model": g.EmbeddingModel, "embedding_version": g.EmbeddingVersion}})
		}
		skipped := domain.ProcessingStatusSkipped
		return s.Conversations.UpdateConversationState(ctx, domain.ConversationStateUpdate{UserID: c.UserID, ConversationID: c.ConversationID, SummaryStatus: &skipped, AnalysisStatus: &skipped, Metadata: metadata})
	}
}

func extractLightweight(ctx context.Context, s Services, c domain.ConversationRecord, g domain.MemoryGeneration, snapshotID string) (bool, string, error) {
	config := s.Processing.Config()
	windows, texts, media := analysisservice.LightweightWindows(c)
	repo, ok := s.Conversations.(ports.ProcessingCheckpointRepository)
	if !ok && snapshotID == "" {
		return false, "", errors.New("processing checkpoints unavailable")
	}
	identity, _ := json.Marshal([]any{config.Identity(), c.UserID, c.WorkspaceID, c.ConversationID, c.Revision, g.GenerationID, windows})
	digest := sha256.Sum256(identity)
	key := hex.EncodeToString(digest[:])
	persist := func(value json.RawMessage) (json.RawMessage, error) {
		if snapshotID != "" {
			if s.Generations == nil {
				return nil, errors.New("snapshot processing store unavailable")
			}
			return s.Generations.SnapshotCheckpoint(ctx, c.UserID, g.GenerationID, snapshotID, key, value)
		}
		return repo.ProcessingCheckpoint(ctx, c.UserID, c.ConversationID, c.Revision, key, value)
	}
	raw, err := persist(nil)
	if err != nil {
		return false, "", err
	}
	var observations []processing.Observation
	if len(raw) == 0 {
		observations, err = s.Processing.Extract(ctx, c.UserID, texts)
		if err != nil {
			return false, "", err
		}
		raw, err = json.Marshal(observations)
		if err != nil {
			return false, "", err
		}
		raw, err = persist(raw)
		if err != nil {
			return false, "", err
		}
	}
	if err = json.Unmarshal(raw, &observations); err != nil || len(observations) != len(windows) {
		return false, "", errors.New("invalid persisted extraction checkpoint")
	}
	seen := map[string]bool{}
	for i, observation := range observations {
		for _, span := range observation.Spans {
			if span.Start < 0 || span.End > len([]rune(texts[i])) || span.End <= span.Start || string([]rune(texts[i])[span.Start:span.End]) != span.Text {
				return false, "", errors.New("ungrounded extraction checkpoint")
			}
			span.Start += windows[i].Start
			span.End += windows[i].Start
			doc, err := analysisservice.LightweightDocument(c.UserID, c.ConversationID, windows[i].Source, span, config.Identity())
			if err != nil {
				return false, "", err
			}
			doc = applyGenerationToDocument(c.UserID, g.GenerationID, doc)
			if seen[doc.ID] {
				continue
			}
			seen[doc.ID] = true
			doc.Metadata["source_conversation_revision"] = c.Revision
			doc.ActivityAt = c.UpdatedAt
			if err = s.Memories.StoreDocument(ctx, doc); err != nil {
				return false, "", err
			}
			if err = linkMentionSources(ctx, s, c, doc, windows[i].Source, span); err != nil {
				return false, "", err
			}
			evidences := groundedNodeEvidences(doc, buildNodeEvidences(doc.UserID, doc.GenerationID, doc.NodeID, doc.ConversationID, c.Parts, c.Artifacts, nil, nil))
			if err = storeDocumentEvidences(ctx, s, doc.UserID, doc.GenerationID, doc.NodeID, doc.ConversationID, evidences); err != nil {
				return false, "", err
			}
			if err = enqueueDocumentEmbedding(ctx, s, doc, g); err != nil {
				return false, "", err
			}
		}
	}
	if err = cleanupStaleMentions(ctx, s, c, g, seen); err != nil {
		return false, "", err
	}
	if media {
		observations = append(observations, processing.Observation{Supported: false, NeedsSemantic: 1})
	}
	promote, reason := s.Processing.Promote(key, observations)
	if promote && config.Strategy == "hybrid" {
		size := 0
		for _, source := range analysisservice.Sources(c) {
			size += len(source.Text)
		}
		if size > config.MaxSemanticBytes {
			return false, "deferred_budget", nil
		}
	}
	return promote, reason, nil
}

// These edges mean a source slice contains the exact mention. They make no
// semantic relationship or actor-equivalence assertion.
func linkMentionSources(ctx context.Context, s Services, c domain.ConversationRecord, doc domain.MemoryDocument, source analysisservice.Source, span processing.Span) error {
	hash := sha256.Sum256([]byte(source.Text))
	runes := []rune(source.Text)
	start, end := len(string(runes[:span.Start]))+source.ByteOffset, len(string(runes[:span.End]))+source.ByteOffset
	for _, segment := range c.Segments {
		raw, err := json.Marshal(segment.Metadata["source_slices"])
		if err != nil {
			return err
		}
		var slices []domain.SegmentSourceSlice
		if err = json.Unmarshal(raw, &slices); err != nil {
			return err
		}
		for _, slice := range slices {
			sequence := source.SequenceNum
			if source.PartID == "" {
				sequence--
			}
			if slice.PartID != source.PartID || slice.SequenceNum != sequence || slice.SourceSHA256 != hex.EncodeToString(hash[:]) || start < slice.ByteStart || end > slice.ByteEnd {
				continue
			}
			err = s.Memories.LinkDocuments(ctx, domain.MemoryRelation{UserID: c.UserID, GenerationID: doc.GenerationID, SrcNodeID: segmentNodeID(segment.ID), DstNodeID: doc.NodeID, RelationType: "mentions_entity", Weight: 1, Source: "lightweight", Metadata: map[string]any{"source_conversation_revision": c.Revision, "processing_policy_id": s.Processing.Config().Identity(), "start_byte": start, "end_byte": end}})
			if err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// cleanupStaleMentions retires mentions from an earlier revision or policy, which
// would otherwise stay searchable and inflate name occurrence counts.
func cleanupStaleMentions(ctx context.Context, s Services, c domain.ConversationRecord, g domain.MemoryGeneration, keep map[string]bool) error {
	docs, err := s.Memories.ListConversationDocumentsInGeneration(ctx, c.UserID, g.GenerationID, c.ConversationID, false)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		if doc.Type != domain.MemoryTypeEntity || doc.Metadata["interpretation"] != "source_mention" || keep[doc.ID] {
			continue
		}
		if err = s.Memories.DeleteDocument(ctx, c.UserID, doc.ID, doc.Type); err != nil {
			return err
		}
		if s.Vectors != nil {
			if err = s.Vectors.DeleteByNode(ctx, domain.VectorDeleteRequest{UserID: c.UserID, NodeID: doc.NodeID, RequireMissingNode: true}); err != nil {
				return err
			}
		}
	}
	return nil
}
