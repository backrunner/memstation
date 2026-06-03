package memory

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/cache"
	"memstation/apps/server/internal/domain"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	sqlstore "memstation/apps/server/internal/store/sql"
	vectorstore "memstation/apps/server/internal/store/vector"
)

func TestServiceInvalidatesKeyMemoryListCacheByPrefix(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := cache.NewJSONStore(ctx, nil, time.Minute)
	if err != nil {
		t.Fatalf("new cache store: %v", err)
	}
	defer func() { _ = store.Close(context.Background()) }()

	repo := sqlstore.NewInMemoryMemoryRepository()
	service := NewService(nil, repo)
	service.SetKeyMemoryCache(store)

	if _, err := service.UpsertKeyMemory(ctx, domain.KeyMemory{
		UserID: "user-1",
		Key:    "recent_conversation",
		Value:  "conv-1",
	}); err != nil {
		t.Fatalf("upsert first key memory: %v", err)
	}

	initial, err := service.ListKeyMemories(ctx, "user-1", 1)
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(initial) != 1 {
		t.Fatalf("expected 1 key memory, got %d", len(initial))
	}

	full, err := service.ListKeyMemories(ctx, "user-1", 10)
	if err != nil {
		t.Fatalf("list cached page: %v", err)
	}
	if len(full) != 1 {
		t.Fatalf("expected 1 cached key memory, got %d", len(full))
	}

	if err := repo.UpsertKeyMemory(ctx, domain.KeyMemory{
		UserID: "user-1",
		Key:    "project_focus",
		Value:  "cache hardening",
	}); err != nil {
		t.Fatalf("repo bypass insert: %v", err)
	}

	stale, err := service.ListKeyMemories(ctx, "user-1", 10)
	if err != nil {
		t.Fatalf("list stale cache: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("expected stale cached length 1, got %d", len(stale))
	}

	if _, err := service.UpsertKeyMemory(ctx, domain.KeyMemory{
		UserID: "user-1",
		Key:    "system_state",
		Value:  "ready",
	}); err != nil {
		t.Fatalf("service upsert after stale cache: %v", err)
	}

	refreshed, err := service.ListKeyMemories(ctx, "user-1", 10)
	if err != nil {
		t.Fatalf("list refreshed cache: %v", err)
	}
	if len(refreshed) != 3 {
		t.Fatalf("expected refreshed length 3, got %d", len(refreshed))
	}
}

func TestServiceUpsertKeyMemoryCreatesSearchableEmbedding(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()
	vectors := vectorstore.NewPGVectorStore(nil)
	engine := NewEngine(repo, vectors)
	service := NewService(engine, repo)
	service.SetVectorStore(vectors)

	if _, err := service.UpsertKeyMemory(ctx, domain.KeyMemory{
		UserID: "user-1",
		Key:    "preferred_editor",
		Value:  "neovim",
	}); err != nil {
		t.Fatalf("upsert key memory: %v", err)
	}

	result, err := engine.Search(ctx, domain.SearchInput{
		UserID:      "user-1",
		Query:       "preferred_editor: neovim",
		TopN:        3,
		MemoryTypes: []domain.MemoryType{domain.MemoryTypeKeyMemory},
	})
	if err != nil {
		t.Fatalf("search key memory: %v", err)
	}
	if len(result.Memories) == 0 {
		t.Fatal("expected key memory search result")
	}
	if result.Memories[0].Type != domain.MemoryTypeKeyMemory {
		t.Fatalf("expected key memory type, got %s", result.Memories[0].Type)
	}
	if result.Memories[0].Metadata["key"] != "preferred_editor" {
		t.Fatalf("expected metadata key preferred_editor, got %v", result.Memories[0].Metadata["key"])
	}
}

func TestServiceUpsertKeyMemoryReusesExistingIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()
	vectors := vectorstore.NewPGVectorStore(nil)
	engine := NewEngine(repo, vectors)
	service := NewService(engine, repo)
	service.SetVectorStore(vectors)

	created, err := service.UpsertKeyMemory(ctx, domain.KeyMemory{
		UserID: "user-1",
		Key:    "preferred_editor",
		Value:  "neovim",
	})
	if err != nil {
		t.Fatalf("create key memory: %v", err)
	}
	createdNodeID := stringMetadataValue(created.Metadata, "node_id")

	updated, err := service.UpsertKeyMemory(ctx, domain.KeyMemory{
		UserID: "user-1",
		Key:    "preferred_editor",
		Value:  "zed",
	})
	if err != nil {
		t.Fatalf("update key memory: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("expected update to reuse id %s, got %s", created.ID, updated.ID)
	}
	if got := stringMetadataValue(updated.Metadata, "node_id"); got != createdNodeID {
		t.Fatalf("expected update to reuse node id %s, got %s", createdNodeID, got)
	}

	memories, err := repo.ListKeyMemories(ctx, "user-1", 10)
	if err != nil {
		t.Fatalf("list key memories: %v", err)
	}
	if len(memories) != 1 || memories[0].Value != "zed" {
		t.Fatalf("expected single updated key memory, got %+v", memories)
	}
	docs, err := repo.ListDocuments(ctx, "user-1", []domain.MemoryType{domain.MemoryTypeKeyMemory}, 10)
	if err != nil {
		t.Fatalf("list key-memory documents: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != created.ID || docs[0].Content != "preferred_editor: zed" {
		t.Fatalf("expected one updated key-memory document, got %+v", docs)
	}
	result, err := engine.Search(ctx, domain.SearchInput{
		UserID:      "user-1",
		Query:       "preferred_editor zed",
		TopN:        5,
		MemoryTypes: []domain.MemoryType{domain.MemoryTypeKeyMemory},
	})
	if err != nil {
		t.Fatalf("search updated key memory: %v", err)
	}
	if len(result.Memories) != 1 || result.Memories[0].ID != created.ID || !strings.Contains(result.Memories[0].Content, "zed") {
		t.Fatalf("expected only updated key memory in search, got %+v", result.Memories)
	}

	if err := service.DeleteKeyMemory(ctx, "user-1", "preferred_editor"); err != nil {
		t.Fatalf("delete updated key memory: %v", err)
	}
	deletedResult, err := engine.Search(ctx, domain.SearchInput{
		UserID:      "user-1",
		Query:       "preferred_editor zed",
		TopN:        5,
		MemoryTypes: []domain.MemoryType{domain.MemoryTypeKeyMemory},
	})
	if err != nil {
		t.Fatalf("search deleted key memory: %v", err)
	}
	if len(deletedResult.Memories) != 0 {
		t.Fatalf("expected deleted key memory vector/document to be gone, got %+v", deletedResult.Memories)
	}
}

func TestServiceUpsertKeyMemoryFindsExistingPastListLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()
	vectors := vectorstore.NewPGVectorStore(nil)
	engine := NewEngine(repo, vectors)
	service := NewService(engine, repo)
	service.SetVectorStore(vectors)

	targetCreatedAt := time.Now().UTC().Add(-48 * time.Hour)
	if err := repo.UpsertKeyMemory(ctx, domain.KeyMemory{
		ID:        "target-km",
		UserID:    "user-1",
		Key:       "deep_key",
		Value:     "old",
		CreatedAt: targetCreatedAt,
		UpdatedAt: targetCreatedAt,
		Metadata:  map[string]any{"node_id": "target-node"},
	}); err != nil {
		t.Fatalf("seed target key memory: %v", err)
	}
	for idx := range 1005 {
		now := time.Now().UTC().Add(time.Duration(idx) * time.Second)
		if err := repo.UpsertKeyMemory(ctx, domain.KeyMemory{
			ID:        "filler-" + strconv.Itoa(idx),
			UserID:    "user-1",
			Key:       "filler_" + strconv.Itoa(idx),
			Value:     "value",
			CreatedAt: now,
			UpdatedAt: now,
			Metadata:  map[string]any{"node_id": "filler-node-" + strconv.Itoa(idx)},
		}); err != nil {
			t.Fatalf("seed filler key memory %d: %v", idx, err)
		}
	}

	updated, err := service.UpsertKeyMemory(ctx, domain.KeyMemory{
		UserID: "user-1",
		Key:    "deep_key",
		Value:  "new",
	})
	if err != nil {
		t.Fatalf("update target key memory: %v", err)
	}
	if updated.ID != "target-km" || stringMetadataValue(updated.Metadata, "node_id") != "target-node" {
		t.Fatalf("expected existing identity beyond list limit, got %+v", updated)
	}
	foundByID, ok, err := service.GetKeyMemoryByID(ctx, "user-1", "target-km")
	if err != nil {
		t.Fatalf("get target key memory by id: %v", err)
	}
	if !ok || foundByID.ID != "target-km" || foundByID.Key != "deep_key" {
		t.Fatalf("expected deterministic lookup by id beyond list limit, got ok=%v item=%+v", ok, foundByID)
	}
	docs, err := repo.ListDocuments(ctx, "user-1", []domain.MemoryType{domain.MemoryTypeKeyMemory}, 2000)
	if err != nil {
		t.Fatalf("list docs: %v", err)
	}
	targetDocs := 0
	for _, doc := range docs {
		if doc.ID == "target-km" || doc.Content == "deep_key: new" {
			targetDocs++
			if doc.NodeID != "target-node" || doc.Content != "deep_key: new" {
				t.Fatalf("expected target doc to keep node and content, got %+v", doc)
			}
		}
	}
	if targetDocs != 1 {
		t.Fatalf("expected exactly one updated target document, got %d", targetDocs)
	}
}

func TestListDocumentsByConversationInGenerationDefaultsToActiveGeneration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()
	service := NewService(nil, repo)
	generations := memorygenservice.NewService(nil, "text-embedding-3-large")
	service.SetGenerationService(generations)

	active, err := generations.EnsureActiveGeneration(ctx, "user-1")
	if err != nil {
		t.Fatalf("ensure active generation: %v", err)
	}

	for _, doc := range []domain.MemoryDocument{
		{
			ID:             "summary-active",
			NodeID:         "node-summary-active",
			UserID:         "user-1",
			ConversationID: "conv-1",
			GenerationID:   active.GenerationID,
			Type:           domain.MemoryTypeSummary,
			Content:        "Active generation summary.",
		},
		{
			ID:             "summary-candidate",
			NodeID:         "node-summary-candidate",
			UserID:         "user-1",
			ConversationID: "conv-1",
			GenerationID:   "gen:rebuild:user-1",
			Type:           domain.MemoryTypeSummary,
			Content:        "Candidate generation summary.",
		},
	} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store document %s: %v", doc.ID, err)
		}
	}

	documents, err := service.ListDocumentsByConversationInGeneration(ctx, "user-1", "", "conv-1")
	if err != nil {
		t.Fatalf("list documents in active generation: %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("expected 1 active-generation document, got %d", len(documents))
	}
	if documents[0].ID != "summary-active" {
		t.Fatalf("expected active-generation summary, got %s", documents[0].ID)
	}
}

