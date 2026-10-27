package memory

import (
	"context"
	sqlpkg "database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
	recordutilstore "memstation/apps/server/internal/store/sql/internal/recordutil"

	"github.com/google/uuid"
)

func TestMemoryRepositoryRejectsUnboundOwnersAndWorkspaces(t *testing.T) {
	for name, repo := range map[string]ports.MemoryRepository{"memory": NewInMemoryMemoryRepository(), "postgres": NewPostgresMemoryRepository(nil)} {
		t.Run(name, func(t *testing.T) {
			for _, scope := range []domain.AccessContext{
				{UserID: "other", WorkspaceID: domain.PersonalWorkspaceID("other")},
				{UserID: "owner", WorkspaceID: "group"},
			} {
				ctx := security.WithAccess(context.Background(), scope)
				operations := map[string]func() error{
					"search":       func() error { _, e := repo.Search(ctx, domain.SearchInput{UserID: "owner"}); return e },
					"history":      func() error { _, e := repo.SearchFactsAt(ctx, domain.SearchInput{UserID: "owner"}); return e },
					"store":        func() error { return repo.StoreDocument(ctx, domain.MemoryDocument{UserID: "owner"}) },
					"get":          func() error { _, e := repo.GetDocument(ctx, "owner", "id", domain.MemoryTypeFact); return e },
					"list":         func() error { _, e := repo.ListDocuments(ctx, "owner", nil, 10); return e },
					"nodes":        func() error { _, e := repo.ListDocumentsByNodeIDs(ctx, "owner", []string{"node"}); return e },
					"conversation": func() error { _, e := repo.ListDocumentsByConversation(ctx, "owner", "conv"); return e },
					"keywords":     func() error { _, e := repo.SearchKeywordMatches(ctx, "owner", "gen", "q", 10); return e },
					"update": func() error {
						_, e := repo.UpdateDocument(ctx, "owner", "id", domain.MemoryTypeFact, "changed")
						return e
					},
					"delete":           func() error { return repo.DeleteDocument(ctx, "owner", "id", domain.MemoryTypeFact) },
					"delete_segments":  func() error { _, e := repo.DeleteSegmentsByConversation(ctx, "owner", "conv", nil); return e },
					"lineage":          func() error { _, e := repo.ListFactLineage(ctx, "owner", "gen", "fact"); return e },
					"lineage_key":      func() error { _, e := repo.ListFactLineageByKey(ctx, "owner", "gen", "key"); return e },
					"key_get":          func() error { _, _, e := repo.GetKeyMemory(ctx, "owner", "key"); return e },
					"key_id":           func() error { _, _, e := repo.GetKeyMemoryByID(ctx, "owner", "id"); return e },
					"key_count":        func() error { _, e := repo.CountKeyMemories(ctx, "owner"); return e },
					"key_list":         func() error { _, e := repo.ListKeyMemories(ctx, "owner", 10); return e },
					"key_search":       func() error { _, e := repo.SearchKeyMemories(ctx, domain.KeyMemoryQuery{UserID: "owner"}); return e },
					"key_write":        func() error { return repo.UpsertKeyMemory(ctx, domain.KeyMemory{UserID: "owner"}) },
					"key_delete":       func() error { return repo.DeleteKeyMemory(ctx, "owner", "key") },
					"link":             func() error { return repo.LinkDocuments(ctx, domain.MemoryRelation{UserID: "owner"}) },
					"unlink":           func() error { return repo.DeleteRelationsByTarget(ctx, "owner", "node", "related_to") },
					"relations":        func() error { _, e := repo.ListRelations(ctx, "owner", "gen", []string{"node"}, 1); return e },
					"graph":            func() error { _, e := repo.RecallRelations(ctx, domain.RelationQuery{UserID: "owner"}); return e },
					"evidence":         func() error { _, e := repo.ListEvidences(ctx, "owner", "node"); return e },
					"evidence_nodes":   func() error { _, e := repo.ListEvidencesByNodeIDs(ctx, "owner", []string{"node"}); return e },
					"evidence_replace": func() error { return repo.ReplaceEvidences(ctx, "owner", "node", nil) },
					"evidence_merge":   func() error { return repo.MergeEvidences(ctx, "owner", "node", nil) },
					"aliases":          func() error { _, e := repo.ListEntityAliases(ctx, "owner", "gen"); return e },
					"alias_write":      func() error { _, e := repo.PutEntityAlias(ctx, domain.EntityAlias{UserID: "owner"}); return e },
				}
				for operation, run := range operations {
					if err := run(); !errors.Is(err, domain.ErrAccessDenied) {
						t.Errorf("%s with %+v: %v", operation, scope, err)
					}
				}
			}
		})
	}
}

