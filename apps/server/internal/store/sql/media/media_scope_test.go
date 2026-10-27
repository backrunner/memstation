package media

import (
	"context"
	sqlpkg "database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	identitystore "memstation/apps/server/internal/store/sql/identity"
	testfixturestore "memstation/apps/server/internal/store/sql/internal/testfixture"

	"github.com/google/uuid"
)

func TestMediaRepositoryRejectsForeignScope(t *testing.T) {
	for _, repo := range []ports.MediaRepository{NewInMemoryMediaRepository(), NewPostgresMediaRepository(nil)} {
		for _, a := range []domain.AccessContext{{UserID: "other", WorkspaceID: domain.PersonalWorkspaceID("other")}, {UserID: "owner", WorkspaceID: "group"}} {
			ctx := security.WithAccess(context.Background(), a)
			calls := []func() error{
				func() error { return repo.PutBlob(ctx, domain.BlobObject{UserID: "owner"}) },
				func() error { _, e := repo.GetBlob(ctx, "owner", "blob"); return e },
				func() error { return repo.DeleteBlob(ctx, "owner", "blob", time.Now()) },
				func() error { _, e := repo.MarkBlobForCleanup(ctx, "owner", "conv", "blob"); return e },
				func() error { return repo.ReplaceMessageParts(ctx, "owner", "conv", nil) },
				func() error { _, e := repo.ListMessageParts(ctx, "owner", "conv"); return e },
				func() error { return repo.ReplacePartArtifacts(ctx, "owner", "conv", nil) },
				func() error { _, e := repo.ListPartArtifacts(ctx, "owner", "conv"); return e },
			}
			for i, call := range calls {
				if e := call(); !errors.Is(e, domain.ErrAccessDenied) {
					t.Errorf("operation %d: %v", i, e)
				}
			}
		}
	}
}