func TestServiceUpsertKeyMemoryLinksConversationSources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()
	service := NewService(nil, repo)

	summary := domain.MemoryDocument{
		ID:             "summary-conv-1",
		NodeID:         "node-summary-conv-1",
		UserID:         "user-1",
		ConversationID: "conv-1",
		Type:           domain.MemoryTypeSummary,
		Content:        "Conversation summary for rollout planning.",
	}
	fact := domain.MemoryDocument{
		ID:             "fact-conv-1",
		NodeID:         "node-fact-conv-1",
		UserID:         "user-1",
		ConversationID: "conv-1",
		Type:           domain.MemoryTypeFact,
		Content:        "The rollback owner is Alice.",
	}
	for _, doc := range []domain.MemoryDocument{summary, fact} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store conversation document %s: %v", doc.ID, err)
		}
	}

	record, err := service.UpsertKeyMemory(ctx, domain.KeyMemory{
		UserID: "user-1",
		Key:    "recent_conversation",
		Value:  "conv-1",
		Source: "worker",
	})
	if err != nil {
		t.Fatalf("upsert linked key memory: %v", err)
	}

	nodeID, _ := record.Metadata["node_id"].(string)
	relations, err := repo.ListRelations(ctx, "user-1", "", []string{nodeID}, 1)
	if err != nil {
		t.Fatalf("list key memory relations: %v", err)
	}
	promoted := make([]domain.MemoryRelation, 0, len(relations))
	for _, relation := range relations {
		if relation.RelationType == "promoted_to_key_memory" {
			promoted = append(promoted, relation)
		}
	}
	if len(promoted) != 2 {
		t.Fatalf("expected 2 conversation source relations, got %d", len(promoted))
	}
	for _, relation := range promoted {
		if relation.RelationType != "promoted_to_key_memory" {
			t.Fatalf("expected promoted_to_key_memory relation, got %+v", relation)
		}
		if relation.DstNodeID != nodeID {
			t.Fatalf("expected key memory node as target, got %+v", relation)
		}
	}
}

