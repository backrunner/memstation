package worker

import (
	"context"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

func segmentNodeID(segmentID string) string {
	return "node:" + segmentID
}

func segmentDocument(segment domain.ConversationSegment) domain.MemoryDocument {
	metadata := cloneMap(segment.Metadata)
	metadata["segment_index"] = segment.SegmentIndex
	metadata["start_sequence"] = segment.StartSequence
	metadata["end_sequence"] = segment.EndSequence
	metadata["message_count"] = segment.MessageCount
	metadata["overlap_from_sequence"] = segment.OverlapFromSequence
	metadata["overlap_message_count"] = segment.OverlapMessageCount
	metadata["content_hash"] = segment.ContentHash
	metadata["source"] = "conversation_segment"

	return domain.MemoryDocument{
		ID:             segment.ID,
		NodeID:         segmentNodeID(segment.ID),
		GenerationID:   segment.GenerationID,
		UserID:         segment.UserID,
		ConversationID: segment.ConversationID,
		Type:           domain.MemoryTypeSegment,
		Content:        segment.Content,
		Metadata:       metadata,
		CreatedAt:      segment.CreatedAt,
		UpdatedAt:      segment.UpdatedAt,
	}
}

func storeSegmentDocuments(ctx context.Context, services Services, segments []domain.ConversationSegment, summaryDoc domain.MemoryDocument, referenceTime time.Time, parts []domain.MessagePart, artifacts []domain.MessagePartArtifact) ([]domain.MemoryDocument, error) {
	if services.Memories == nil || len(segments) == 0 {
		return nil, nil
	}

	stored := make([]domain.MemoryDocument, 0, len(segments))
	for idx, segment := range segments {
		doc := segmentDocument(segment)
		applyDocumentScore(ctx, services.Scorer, &doc, referenceTime)
		if err := services.Memories.StoreDocument(ctx, doc); err != nil {
			return nil, err
		}
		evidences := buildNodeEvidences(doc.UserID, doc.GenerationID, doc.NodeID, doc.ConversationID, parts, artifacts, stringSliceValue(doc.Metadata, "source_part_ids"), stringSliceValue(doc.Metadata, "source_artifact_ids"))
		if err := storeDocumentEvidences(ctx, services, doc.UserID, doc.GenerationID, doc.NodeID, doc.ConversationID, evidences); err != nil {
			return nil, err
		}
		stored = append(stored, doc)

		if summaryDoc.NodeID != "" {
			if err := services.Memories.LinkDocuments(ctx, domain.MemoryRelation{
				GenerationID: doc.GenerationID,
				UserID:       doc.UserID,
				SrcNodeID:    doc.NodeID,
				DstNodeID:    summaryDoc.NodeID,
				RelationType: "summarized_into",
				Weight:       0.92,
				Source:       "summary.generate",
				Metadata: mergeMetadata(extractSourceSnapshotMetadata(doc.Metadata), map[string]any{
					"conversation_id": doc.ConversationID,
					"segment_index":   doc.Metadata["segment_index"],
					"summary_id":      summaryDoc.ID,
				}),
			}); err != nil {
				return nil, err
			}
		}

		if idx == 0 {
			continue
		}
		weight := 0.85
		if segment.OverlapMessageCount > 0 {
			weight = 0.95
		}
		if err := services.Memories.LinkDocuments(ctx, domain.MemoryRelation{
			GenerationID: doc.GenerationID,
			UserID:       doc.UserID,
			SrcNodeID:    doc.NodeID,
			DstNodeID:    stored[idx-1].NodeID,
			RelationType: "follows_segment",
			Weight:       weight,
			Source:       "summary.generate",
			Metadata: mergeMetadata(extractSourceSnapshotMetadata(doc.Metadata), map[string]any{
				"conversation_id":        doc.ConversationID,
				"segment_index":          doc.Metadata["segment_index"],
				"previous_segment_index": stored[idx-1].Metadata["segment_index"],
				"overlap_from_sequence":  doc.Metadata["overlap_from_sequence"],
				"overlap_message_count":  doc.Metadata["overlap_message_count"],
			}),
		}); err != nil {
			return nil, err
		}
	}

	return stored, nil
}

func cleanupStaleSegments(ctx context.Context, services Services, userID, generationID, conversationID string, next []domain.ConversationSegment) error {
	if services.Memories == nil || userID == "" || conversationID == "" {
		return nil
	}

	keepIDs := make([]string, 0, len(next))
	for _, segment := range next {
		keepIDs = append(keepIDs, segment.ID)
	}

	deletedNodeIDs, err := services.Memories.DeleteSegmentsByConversationInGeneration(ctx, userID, generationID, conversationID, keepIDs)
	if err != nil {
		return err
	}
	if services.Vectors == nil {
		return nil
	}
	for _, nodeID := range deletedNodeIDs {
		if err := services.Vectors.DeleteByNode(ctx, userID, nodeID); err != nil {
			return err
		}
	}
	return nil
}

func applyGenerationToSegments(userID, generationID string, segments []domain.ConversationSegment) []domain.ConversationSegment {
	if len(segments) == 0 {
		return nil
	}
	out := make([]domain.ConversationSegment, 0, len(segments))
	for _, segment := range segments {
		segment.GenerationID = generationID
		segment.ID = scopeStableID(userID, generationID, segment.ID)
		if segment.Metadata == nil {
			segment.Metadata = map[string]any{}
		}
		segment.Metadata["generation_id"] = generationID
		out = append(out, segment)
	}
	return out
}

func scopeStableID(userID, generationID, baseID string) string {
	baseID = strings.TrimSpace(baseID)
	generationID = strings.TrimSpace(generationID)
	if baseID == "" || generationID == "" || generationID == currentGenerationID(userID) {
		return baseID
	}
	return generationID + ":" + baseID
}

func extractSourceSnapshotMetadata(metadata map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"source_snapshot_id", "source_snapshot_version", "source_hash", "source_snapshot_at"} {
		if value, ok := metadata[key]; ok && value != nil {
			out[key] = value
		}
	}
	return out
}
