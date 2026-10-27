package conversation

import (
	"context"
	"errors"
	"testing"

	ingestfixturestore "memstation/apps/server/internal/store/sql/internal/ingestfixture"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	testfixturestore "memstation/apps/server/internal/store/sql/internal/testfixture"
)

func TestSourceWindowMemoryBoundaries(t *testing.T) {
	ctx := context.Background()
	repo := NewInMemoryConversationRepository()
	c := domain.ConversationRecord{UserID: "owner", ConversationID: "conversation", Messages: []domain.UnifiedMessage{
		{Role: domain.RoleUser, Text: "before", Metadata: map[string]any{"speaker": "Alice", "ref": "D1:1"}},
		{Role: domain.RoleUser, Text: "I did not attend."},
		{Role: domain.RoleAssistant, Text: "after"},
		{Role: domain.RoleUser, Text: "outside window"},
	}, AgentID: "fixture-agent"}
	if err := repo.PersistExchange(ctx, c); err != nil {
		t.Fatal(err)
	}
	in := domain.SourceWindowInput{UserID: "owner", ConversationID: "conversation", ConversationRevision: 1, Sequences: []int{1}, Radius: 1}
	got, err := repo.ReadSourceWindow(ctx, in)
	if err != nil || len(got) != 3 || got[0].Speaker != "Alice" || got[0].ExternalRef != "D1:1" || got[1].Text != "I did not attend." {
		t.Fatalf("window: %+v %v", got, err)
	}
	in.MaxBytes = 6
	got, err = repo.ReadSourceWindow(ctx, in)
	if err != nil || len(got) != 1 || got[0].Text != "before" {
		t.Fatalf("whole-record budget: %+v %v", got, err)
	}
	in.UserID = "other"
	if got, err = repo.ReadSourceWindow(ctx, in); err == nil || len(got) != 0 {
		t.Fatal("cross-owner read", got, err)
	}
	in.UserID = "owner"
	ctx = security.WithAccess(ctx, domain.AccessContext{UserID: "other", WorkspaceID: domain.PersonalWorkspaceID("other"), ScopeVersion: 2, PartitionIDs: []string{""}})
	if _, err = repo.ReadSourceWindow(ctx, in); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("foreign context", err)
	}
}

func TestSourceWindowPostgresEventsRevisionAndAccess(t *testing.T) {
	db, ingest, ctx, b := ingestfixturestore.IngestionFixture(t)
	second := b.Events[0]
	second.EventID = "event2"
	second.SourceMessageID = "message2"
	second.Speaker.ExternalID = "bob"
	second.Parts = []domain.SourcePart{{Type: "text", Text: "I did not attend."}}
	b.Events = append(b.Events, second)
	if _, err := ingest.Observe(ctx, b, "sources", 8192); err != nil {
		t.Fatal(err)
	}
	var conversation, source string
	var seq int
	if err := db.QueryRow(`SELECT w.conversation_id,w.source_message_id,w.sequence_num FROM ingestion_window_sources w WHERE w.workspace_id=$1 ORDER BY w.sequence_num LIMIT 1`, b.WorkspaceID).Scan(&conversation, &source, &seq); err != nil {
		t.Fatal(err)
	}
	owner := security.UserIDFromContext(ctx)
	in := domain.SourceWindowInput{UserID: owner, WorkspaceID: b.WorkspaceID, ConversationID: conversation, Refs: []domain.SourceRef{{WorkspaceID: b.WorkspaceID, MessageID: source, Revision: 1, PartIndex: 0}}, Radius: 1}
	reader := NewPostgresConversationRepository(db)
	got, err := reader.ReadSourceWindow(ctx, in)
	if err != nil || len(got) != 2 || got[0].Sequence != seq || len(got[0].Refs) != 1 || len(got[0].Parts) != 1 || got[0].Speaker == got[1].Speaker || got[1].Text != "I did not attend." {
		t.Fatalf("source window: %+v %v", got, err)
	}
	restricted := security.WithAccess(ctx, domain.AccessContext{UserID: owner, WorkspaceID: b.WorkspaceID, ScopeVersion: 2, PartitionIDs: []string{"other"}, Capabilities: domain.AllCapabilities()})
	if got, err = reader.ReadSourceWindow(restricted, in); err != nil || len(got) != 0 {
		t.Fatalf("partition: %+v %v", got, err)
	}
	in.Refs[0].Revision = 2
	if got, err = reader.ReadSourceWindow(ctx, in); err != nil || len(got) != 0 {
		t.Fatalf("revision: %+v %v", got, err)
	}
	in.Refs[0].Revision = 1
	in.Refs[0].PartIndex = 8
	if got, err = reader.ReadSourceWindow(ctx, in); err != nil || len(got) != 0 {
		t.Fatalf("part: %+v %v", got, err)
	}
	in.Refs[0].PartIndex = 0
	if _, err = db.Exec(`UPDATE source_messages SET tombstoned=true WHERE id=$1`, source); err != nil {
		t.Fatal(err)
	}
	if got, err = reader.ReadSourceWindow(ctx, in); err != nil || len(got) != 0 {
		t.Fatalf("tombstoned anchor: %+v %v", got, err)
	}
	in.Refs = nil
	in.Sequences = []int{seq}
	if got, err = reader.ReadSourceWindow(ctx, in); err != nil || len(got) != 0 {
		t.Fatalf("events cannot fall back to sequence: %+v %v", got, err)
	}
}

