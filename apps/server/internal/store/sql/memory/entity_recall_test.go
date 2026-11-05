package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	ingestfixture "memstation/apps/server/internal/store/sql/internal/ingestfixture"
)

func TestPostgresEntityIndexBoundsMatchesAndAppliesLiveScope(t *testing.T) {
	db, _, ctx, b := ingestfixture.IngestionFixture(t)
	repo := NewPostgresMemoryRepository(db)
	user := security.UserIDFromContext(ctx)
	generation := "gen:current:" + b.WorkspaceID
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("entity-%02d", i)
		if err := repo.StoreDocument(ctx, domain.MemoryDocument{UserID: user, ID: id, NodeID: id, GenerationID: generation, Type: domain.MemoryTypeEntity, Content: "同名项目", Metadata: map[string]any{"canonical_name": "MemStation", "entity_scope": "work"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO memory_partitions(id,user_id,workspace_id,agent_id,audience_actor_ids) SELECT 'private',$1,$2,a.id,jsonb_build_array(a.actor_id) FROM agents a WHERE a.user_id=$1 ORDER BY a.id LIMIT 1`, user, b.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	accessPrivate, _ := security.AccessFromContext(ctx)
	accessPrivate.PartitionIDs = []string{"private"}
	private := security.WithAccess(context.Background(), accessPrivate)
	if err := repo.StoreDocument(private, domain.MemoryDocument{UserID: user, ID: "private-entity", NodeID: "private-entity", GenerationID: generation, Type: domain.MemoryTypeEntity, Content: "private", Metadata: map[string]any{"canonical_name": "MemStation", "entity_scope": "work"}}); err != nil {
		t.Fatal(err)
	}
	matches, err := repo.MatchEntityNames(ctx, user, generation, "work", []string{"MemStation", "memstation", "missing' OR TRUE --"})
	if err != nil || len(matches) != 20 || matches[0].Occurrences != 20 {
		t.Fatalf("bounded scoped index: %+v %v", matches, err)
	}
	for _, scope := range []string{"personal", "other"} {
		out, err := repo.MatchEntityNames(ctx, user, generation, scope, []string{"MemStation"})
		if err != nil || len(out) != 0 {
			t.Fatal("scope leaked", out, err)
		}
	}
	if out, err := repo.MatchEntityNames(ctx, user, generation+"-other", "work", []string{"MemStation"}); err != nil || len(out) != 0 {
		t.Fatal("generation leaked", out, err)
	}
	if _, err := repo.MatchEntityNames(ctx, "another-user", generation, "work", []string{"MemStation"}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("foreign user accepted", err)
	}
	if err := repo.DeleteDocument(ctx, user, matches[0].NodeID, domain.MemoryTypeEntity); err != nil {
		t.Fatal(err)
	}
	matches, err = repo.MatchEntityNames(ctx, user, generation, "work", []string{"MemStation"})
	if err != nil || len(matches) != 19 || matches[0].Occurrences != 19 {
		t.Fatal("deleted mention still counted", matches, err)
	}
	access, _ := security.AccessFromContext(ctx)
	access.WorkspaceID = domain.PersonalWorkspaceID(user)
	other := security.WithAccess(context.Background(), access)
	if out, err := repo.MatchEntityNames(other, user, generation, "work", []string{"MemStation"}); err != nil || len(out) != 0 {
		t.Fatal("workspace leaked", out, err)
	}
}

func TestScopedEntityRecallKeepsSourceMentions(t *testing.T) {
	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	generation := "gen:current:" + domain.PersonalWorkspaceID("u")
	for id, metadata := range map[string]map[string]any{
		"mention":     {"canonical_name": "Alice", "interpretation": "source_mention"},
		"other-scope": {"canonical_name": "Alice", "entity_scope": "home"},
		"same-scope":  {"canonical_name": "Alice", "entity_scope": "work"},
	} {
		if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: id, NodeID: id, UserID: "u", GenerationID: generation, Type: domain.MemoryTypeEntity, Content: "Alice", Metadata: metadata}); err != nil {
			t.Fatal(err)
		}
	}
	matches, err := repo.MatchEntityNames(ctx, "u", generation, "work", []string{"Alice"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, match := range matches {
		got[match.NodeID] = true
	}
	if !got["mention"] || !got["same-scope"] || got["other-scope"] {
		t.Fatalf("scoped matches: %+v", matches)
	}
}
