package sql

import (
	"context"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
)

func TestInMemoryMemoryRepositoryDeleteSegmentsByConversationUsesKeepSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	now := time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC)

	keepDoc := domain.MemoryDocument{
		ID:             "segment:conv-keep:000",
		NodeID:         "node:segment:conv-keep:000",
		UserID:         "user-1",
		ConversationID: "conv-keep",
		Type:           domain.MemoryTypeSegment,
		Content:        "user: keep this segment",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	staleDoc := domain.MemoryDocument{
		ID:             "segment:conv-keep:001",
		NodeID:         "node:segment:conv-keep:001",
		UserID:         "user-1",
		ConversationID: "conv-keep",
		Type:           domain.MemoryTypeSegment,
		Content:        "user: remove this segment",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	for _, doc := range []domain.MemoryDocument{keepDoc, staleDoc} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store doc %s: %v", doc.ID, err)
		}
	}
	if err := repo.LinkDocuments(ctx, domain.MemoryRelation{
		UserID:       "user-1",
		SrcNodeID:    staleDoc.NodeID,
		DstNodeID:    "node:summary:conv-keep:v1",
		RelationType: "summarized_into",
		Weight:       1,
		Source:       "test",
	}); err != nil {
		t.Fatalf("seed stale relation: %v", err)
	}

	deletedNodeIDs, err := repo.DeleteSegmentsByConversation(ctx, "user-1", "conv-keep", []string{keepDoc.ID})
	if err != nil {
		t.Fatalf("delete stale segments: %v", err)
	}
	if len(deletedNodeIDs) != 1 || deletedNodeIDs[0] != staleDoc.NodeID {
		t.Fatalf("expected stale node %s to be deleted, got %+v", staleDoc.NodeID, deletedNodeIDs)
	}
	if _, err := repo.GetDocument(ctx, "user-1", keepDoc.ID, domain.MemoryTypeSegment); err != nil {
		t.Fatalf("expected keep doc to remain: %v", err)
	}
	if _, err := repo.GetDocument(ctx, "user-1", staleDoc.ID, domain.MemoryTypeSegment); err == nil {
		t.Fatalf("expected stale doc to be removed")
	}
	relations, err := repo.ListRelations(ctx, "user-1", []string{staleDoc.NodeID}, 1)
	if err != nil {
		t.Fatalf("list stale relations: %v", err)
	}
	if len(relations) != 0 {
		t.Fatalf("expected stale relations to be removed, got %+v", relations)
	}
}

func TestSegmentEntityPrefixPatternEscapesLikeWildcards(t *testing.T) {
	t.Parallel()

	got := segmentEntityPrefixPattern(`conv_%\demo`)
	want := `segment:conv\_\%\\demo:%`
	if got != want {
		t.Fatalf("expected escaped LIKE prefix %q, got %q", want, got)
	}
}