func TestSourceWindowPostgresVersionedConversation(t *testing.T) {
	db, _, ctx, b := ingestfixturestore.IngestionFixture(t)
	owner := security.UserIDFromContext(ctx)
	repo := NewPostgresConversationRepository(db)
	c := domain.ConversationRecord{UserID: owner, WorkspaceID: b.WorkspaceID, ConversationID: "source-conversation", Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "not completed", Metadata: map[string]any{"speaker": "Alice", "ref": "D1:1"}}, {Role: domain.RoleAssistant, Text: "planned"}}, AgentID: testfixturestore.FixtureAgentID(owner)}
	if err := repo.PersistExchange(ctx, c); err != nil {
		t.Fatal(err)
	}
	in := domain.SourceWindowInput{UserID: owner, WorkspaceID: b.WorkspaceID, ConversationID: c.ConversationID, ConversationRevision: 1, Sequences: []int{0}, Radius: 1}
	got, err := repo.ReadSourceWindow(ctx, in)
	if err != nil || len(got) != 2 || got[0].Speaker != "Alice" || got[0].ExternalRef != "D1:1" || got[1].Role != domain.RoleAssistant {
		t.Fatalf("conversation: %+v %v", got, err)
	}
	in.ConversationRevision = 2
	if got, err = repo.ReadSourceWindow(ctx, in); err != nil || len(got) != 0 {
		t.Fatalf("wrong conversation revision: %+v %v", got, err)
	}
	in.ConversationRevision = 0
	if got, err = repo.ReadSourceWindow(ctx, in); err != nil || len(got) != 0 {
		t.Fatalf("unversioned conversation source: %+v %v", got, err)
	}
	in.ConversationRevision = 1
	if _, err = db.Exec(`UPDATE conversations SET finalize_status='invalidated' WHERE id=$1`, c.ConversationID); err != nil {
		t.Fatal(err)
	}
	if got, err = repo.ReadSourceWindow(ctx, in); err != nil || len(got) != 0 {
		t.Fatalf("invalidated: %+v %v", got, err)
	}
}

func TestSourceWindowPreservesQuotedAuthor(t *testing.T) {
	db, ingest, ctx, b := ingestfixturestore.IngestionFixture(t)
	first, err := ingest.Observe(ctx, b, "original", 8192)
	if err != nil {
		t.Fatal(err)
	}
	original, err := ingest.Source(ctx, first.Events[0].MessageID)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.SourceRef{WorkspaceID: b.WorkspaceID, MessageID: original.ID, Revision: 1, PartIndex: 0}
	event := b.Events[0]
	event.EventID = "quote"
	event.SourceMessageID = "quote"
	event.Speaker.ExternalID = "bob"
	event.Parts = []domain.SourcePart{{Type: "quote", Text: "我在上海工作。", Source: &ref, Author: &domain.SourceSpeaker{Kind: "human", ExternalID: "alice"}}}
	b.Events = []domain.SourceEvent{event}
	receipt, err := ingest.Observe(ctx, b, "quoted", 8192)
	if err != nil {
		t.Fatal(err)
	}
	var conversation string
	if err = db.QueryRow(`SELECT conversation_id FROM ingestion_window_sources WHERE source_message_id=$1`, receipt.Events[0].MessageID).Scan(&conversation); err != nil {
		t.Fatal(err)
	}
	turns, err := NewPostgresConversationRepository(db).ReadSourceWindow(ctx, domain.SourceWindowInput{UserID: security.UserIDFromContext(ctx), WorkspaceID: b.WorkspaceID, ConversationID: conversation, Refs: []domain.SourceRef{{WorkspaceID: b.WorkspaceID, MessageID: receipt.Events[0].MessageID, Revision: 1, PartIndex: 0}}, Radius: 0})
	if err != nil || len(turns) != 1 || len(turns[0].Parts) != 1 || turns[0].Parts[0].Author == nil || turns[0].Speaker == original.SpeakerActorID || turns[0].Parts[0].Author.ActorID != original.SpeakerActorID || turns[0].Parts[0].Source.MessageID != original.ID {
		t.Fatalf("quote attribution: %+v %v", turns, err)
	}
}