func TestPersonalMemoryIsSharedByAuthorizedAgents(t *testing.T) {
	repo := NewInMemoryMemoryRepository()
	workspace := domain.PersonalWorkspaceID("owner")
	a := security.WithAccess(context.Background(), domain.AccessContext{UserID: "owner", WorkspaceID: workspace, AgentID: "a", ScopeVersion: 2, PartitionIDs: []string{""}})
	b := security.WithAccess(context.Background(), domain.AccessContext{UserID: "owner", WorkspaceID: workspace, AgentID: "b", ScopeVersion: 2, PartitionIDs: []string{""}})
	doc := domain.MemoryDocument{UserID: "owner", ID: "fact", Type: domain.MemoryTypeFact, Content: "Shared fact"}
	if err := repo.StoreDocument(a, doc); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetDocument(b, "owner", "fact", domain.MemoryTypeFact)
	if err != nil || got.WorkspaceID != workspace || got.Content != doc.Content {
		t.Fatalf("shared read: %+v %v", got, err)
	}
	doc.WorkspaceID = "group"
	if err = repo.StoreDocument(a, doc); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("explicit scope ignored: %v", err)
	}
	// Separator-bearing owner/record IDs must remain distinct in fallback maps.
	for _, d := range []domain.MemoryDocument{{UserID: "a", ID: "b::c", Type: domain.MemoryTypeTask, Content: "first"}, {UserID: "a::b", ID: "c", Type: domain.MemoryTypeTask, Content: "second"}} {
		if err = repo.StoreDocument(context.Background(), d); err != nil {
			t.Fatal(err)
		}
	}
	first, err := repo.GetDocument(context.Background(), "a", "b::c", domain.MemoryTypeTask)
	if err != nil || first.Content != "first" {
		t.Fatalf("composite identity collision: %+v %v", first, err)
	}
}

func TestInMemoryWritesDoNotTouchForeignWorkspace(t *testing.T) {
	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	foreign := domain.MemoryDocument{ID: "foreign", NodeID: "foreign-node", UserID: "owner", WorkspaceID: "group", GenerationID: recordutilstore.CurrentGenerationID(domain.PersonalWorkspaceID("owner")), Type: domain.MemoryTypeFact, Content: "private", Metadata: map[string]any{"fact_key": "home", "lineage_group_id": "home", "revision_seq": 90}}
	repo.documents[recordutilstore.Key("owner", foreign.ID)] = foreign
	repo.keyMemories[recordutilstore.Key(security.ScopedIdentity(ctx, "owner"), "secret")] = domain.KeyMemory{ID: "foreign-key", UserID: "owner", WorkspaceID: "group", Key: "secret", Value: "preserve"}
	if err := repo.UpsertKeyMemory(ctx, domain.KeyMemory{UserID: "owner", Key: "secret", Value: "overwrite"}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("key collision was accepted", err)
	}
	if err := repo.DeleteKeyMemory(ctx, "owner", "secret"); err != nil {
		t.Fatal(err)
	}
	if repo.keyMemories[recordutilstore.Key(security.ScopedIdentity(ctx, "owner"), "secret")].Value != "preserve" {
		t.Fatal("foreign key was deleted")
	}
	if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: "visible", UserID: "owner", Type: domain.MemoryTypeFact, Content: "public", Metadata: map[string]any{"fact_key": "home", "lineage_group_id": "home"}}); err != nil {
		t.Fatal(err)
	}
	got := repo.documents[recordutilstore.Key("owner", "foreign")]
	if got.Metadata["is_superseded"] == true {
		t.Fatal("foreign lineage head was superseded")
	}
	visible := repo.documents[recordutilstore.Key("owner", "visible")]
	if factLineageRevisionSeqFromMetadata(visible.Metadata) != 1 {
		t.Fatal("foreign revision affected visible sequence")
	}
	if err := repo.ReplaceEvidences(ctx, "owner", foreign.NodeID, nil); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("foreign evidence write accepted", err)
	}
	if err := repo.LinkDocuments(ctx, domain.MemoryRelation{UserID: "owner", SrcNodeID: visible.NodeID, DstNodeID: foreign.NodeID, RelationType: "related_to"}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("foreign relation accepted", err)
	}
}

