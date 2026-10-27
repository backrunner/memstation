package memory

import (
	"testing"

	ingestfixturestore "memstation/apps/server/internal/store/sql/internal/ingestfixture"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

func TestEpisodeSourceEditWithdrawsOldRevision(t *testing.T) {
	db, ingest, ctx, batch := ingestfixturestore.IngestionFixture(t)
	receipt, err := ingest.Observe(ctx, batch, "episode-original", 8192)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.SourceRef{WorkspaceID: batch.WorkspaceID, MessageID: receipt.Events[0].MessageID, Revision: 1, PartIndex: 0}
	repo := NewPostgresMemoryRepository(db)
	doc := domain.MemoryDocument{ID: "episode-old", NodeID: "node-episode-old", UserID: security.UserIDFromContext(ctx), WorkspaceID: batch.WorkspaceID, ConversationID: receipt.Events[0].ConversationID, Type: domain.MemoryTypeEpisode, Content: "The source described working in Shanghai.", SourceRefs: []domain.SourceRef{ref}}
	if err = repo.StoreDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	docs, err := repo.ListDocuments(ctx, doc.UserID, []domain.MemoryType{domain.MemoryTypeEpisode}, 20)
	if err != nil || len(docs) != 1 {
		t.Fatal("episode was not visible before source edit", len(docs), err)
	}
	batch.Events[0].EventID = "edited-event"
	batch.Events[0].Type = "message.edited"
	batch.Events[0].SourceRevision = 2
	batch.Events[0].Parts = []domain.SourcePart{{Type: "text", Text: "我在北京工作，先前的上海信息有误。"}}
	if _, err = ingest.Observe(ctx, batch, "episode-edited", 8192); err != nil {
		t.Fatal(err)
	}
	docs, err = repo.ListDocuments(ctx, doc.UserID, []domain.MemoryType{domain.MemoryTypeEpisode}, 20)
	if err != nil || len(docs) != 0 {
		t.Fatal("old episode survived source revision", len(docs), err)
	}
	if err = repo.StoreDocument(ctx, doc); err == nil {
		t.Fatal("stale task revived episode from obsolete source")
	}
}