func TestStoreDocumentSyncsFactSupersedesRelations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()

	older := domain.MemoryDocument{
		ID:      "fact-old",
		NodeID:  "node-fact-old",
		UserID:  "user-1",
		Type:    domain.MemoryTypeFact,
		Content: "The user lives in Hangzhou.",
		Metadata: map[string]any{
			"fact_key":         "home_location",
			"superseded_by_id": "fact-new",
		},
	}
	newer := domain.MemoryDocument{
		ID:      "fact-new",
		NodeID:  "node-fact-new",
		UserID:  "user-1",
		Type:    domain.MemoryTypeFact,
		Content: "The user now lives in Shanghai.",
		Metadata: map[string]any{
			"fact_key": "home_location",
		},
	}

	if err := repo.StoreDocument(ctx, newer); err != nil {
		t.Fatalf("store newer fact: %v", err)
	}
	if err := repo.StoreDocument(ctx, older); err != nil {
		t.Fatalf("store older fact: %v", err)
	}

	relations, err := repo.ListRelations(ctx, "user-1", "", []string{newer.NodeID}, 1)
	if err != nil {
		t.Fatalf("list supersedes relations: %v", err)
	}
	supersedes := make([]domain.MemoryRelation, 0, len(relations))
	for _, relation := range relations {
		if relation.RelationType == domain.MemoryRelationSupersedes {
			supersedes = append(supersedes, relation)
		}
	}
	if len(supersedes) != 1 {
		t.Fatalf("expected 1 supersedes relation, got %d", len(supersedes))
	}
	relation := supersedes[0]
	if relation.RelationType != domain.MemoryRelationSupersedes || relation.SrcNodeID != newer.NodeID || relation.DstNodeID != older.NodeID {
		t.Fatalf("unexpected supersedes relation: %+v", relation)
	}

	older.Metadata = map[string]any{
		"fact_key": "home_location",
	}
	if err := repo.StoreDocument(ctx, older); err != nil {
		t.Fatalf("retry older fact without supersession metadata: %v", err)
	}
	relations, err = repo.ListRelations(ctx, "user-1", "", []string{newer.NodeID}, 1)
	if err != nil {
		t.Fatalf("list supersedes relations after retry: %v", err)
	}
	supersedes = supersedes[:0]
	for _, relation := range relations {
		if relation.RelationType == domain.MemoryRelationSupersedes {
			supersedes = append(supersedes, relation)
		}
	}
	if len(supersedes) != 1 {
		t.Fatalf("expected supersedes relation to survive retry, got %+v", relations)
	}
	updatedOlder, err := repo.GetDocument(ctx, "user-1", older.ID, domain.MemoryTypeFact)
	if err != nil {
		t.Fatalf("get older fact after retry: %v", err)
	}
	if updatedOlder.Metadata["superseded_by_id"] != newer.ID {
		t.Fatalf("expected older fact retry to preserve superseded_by_id, got %+v", updatedOlder.Metadata)
	}

	older.Metadata = map[string]any{
		"fact_key":         "home_location",
		"superseded_by_id": "",
	}
	if err := repo.StoreDocument(ctx, older); err != nil {
		t.Fatalf("explicitly clear superseded state: %v", err)
	}
	relations, err = repo.ListRelations(ctx, "user-1", "", []string{newer.NodeID}, 1)
	if err != nil {
		t.Fatalf("list supersedes relations after clear: %v", err)
	}
	for _, relation := range relations {
		if relation.RelationType == domain.MemoryRelationSupersedes {
			t.Fatalf("expected supersedes relation to be removed, got %+v", relations)
		}
	}
}

