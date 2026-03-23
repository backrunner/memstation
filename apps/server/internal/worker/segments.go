package worker

import (
	"context"
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
		UserID:         segment.UserID,
		ConversationID: segment.ConversationID,
		Type:           domain.MemoryTypeSegment,
		Content:        segment.Content,
		Metadata:       metadata,
		CreatedAt:      segment.CreatedAt,
		UpdatedAt:      segment.UpdatedAt,
	}
}

func storeSegmentDocuments(ctx context.Context, services Services, segments []domain.ConversationSegment, summaryDoc domain.MemoryDocument, referenceTime time.Time) ([]domain.MemoryDocument, error) {
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
		stored = append(stored, doc)

		if summaryDoc.NodeID != "" {
			if err := services.Memories.LinkDocuments(ctx, domain.MemoryRelation{
				UserID:       doc.UserID,
				SrcNodeID:    doc.NodeID,
				DstNodeID:    summaryDoc.NodeID,
				RelationType: "summarized_into",
				Weight:       0.92,
				Source:       "summary.generate",
				Metadata: map[string]any{
					"conversation_id": doc.ConversationID,
					"segment_index":   doc.Metadata["segment_index"],
					"summary_id":      summaryDoc.ID,
				},
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
			UserID:       doc.UserID,
			SrcNodeID:    doc.NodeID,
			DstNodeID:    stored[idx-1].NodeID,
			RelationType: "follows_segment",
			Weight:       weight,
			Source:       "summary.generate",
			Metadata: map[string]any{
				"conversation_id":        doc.ConversationID,
				"segment_index":          doc.Metadata["segment_index"],
				"previous_segment_index": stored[idx-1].Metadata["segment_index"],
				"overlap_from_sequence":  doc.Metadata["overlap_from_sequence"],
				"overlap_message_count":  doc.Metadata["overlap_message_count"],
			},
		}); err != nil {
			return nil, err
		}
	}

	return stored, nil
}

func cleanupStaleSegments(ctx context.Context, services Services, userID, conversationID string, next []domain.ConversationSegment) error {
	if services.Memories == nil || userID == "" || conversationID == "" {
		return nil
	}

	keepIDs := make([]string, 0, len(next))
	for _, segment := range next {
		keepIDs = append(keepIDs, segment.ID)
	}

	deletedNodeIDs, err := services.Memories.DeleteSegmentsByConversation(ctx, userID, conversationID, keepIDs)
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
