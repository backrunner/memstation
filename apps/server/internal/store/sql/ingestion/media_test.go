package ingestion_test

import (
	"encoding/json"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	ingestfixturestore "memstation/apps/server/internal/store/sql/internal/ingestfixture"
)

func TestEventMediaSurvivesPersistenceAndWorkerRead(t *testing.T) {
	db, repo, ctx, batch := ingestfixturestore.IngestionFixture(t)
	batch.Events[0].Parts = append(batch.Events[0].Parts, domain.SourcePart{Type: "media", Media: &domain.ContentBlock{Type: domain.ContentBlockImage, SourceURL: "https://example.com/photo.png", MIMEType: "image/png"}})
	result, err := repo.Observe(ctx, batch, "media-event", 8192)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err = db.QueryRow(`SELECT content_blocks FROM messages WHERE conversation_id=$1`, result.Events[0].ConversationID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var blocks []domain.ContentBlock
	if err = json.Unmarshal(raw, &blocks); err != nil || len(blocks) != 2 || blocks[1].Type != domain.ContentBlockImage {
		t.Fatal(blocks, err)
	}
	access, _ := security.AccessFromContext(ctx)
	conversation, err := conversationstore.NewPostgresConversationRepository(db).GetConversation(ctx, access.UserID, result.Events[0].ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation.Messages) != 1 || conversation.Messages[0].Source == nil || conversation.Messages[0].Source.Parts[1].Media == nil || len(conversation.Messages[0].Blocks) != 2 || conversation.Messages[0].Blocks[1].SourceURL != "https://example.com/photo.png" {
		t.Fatal("worker lost original attachment", conversation)
	}
}