func TestStoreDocumentMarksExplicitlySupersededFacts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()

	older := domain.MemoryDocument{
		ID:      "fact-location-old",
		NodeID:  "node-fact-location-old",
		UserID:  "user-1",
		Type:    domain.MemoryTypeFact,
		Content: "The user lives in Hangzhou.",
		Metadata: map[string]any{
			"category": "location",
			"subject":  "user",
			"fact_key": "home_location",
		},
	}
	newer := domain.MemoryDocument{
		ID:      "fact-location-new",
		NodeID:  "node-fact-location-new",
		UserID:  "user-1",
		Type:    domain.MemoryTypeFact,
		Content: "The user now lives in Shanghai.",
		Metadata: map[string]any{
			"category":             "location",
			"subject":              "user",
			"fact_key":             "home_location",
			"supersedes_fact_ids":  []string{"fact-location-old"},
			"supersession_source":  "analysis",
			"supersession_trigger": "conflicting_fact",
		},
	}

	if err := repo.StoreDocument(ctx, older); err != nil {
		t.Fatalf("store older fact: %v", err)
	}
	if err := repo.StoreDocument(ctx, newer); err != nil {
		t.Fatalf("store newer fact: %v", err)
	}
	updatedOlder, err := repo.GetDocument(ctx, "user-1", older.ID, domain.MemoryTypeFact)
	if err != nil {
		t.Fatalf("get older fact: %v", err)
	}
	if updatedOlder.Metadata["superseded_by_id"] != newer.ID {
		t.Fatalf("expected older fact to point at superseding fact, got %+v", updatedOlder.Metadata)
	}
	if !domain.IsStaleMemory(updatedOlder, time.Now().UTC()) {
		t.Fatalf("expected older fact to be stale after supersession, got %+v", updatedOlder.Metadata)
	}
	older.Metadata = map[string]any{
		"fact_key": "home_location",
	}
	if err := repo.StoreDocument(ctx, older); err != nil {
		t.Fatalf("retry older fact: %v", err)
	}
	updatedOlder, err = repo.GetDocument(ctx, "user-1", older.ID, domain.MemoryTypeFact)
	if err != nil {
		t.Fatalf("get older fact after retry: %v", err)
	}
	if updatedOlder.Metadata["superseded_by_id"] != newer.ID || !domain.IsStaleMemory(updatedOlder, time.Now().UTC()) {
		t.Fatalf("expected retry to preserve supersession state, got %+v", updatedOlder.Metadata)
	}

	relations, err := repo.ListRelations(ctx, "user-1", "", []string{newer.NodeID}, 1)
	if err != nil {
		t.Fatalf("list supersedes relations: %v", err)
	}
	for _, relation := range relations {
		if relation.RelationType == domain.MemoryRelationSupersedes && relation.SrcNodeID == newer.NodeID && relation.DstNodeID == older.NodeID {
			return
		}
	}
	t.Fatalf("expected write-time supersedes relation, got %+v", relations)
}

