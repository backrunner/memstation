package sql

import (
	"context"
	sqlpkg "database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"

	"github.com/google/uuid"
)

func TestAtomicConversationFinalization(t *testing.T) {
	finalizationSuite(t, NewInMemoryConversationRepository(), NewInMemoryOutboxRepository(), nil)
}

func TestPostgresAtomicConversationFinalization(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sqlpkg.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	finalizationSuite(t, NewPostgresConversationRepository(db), NewPostgresOutboxRepository(db), db)
}

func finalizationSuite(t *testing.T, repo ports.ConversationRepository, outbox ports.OutboxRepository, db *sqlpkg.DB) {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewString()
	user := "finalize-" + id
	c := domain.ConversationRecord{UserID: user, ConversationID: id, RequestFormat: "openai_chat", Mode: domain.ModeTransparent, FinalizeStatus: domain.FinalizeStatusActive, Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "Keep this history"}}, Usage: domain.UsageRecord{InputTokens: 1, TotalTokens: 1}, Metadata: map[string]any{"workspace_id": "forged", "agent_id": "forged"}}
	if err := repo.PersistExchange(ctx, c); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.GetConversation(ctx, user, id)
	if err != nil {
		t.Fatal(err)
	}
	if saved.WorkspaceID != domain.PersonalWorkspaceID(user) || saved.AgentID != domain.LegacyAgentID(user) || saved.Metadata["workspace_id"] != saved.WorkspaceID {
		t.Fatal("untrusted metadata became identity")
	}
	input := domain.ConversationFinalization{UserID: user, ConversationID: id, WorkspaceID: saved.WorkspaceID, Status: domain.FinalizeStatusEnded, Reason: "explicit"}
	cutoff := saved.UpdatedAt.Add(-time.Second)
	staleTimeout := input
	staleTimeout.Status = domain.FinalizeStatusTimeout
	staleTimeout.Reason = "timeout"
	staleTimeout.TimeoutBefore = &cutoff
	if err = repo.RequestFinalization(ctx, staleTimeout, outbox); err != nil {
		t.Fatal(err)
	}
	active, err := repo.GetConversation(ctx, user, id)
	if err != nil || active.FinalizeStatus != domain.FinalizeStatusActive {
		t.Fatalf("stale timeout ended a recently updated conversation: %+v %v", active, err)
	}
	wrong := input
	wrong.WorkspaceID = "group-other"
	if err = repo.RequestFinalization(ctx, wrong, outbox); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("cross-workspace finalization: %v", err)
	}
	other := security.WithAccess(ctx, domain.AccessContext{UserID: user, WorkspaceID: saved.WorkspaceID, AgentID: "other-agent"})
	if err = repo.RequestFinalization(other, input, outbox); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("cross-agent finalization: %v", err)
	}
	// A failing outbox cannot leave a conversation sealed without a durable job.
	if db == nil {
		if err = repo.RequestFinalization(ctx, input, nil); err == nil {
			t.Fatal("missing atomic outbox accepted")
		}
	} else {
		constraint := "test_outbox_" + uuid.NewString()[:8]
		if _, err = db.ExecContext(ctx, `ALTER TABLE outbox_events ADD CONSTRAINT `+constraint+` CHECK (user_id <> '`+user+`') NOT VALID`); err != nil {
			t.Fatal(err)
		}
		err = repo.RequestFinalization(ctx, input, outbox)
		_, dropErr := db.ExecContext(ctx, `ALTER TABLE outbox_events DROP CONSTRAINT `+constraint)
		if dropErr != nil {
			t.Fatal(dropErr)
		}
		if err == nil {
			t.Fatal("outbox insert did not fail")
		}
	}
	before, err := repo.GetConversation(ctx, user, id)
	if err != nil || before.FinalizeStatus != domain.FinalizeStatusActive {
		t.Fatalf("failed transaction changed conversation: %+v %v", before, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for i := range 16 {
		wg.Go(func() {
			next := input
			if i%2 == 0 {
				next.Reason = "timeout"
				next.Status = domain.FinalizeStatusTimeout
			}
			results <- repo.RequestFinalization(ctx, next, outbox)
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	events, err := outbox.Pending(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var event domain.OutboxEvent
	for _, item := range events {
		if item.AggregateID == id {
			count++
			event = item
		}
	}
	if count != 1 || event.Payload["workspace_id"] != saved.WorkspaceID || event.Payload["agent_id"] != saved.AgentID {
		t.Fatalf("invalid finalization outbox: %d %+v", count, event)
	}
	if err = outbox.MarkProcessed(ctx, event.ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.RequestFinalization(ctx, input, outbox); err != nil {
		t.Fatal(err)
	}
	// A gateway snapshot prepared before the seal cannot restore active state or
	// erase the winning finalization reason; worker updates cannot erase messages.
	if err = repo.PersistExchange(ctx, c); err != nil {
		t.Fatal(err)
	}
	completed := domain.ProcessingStatusCompleted
	if err = repo.UpdateConversationState(ctx, domain.ConversationStateUpdate{UserID: user, ConversationID: id, SummaryStatus: &completed, Metadata: map[string]any{"workspace_id": "forged"}}); err != nil {
		t.Fatal(err)
	}
	after, err := repo.GetConversation(ctx, user, id)
	if err != nil || after.FinalizeStatus != domain.FinalizeStatusEnding || after.Metadata["finalize_reason"] != event.Payload["finalize_reason"] || len(after.Messages) != 1 || after.WorkspaceID != after.Metadata["workspace_id"] {
		t.Fatalf("late write reset lifecycle/scope: %+v %v", after, err)
	}
	events, err = outbox.Pending(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.AggregateID == id {
			t.Fatal("duplicate request requeued a processed event")
		}
	}
	// No-retention conversations finish without an outbox job.
	c.ConversationID = uuid.NewString()
	c.Metadata = map[string]any{"memory_persist_enabled": false, "source_retain_enabled": false}
	if err = repo.PersistExchange(ctx, c); err != nil {
		t.Fatal(err)
	}
	input.ConversationID = c.ConversationID
	if err = repo.RequestFinalization(ctx, input, outbox); err != nil {
		t.Fatal(err)
	}
	ended, err := repo.GetConversation(ctx, user, c.ConversationID)
	if err != nil || ended.FinalizeStatus != domain.FinalizeStatusEnded || ended.SummaryStatus != domain.ProcessingStatusSkipped {
		t.Fatalf("no-retention finalization: %+v %v", ended, err)
	}
	assertPersonalConversationQueries(t, repo, db, saved)
}

func assertPersonalConversationQueries(t *testing.T, repo ports.ConversationRepository, db *sqlpkg.DB, personal domain.ConversationRecord) {
	t.Helper()
	ctx := security.WithAccess(context.Background(), domain.AccessContext{UserID: personal.UserID, WorkspaceID: personal.WorkspaceID, AgentID: personal.AgentID})
	group := personal
	group.ConversationID = "group-conversation-" + personal.ConversationID
	group.WorkspaceID = "group-" + personal.ConversationID
	group.FinalizeStatus = domain.FinalizeStatusActive
	group.UpdatedAt = time.Now().Add(-time.Hour)
	if db == nil {
		memory := repo.(*InMemoryConversationRepository)
		memory.conversations[key(group.UserID, group.ConversationID)] = group
	} else {
		if _, err := db.ExecContext(ctx, `INSERT INTO workspaces (id,user_id,kind,name,capabilities) VALUES ($1,$2,'group','Other scope','[]')`, group.WorkspaceID, group.UserID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO conversations (id,user_id,workspace_id,agent_id,request_format,updated_at) VALUES ($1,$2,$3,$4,'openai_chat',$5)`, group.ConversationID, group.UserID, group.WorkspaceID, group.AgentID, group.UpdatedAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.GetConversation(ctx, group.UserID, group.ConversationID); !errors.Is(err, domain.ErrConversationNotFound) {
		t.Fatalf("direct ID bypassed Personal scope: %v", err)
	}
	listed, err := repo.ListConversations(ctx, group.UserID)
	if err != nil {
		t.Fatal(err)
	}
	timedOut, err := repo.ListTimedOutConversations(context.Background(), time.Now(), 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range append(listed, timedOut...) {
		if c.ConversationID == group.ConversationID {
			t.Fatal("unsupported workspace leaked through a list or timeout query")
		}
	}
	if err := repo.UpdateConversationState(ctx, domain.ConversationStateUpdate{UserID: group.UserID, ConversationID: group.ConversationID}); !errors.Is(err, domain.ErrConversationNotFound) {
		t.Fatalf("state write bypassed Personal scope: %v", err)
	}
}