func TestPostgresMediaReferencesAndCleanup(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sqlpkg.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user := "media-scope-" + uuid.NewString()
	workspace := domain.PersonalWorkspaceID(user)
	group := user + "-group"
	ctx := context.Background()
	agentID := testfixturestore.ProvisionFixtureAgent(t, identitystore.NewIdentityRepository(runtimeconfig.NewStaticDBHandle(db)), user)
	repo := NewPostgresMediaRepository(db)
	conversations := conversationstore.NewPostgresConversationRepository(db)
	a, b := user+"-a", user+"-b"
	for _, id := range []string{a, b} {
		if err = conversations.PersistExchange(ctx, domain.ConversationRecord{UserID: user, ConversationID: id, RequestFormat: "openai_chat", AgentID: agentID}); err != nil {
			t.Fatal(err)
		}
	}
	parent, err := conversations.GetConversation(ctx, user, a)
	if err != nil {
		t.Fatal(err)
	}
	hiddenBlob := user + "-hidden-blob"
	hiddenConv := user + "-hidden-conv"
	for _, statement := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO workspaces(id,user_id,name,kind,capabilities) VALUES($1,$2,'Group','group','[]')`, []any{group, user}},
		{`INSERT INTO conversations(id,user_id,workspace_id,agent_id,request_format) VALUES($1,$2,$3,$4,'openai_chat')`, []any{hiddenConv, user, group, parent.AgentID}},
		{`INSERT INTO blob_objects(blob_id,user_id,workspace_id,source_kind,storage_backend,storage_key) VALUES($1,$2,$3,'upload','local','hidden')`, []any{hiddenBlob, user, group}},
	} {
		if _, err = db.ExecContext(ctx, statement.q, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	blob := domain.BlobObject{BlobID: user + "-blob", UserID: user, SourceKind: domain.BlobSourceKindUpload, StorageBackend: "local", StorageKey: "fixture"}
	if err = repo.PutBlob(ctx, blob); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetBlob(ctx, user, hiddenBlob); err == nil {
		t.Fatal("hidden blob readable")
	}
	forged := blob
	forged.BlobID = hiddenBlob
	forged.StorageKey = "hidden"
	if err = repo.PutBlob(ctx, forged); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("blob owner rebound", err)
	}
	part := domain.MessagePart{PartID: user + "-part-a", UserID: user, WorkspaceID: workspace, ConversationID: a, Role: domain.RoleUser, PartType: domain.ContentBlockType("file"), BlobID: blob.BlobID}
	if err = repo.ReplaceMessageParts(ctx, user, a, []domain.MessagePart{part}); err != nil {
		t.Fatal(err)
	}
	foreign := part
	foreign.BlobID = hiddenBlob
	if err = repo.ReplaceMessageParts(ctx, user, a, []domain.MessagePart{foreign}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("foreign blob reference accepted", err)
	}
	if err = repo.ReplaceMessageParts(ctx, user, hiddenConv, nil); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("hidden conversation replace accepted", err)
	}
	parts, err := repo.ListMessageParts(ctx, user, a)
	if err != nil || len(parts) != 1 || parts[0].BlobID != blob.BlobID || parts[0].WorkspaceID != workspace {
		t.Fatal("failed replacement did not roll back", parts, err)
	}
	artifact := domain.MessagePartArtifact{ArtifactID: user + "-artifact", PartID: part.PartID, ArtifactType: "text", Status: "completed", TextContent: "derived"}
	if err = repo.ReplacePartArtifacts(ctx, user, a, []domain.MessagePartArtifact{artifact}); err != nil {
		t.Fatal(err)
	}
	invalid := artifact
	invalid.PartID = "missing"
	if err = repo.ReplacePartArtifacts(ctx, user, a, []domain.MessagePartArtifact{invalid}); err == nil {
		t.Fatal("orphan artifact accepted")
	}
	arts, err := repo.ListPartArtifacts(ctx, user, a)
	if err != nil || len(arts) != 1 || arts[0].PartID != part.PartID {
		t.Fatal("artifact replacement did not roll back", err)
	}
	shared := part
	shared.PartID = user + "-part-b"
	shared.ConversationID = b
	if err = repo.ReplaceMessageParts(ctx, user, b, []domain.MessagePart{shared}); err != nil {
		t.Fatal(err)
	}
	if marked, e := repo.MarkBlobForCleanup(ctx, user, a, blob.BlobID); e != nil || marked {
		t.Fatal("shared live blob removed", e)
	}
	if err = repo.ReplaceMessageParts(ctx, user, b, nil); err != nil {
		t.Fatal(err)
	}
	snapshot := user + "-snapshot"
	if _, err = db.ExecContext(ctx, `INSERT INTO memory_source_snapshots(snapshot_id,user_id,workspace_id,conversation_id,snapshot_version,source_hash,request_format,message_parts) VALUES($1,$2,$3,$4,1,'fixture','openai_chat',jsonb_build_array(jsonb_build_object('blob_id',$5::text)))`, snapshot, user, workspace, a, blob.BlobID); err != nil {
		t.Fatal(err)
	}
	if marked, e := repo.MarkBlobForCleanup(ctx, user, a, blob.BlobID); e != nil || marked {
		t.Fatal("retained blob removed", e)
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM memory_source_snapshots WHERE snapshot_id=$1`, snapshot); err != nil {
		t.Fatal(err)
	}
	if marked, e := repo.MarkBlobForCleanup(ctx, user, a, blob.BlobID); e != nil || !marked {
		t.Fatal("unused blob not cleaned", e)
	}
	got, err := repo.GetBlob(ctx, user, blob.BlobID)
	if err != nil || got.DeletedAt == nil {
		t.Fatal("cleanup tombstone missing", err)
	}
	if err = repo.ReplaceMessageParts(ctx, user, b, []domain.MessagePart{shared}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("new reference revived deleted blob", err)
	}
	if err = repo.PutBlob(ctx, blob); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("upsert revived deleted blob", err)
	}
}