func TestServiceSearchFoldsSupersededFactToCurrentHead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()
	engine := NewEngine(repo, nil)
	service := NewService(engine, repo)

	older := domain.MemoryDocument{
		ID:      "fact-location-old",
		NodeID:  "node-fact-location-old",
		UserID:  "user-1",
		Type:    domain.MemoryTypeFact,
		Content: "The user lives in Hangzhou.",
		Metadata: map[string]any{
			"fact_key": "home_location",
		},
	}
	newer := domain.MemoryDocument{
		ID:      "fact-location-new",
		NodeID:  "node-fact-location-new",
		UserID:  "user-1",
		Type:    domain.MemoryTypeFact,
		Content: "The user now lives in Shanghai.",
		Metadata: map[string]any{
			"fact_key": "home_location",
		},
	}
	if err := repo.StoreDocument(ctx, older); err != nil {
		t.Fatalf("store older fact: %v", err)
	}
	if err := repo.StoreDocument(ctx, newer); err != nil {
		t.Fatalf("store newer fact: %v", err)
	}

	result, err := service.Search(ctx, domain.SearchInput{
		UserID:      "user-1",
		Query:       "home_location",
		TopN:        5,
		MemoryTypes: []domain.MemoryType{domain.MemoryTypeFact},
	})
	if err != nil {
		t.Fatalf("search facts: %v", err)
	}
	if len(result.Memories) != 1 {
		t.Fatalf("expected folded current-head result, got %+v", result.Memories)
	}
	got := result.Memories[0]
	if got.ID != newer.ID {
		t.Fatalf("expected current head %s, got %+v", newer.ID, got)
	}
	if got.Lineage == nil || got.Lineage.CurrentHeadID != newer.ID || got.Lineage.RevisionDepth != 2 {
		t.Fatalf("expected hydrated lineage metadata, got %+v", got.Lineage)
	}
	if len(got.Lineage.SupersededAliasNodeIDs) != 1 || got.Lineage.SupersededAliasNodeIDs[0] != older.NodeID {
		t.Fatalf("expected superseded alias node, got %+v", got.Lineage)
	}
}

