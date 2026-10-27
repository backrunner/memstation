package memory

import (
	"context"
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestRelationGenerationIsResolvedFromBothEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name, requested, src, dst, want string
		wantError                       bool
	}{
		{name: "same generation", src: "g1", dst: "g1", want: "g1"},
		{name: "key source", dst: "g1", want: "g1"},
		{name: "key destination", src: "g1", want: "g1"},
		{name: "conflicting endpoints", src: "g1", dst: "g2", wantError: true},
		{name: "explicit mismatch", requested: "g2", src: "g1", dst: "g1", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewInMemoryMemoryRepository()
			ctx := context.Background()
			for i, generation := range []string{tc.src, tc.dst} {
				id := []string{"src", "dst"}[i]
				kind := domain.MemoryTypeSummary
				if generation == "" {
					kind = domain.MemoryTypeKeyMemory
				}
				if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: id, NodeID: id, UserID: "u", GenerationID: generation, Type: kind, Content: id}); err != nil {
					t.Fatal(err)
				}
			}
			err := repo.LinkDocuments(ctx, domain.MemoryRelation{UserID: "u", GenerationID: tc.requested, SrcNodeID: "src", DstNodeID: "dst", RelationType: "related_to", Weight: 1})
			if tc.wantError {
				if err == nil {
					t.Fatal("expected generation conflict")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			relations, err := repo.ListRelations(ctx, "u", tc.want, []string{"src"}, 1)
			if err != nil {
				t.Fatal(err)
			}
			var found bool
			for _, relation := range relations {
				if relation.DstNodeID == "dst" {
					found = true
					if relation.GenerationID != tc.want {
						t.Fatalf("generation = %q, want %q", relation.GenerationID, tc.want)
					}
				}
			}
			if !found {
				t.Fatal("expected scoped edge")
			}
		})
	}
}

func TestConflictingRelationIsHiddenFromScopedRecall(t *testing.T) {
	repo := NewInMemoryMemoryRepository()
	ctx := context.Background()
	for id, generation := range map[string]string{"src": "g1", "dst": "g2"} {
		if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: id, NodeID: id, UserID: "u", GenerationID: generation, Type: domain.MemoryTypeFact, Content: id}); err != nil {
			t.Fatal(err)
		}
	}
	repo.relations = append(repo.relations, domain.MemoryRelation{ID: "conflicting", WorkspaceID: domain.PersonalWorkspaceID("u"), UserID: "u", GenerationID: "g1", SrcNodeID: "src", DstNodeID: "dst", RelationType: "related_to", Weight: 1})
	relations, err := repo.ListRelations(ctx, "u", "g1", []string{"src"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, relation := range relations {
		if relation.ID == "conflicting" {
			t.Fatal("conflicting edge crossed generation boundary")
		}
	}
	all, err := repo.ListRelations(ctx, "u", "", []string{"src"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, relation := range all {
		if relation.ID == "conflicting" {
			return
		}
	}
	t.Fatal("conflicting relation must remain inspectable")
}