func TestPostgresMemoryQueriesExcludeOtherWorkspaceBeforeLimit(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sqlpkg.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner := "memory-scope-" + uuid.NewString()
	workspace := domain.PersonalWorkspaceID(owner)
	ctx := security.WithAccess(context.Background(), domain.AccessContext{UserID: owner, WorkspaceID: workspace, ScopeVersion: 2, PartitionIDs: []string{""}})
	repo := NewPostgresMemoryRepository(db)
	provisionFixtureGeneration(t, repo, ctx, owner, "")
	visible := domain.MemoryDocument{UserID: owner, ID: owner + "-visible", NodeID: owner + "-visible-node", Type: domain.MemoryTypeFact, Content: "scope token visible", Metadata: map[string]any{"fact_key": "shared-key"}}
	if err = repo.StoreDocument(ctx, visible); err != nil {
		t.Fatal(err)
	}
	if err = repo.UpsertKeyMemory(ctx, domain.KeyMemory{UserID: owner, Key: "visible", Value: "scope token visible"}); err != nil {
		t.Fatal(err)
	}
	group, hidden, hiddenNode, hiddenKeyNode := owner+"-group", owner+"-hidden", owner+"-hidden-node", owner+"-hidden-key-node"
	generation := recordutilstore.CurrentGenerationID(domain.PersonalWorkspaceID(owner))
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspaces(id,user_id,kind,name,capabilities) VALUES ($1,$2,'group','Hidden','[]')`, []any{group, owner}},
		{`INSERT INTO memory_nodes(id,user_id,workspace_id,generation_id,node_type,entity_id,importance) VALUES ($1,$2,$3,$4,'fact',$5,10),($6,$2,$3,'','key_memory',$7,10)`, []any{hiddenNode, owner, group, generation, hidden, hiddenKeyNode, hidden + "-key"}},
		{`INSERT INTO facts(id,user_id,generation_id,node_id,conversation_id,content,fact_key,lineage_group_id,revision_seq) VALUES ($1,$2,$3,$4,'hidden-conversation','scope token hidden','shared-key',$5,20)`, []any{hidden, owner, generation, hiddenNode, factLineageGroupID(owner, domain.MemoryDocument{UserID: owner, Metadata: map[string]any{"fact_key": "shared-key"}})}},
		{`INSERT INTO key_memories(id,user_id,node_id,key,value,workspace_id) VALUES ($1,$2,$3,'hidden','scope token hidden',$4)`, []any{hidden + "-key", owner, hiddenKeyNode, group}},
		{`INSERT INTO memory_relations(id,user_id,generation_id,src_node_id,dst_node_id,relation_type) VALUES ($1,$2,$3,$4,$5,'related_to')`, []any{owner + "-edge", owner, generation, visible.NodeID, hiddenNode}},
	} {
		if _, err = db.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	assertVisible := func(name string, docs []domain.MemoryDocument, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, d := range docs {
			if d.ID == hidden || d.ID == hidden+"-key" || d.WorkspaceID != workspace {
				t.Fatalf("%s leaked scope: %+v", name, d)
			}
		}
	}
	docs, err := repo.ListDocuments(ctx, owner, nil, 1)
	assertVisible("limited list", docs, err)
	if len(docs) != 1 {
		t.Fatal("other-scope candidates displaced visible results")
	}
	docs, err = repo.ListDocumentsByNodeIDs(ctx, owner, []string{hiddenNode, visible.NodeID})
	assertVisible("node reads", docs, err)
	if len(docs) != 1 || docs[0].ID != visible.ID {
		t.Fatal("visible node missing")
	}
	docs, err = repo.ListDocumentsByConversation(ctx, owner, "hidden-conversation")
	assertVisible("conversation reads", docs, err)
	result, err := repo.Search(ctx, domain.SearchInput{UserID: owner, Query: "scope token", TopN: 10})
	assertVisible("search", result.Memories, err)
	now := time.Now().UTC()
	docs, err = repo.SearchFactsAt(ctx, domain.SearchInput{UserID: owner, GenerationID: generation, KnownAt: &now})
	assertVisible("history", docs, err)
	if len(docs) != 1 || docs[0].ID != visible.ID {
		t.Fatal("visible history missing")
	}
	if _, err = repo.GetDocument(ctx, owner, hidden, domain.MemoryTypeFact); err == nil {
		t.Fatal("direct ID exposed another workspace")
	}
	keywords, err := repo.SearchKeywordMatches(ctx, owner, generation, "shared-key", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keywords {
		if k.NodeID == hiddenNode {
			t.Fatal("keyword leak")
		}
	}
	if len(keywords) != 1 || keywords[0].NodeID != visible.NodeID {
		t.Fatal("visible keyword match missing")
	}
	for _, graph := range []bool{false, true} {
		var edges []domain.MemoryRelation
		if graph {
			edges, err = repo.RecallRelations(ctx, domain.RelationQuery{UserID: owner, GenerationID: generation, NodeIDs: []string{visible.NodeID}})
		} else {
			edges, err = repo.ListRelations(ctx, owner, generation, []string{visible.NodeID}, 2)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			if e.DstNodeID == hiddenNode {
				t.Fatal("graph crossed workspace")
			}
		}
	}
	if _, ok, e := repo.GetKeyMemory(ctx, owner, "hidden"); e != nil || ok {
		t.Fatalf("hidden key read: %v %v", ok, e)
	}
	if n, e := repo.CountKeyMemories(ctx, owner); e != nil || n != 1 {
		t.Fatalf("hidden key counted: %d %v", n, e)
	}
	if err = repo.UpsertKeyMemory(ctx, domain.KeyMemory{UserID: owner, Key: "hidden", Value: "personal copy"}); err != nil {
		t.Fatal(err)
	}
	var otherValue string
	if err = db.QueryRowContext(ctx, `SELECT value FROM key_memories WHERE user_id=$1 AND node_id=$2`, owner, hiddenKeyNode).Scan(&otherValue); err != nil || otherValue != "scope token hidden" {
		t.Fatalf("same key changed another workspace: %q %v", otherValue, err)
	}
	for _, nodeID := range []string{hiddenNode, owner + "-fresh-node"} {
		if err = repo.StoreDocument(ctx, domain.MemoryDocument{UserID: owner, ID: hidden, NodeID: nodeID, Type: domain.MemoryTypeFact, Content: "overwrite"}); !errors.Is(err, domain.ErrAccessDenied) {
			t.Fatalf("hidden identity was rebound: %v", err)
		}
	}
	if err = repo.LinkDocuments(ctx, domain.MemoryRelation{UserID: owner, SrcNodeID: visible.NodeID, DstNodeID: hiddenNode, RelationType: "related_to"}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("cross-workspace edge accepted", err)
	}
	if _, err = repo.UpdateDocument(ctx, owner, hidden, domain.MemoryTypeFact, "overwrite"); err == nil {
		t.Fatal("update crossed scope")
	}
	if err = repo.ReplaceEvidences(ctx, owner, hiddenNode, nil); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("evidence write crossed scope: %v", err)
	}
	for _, method := range []func(context.Context, string, string, []domain.MemoryEvidence) error{repo.ReplaceEvidences, repo.MergeEvidences} {
		if err = method(ctx, owner, visible.NodeID, []domain.MemoryEvidence{{UserID: "other", NodeID: visible.NodeID}}); !errors.Is(err, domain.ErrAccessDenied) {
			t.Fatal("forged evidence owner accepted")
		}
	}
	if err = repo.DeleteDocument(ctx, owner, hidden, domain.MemoryTypeFact); err != nil {
		t.Fatal(err)
	}
	var content string
	if err = db.QueryRowContext(ctx, `SELECT content FROM facts WHERE id=$1`, hidden).Scan(&content); err != nil || content != "scope token hidden" {
		t.Fatalf("hidden fact changed: %s %v", content, err)
	}
	// A normal fact update must not supersede an identically keyed hidden fact.
	visible.ID += "-next"
	visible.NodeID += "-next"
	if err = repo.StoreDocument(ctx, visible); err != nil {
		t.Fatal(err)
	}
	var superseded bool
	if err = db.QueryRowContext(ctx, `SELECT is_superseded FROM facts WHERE id=$1`, hidden).Scan(&superseded); err != nil || superseded {
		t.Fatalf("fact lineage crossed scope: %v %v", superseded, err)
	}
}