func TestServiceSearchFoldsLongFactLineageToCurrentHead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()
	engine := NewEngine(repo, nil)
	service := NewService(engine, repo)

	for idx, city := range []string{"Hangzhou", "Shanghai", "Tokyo", "London", "New York"} {
		doc := domain.MemoryDocument{
			ID:      fmt.Sprintf("fact-location-%d", idx+1),
			NodeID:  fmt.Sprintf("node-fact-location-%d", idx+1),
			UserID:  "user-1",
			Type:    domain.MemoryTypeFact,
			Content: "The user lives in " + city + ".",
			Metadata: map[string]any{
				"fact_key": "home_location",
			},
		}
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store fact %d: %v", idx+1, err)
		}
	}

	result, err := service.Search(ctx, domain.SearchInput{
		UserID:      "user-1",
		Query:       "home_location Hangzhou",
		TopN:        5,
		MemoryTypes: []domain.MemoryType{domain.MemoryTypeFact},
	})
	if err != nil {
		t.Fatalf("search facts: %v", err)
	}
	if len(result.Memories) != 1 {
		t.Fatalf("expected folded current-head result, got %+v", result.Memories)
	}
	got := result.Memories[0]
	if got.ID != "fact-location-5" {
		t.Fatalf("expected long chain current head, got %+v", got)
	}
	if got.Lineage == nil || got.Lineage.RevisionDepth != 5 {
		t.Fatalf("expected full lineage metadata, got %+v", got.Lineage)
	}
}

func TestServiceGetDocumentHydratesRelatedAndSupportsUpdateDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()
	vectors := vectorstore.NewPGVectorStore(nil)
	engine := NewEngine(repo, vectors)
	service := NewService(engine, repo)
	service.SetVectorStore(vectors)

	summary := domain.MemoryDocument{
		ID:          "summary-1",
		NodeID:      "node-summary-1",
		UserID:      "user-1",
		Type:        domain.MemoryTypeSummary,
		Content:     "Release checklist and deployment follow-up.",
		CreatedAt:   time.Now().Add(-time.Hour),
		UpdatedAt:   time.Now().Add(-time.Hour),
		Importance:  1.1,
		DecayFactor: 1,
	}
	fact := domain.MemoryDocument{
		ID:          "fact-1",
		NodeID:      "node-fact-1",
		UserID:      "user-1",
		Type:        domain.MemoryTypeFact,
		Content:     "Rollback owner is Alice Chen.",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Importance:  1.0,
		DecayFactor: 1,
	}
	if err := repo.StoreDocument(ctx, summary); err != nil {
		t.Fatalf("store summary: %v", err)
	}
	if err := repo.StoreDocument(ctx, fact); err != nil {
		t.Fatalf("store fact: %v", err)
	}
	if err := repo.LinkDocuments(ctx, domain.MemoryRelation{
		UserID:       "user-1",
		SrcNodeID:    summary.NodeID,
		DstNodeID:    fact.NodeID,
		RelationType: "derived_from",
		Weight:       0.9,
	}); err != nil {
		t.Fatalf("link documents: %v", err)
	}

	document, err := service.GetDocument(ctx, "user-1", "summary-1", domain.MemoryTypeSummary)
	if err != nil {
		t.Fatalf("get hydrated document: %v", err)
	}
	if len(document.Related) != 1 || document.Related[0].ID != "fact-1" {
		t.Fatalf("expected related fact hydration, got %+v", document.Related)
	}

	listed, err := service.ListDocuments(ctx, "user-1", []domain.MemoryType{domain.MemoryTypeSummary, domain.MemoryTypeFact}, 10)
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected 2 listed documents, got %d", len(listed))
	}

	updated, err := service.UpdateDocument(ctx, "user-1", "fact-1", domain.MemoryTypeFact, "Rollback owner is Bob Li.")
	if err != nil {
		t.Fatalf("update fact: %v", err)
	}
	if updated.Content != "Rollback owner is Bob Li." {
		t.Fatalf("unexpected updated content: %q", updated.Content)
	}

	km, err := service.UpsertKeyMemory(ctx, domain.KeyMemory{
		UserID: "user-1",
		Key:    "recent_conversation",
		Value:  "conv-9",
	})
	if err != nil {
		t.Fatalf("upsert key memory: %v", err)
	}
	if err := service.DeleteKeyMemory(ctx, "user-1", "recent_conversation"); err != nil {
		t.Fatalf("delete key memory: %v", err)
	}
	memories, err := repo.ListKeyMemories(ctx, "user-1", 10)
	if err != nil {
		t.Fatalf("list key memories after delete: %v", err)
	}
	if len(memories) != 0 {
		t.Fatalf("expected deleted key memory to be removed, got %d", len(memories))
	}
	searchResult, err := engine.Search(ctx, domain.SearchInput{
		UserID:      "user-1",
		Query:       "recent_conversation: conv-9",
		TopN:        3,
		MemoryTypes: []domain.MemoryType{domain.MemoryTypeKeyMemory},
	})
	if err != nil {
		t.Fatalf("search deleted key memory: %v", err)
	}
	for _, item := range searchResult.Memories {
		if item.ID == km.ID {
			t.Fatalf("deleted key memory %s should not remain searchable", km.ID)
		}
	}
}

func TestSearchKeyMemoriesRanksByEffectiveScoreInsteadOfUpdatedAt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()
	service := NewService(nil, repo)

	now := time.Now().UTC()
	if err := repo.UpsertKeyMemory(ctx, domain.KeyMemory{
		ID:           "km-old-important",
		UserID:       "user-1",
		Key:          "deployment_owner",
		Value:        "Alice",
		Importance:   1.8,
		DecayFactor:  2.5,
		ActivityAt:   now.Add(-20 * 24 * time.Hour),
		TimeTier:     domain.TimeTierShortTerm,
		CreatedAt:    now.Add(-20 * 24 * time.Hour),
		UpdatedAt:    now.Add(-20 * 24 * time.Hour),
		ReviewStatus: "approved",
		Metadata: map[string]any{
			"node_id": "node-km-old-important",
		},
	}); err != nil {
		t.Fatalf("seed older important key memory: %v", err)
	}
	if err := repo.UpsertKeyMemory(ctx, domain.KeyMemory{
		ID:           "km-newer-weaker",
		UserID:       "user-1",
		Key:          "scratch_note",
		Value:        "temporary follow-up",
		Importance:   0.7,
		DecayFactor:  0.6,
		ActivityAt:   now.Add(-2 * time.Hour),
		TimeTier:     domain.TimeTierActive,
		CreatedAt:    now.Add(-2 * time.Hour),
		UpdatedAt:    now.Add(-2 * time.Hour),
		ReviewStatus: "pending",
		Metadata: map[string]any{
			"node_id": "node-km-newer-weaker",
		},
	}); err != nil {
		t.Fatalf("seed newer weaker key memory: %v", err)
	}

	items, err := service.SearchKeyMemories(ctx, domain.KeyMemoryQuery{
		UserID: "user-1",
		TopK:   2,
	})
	if err != nil {
		t.Fatalf("search key memories: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 key memories, got %d", len(items))
	}
	if items[0].ID != "km-old-important" {
		t.Fatalf("expected effective-score ranking to prefer older important key memory, got %s", items[0].ID)
	}
	if items[0].EffectiveScore <= items[1].EffectiveScore {
		t.Fatalf("expected first key memory to have higher effective score, got %f <= %f", items[0].EffectiveScore, items[1].EffectiveScore)
	}
}

func TestPackInjectionMemoriesHonorsTokenBudget(t *testing.T) {
	t.Parallel()

	memories := []domain.MemoryDocument{
		{
			ID:             "km-1",
			Type:           domain.MemoryTypeKeyMemory,
			Content:        "preferred_style: explicit Go naming and small interfaces",
			EffectiveScore: 0.95,
		},
		{
			ID:             "summary-1",
			Type:           domain.MemoryTypeSummary,
			Content:        "This is a deliberately long summary block that should exceed a very small token budget once the packer has already selected the key memory ahead of it.",
			EffectiveScore: 0.9,
		},
		{
			ID:             "fact-1",
			Type:           domain.MemoryTypeFact,
			Content:        "Rollback owner is Alice.",
			EffectiveScore: 0.85,
		},
	}

	selected := packInjectionMemories(memories, estimateMemoryTokens(memories[0])+estimateMemoryTokens(memories[2]))
	if len(selected) != 2 {
		t.Fatalf("expected 2 memories to fit the budget, got %d", len(selected))
	}
	if selected[0].ID != "km-1" || selected[1].ID != "fact-1" {
		t.Fatalf("expected packer to keep high-signal items within budget, got %+v", selected)
	}
}

func TestMergeInjectionMemoriesUsesTypePriority(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	merged := mergeInjectionMemories([]domain.MemoryDocument{
		{ID: "impression-1", NodeID: "node-impression-1", Type: domain.MemoryTypeImpression, Content: "Prefers direct answers.", EffectiveScore: 0.99, ActivityAt: now},
		{ID: "summary-1", NodeID: "node-summary-1", Type: domain.MemoryTypeSummary, Content: "Discussed rollout planning.", EffectiveScore: 0.4, ActivityAt: now},
		{ID: "fact-1", NodeID: "node-fact-1", Type: domain.MemoryTypeFact, Content: "Rollback owner is Alice.", EffectiveScore: 0.9, ActivityAt: now},
	}, []domain.MemoryDocument{
		{ID: "km-1", NodeID: "node-km-1", Type: domain.MemoryTypeKeyMemory, Content: "timezone: UTC+8", EffectiveScore: 0.1, ActivityAt: now},
	})

	if got := []string{merged[0].ID, merged[1].ID, merged[2].ID, merged[3].ID}; strings.Join(got, ",") != "km-1,summary-1,fact-1,impression-1" {
		t.Fatalf("unexpected injection priority order: %+v", got)
	}

	prompt := buildInjectionSystemPrompt(merged)
	for _, section := range []string{"User Profile:", "Relevant Memories:", "Relevant Facts:", "Relevant Impressions:"} {
		if !strings.Contains(prompt, section) {
			t.Fatalf("expected prompt section %q in %q", section, prompt)
		}
	}
}

func TestDeleteDocumentIsIdempotentForMissingSegment(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()

	if err := repo.DeleteDocument(ctx, "user-1", "segment:missing:000", domain.MemoryTypeSegment); err != nil {
		t.Fatalf("expected deleting a missing segment document to be idempotent, got %v", err)
	}
}
