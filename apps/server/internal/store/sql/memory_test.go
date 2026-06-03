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
	relations, err := repo.ListRelations(ctx, "user-1", "", []string{staleDoc.NodeID}, 1)
	if err != nil {
		t.Fatalf("list stale relations: %v", err)
	}
	if len(relations) != 0 {
		t.Fatalf("expected stale relations to be removed, got %+v", relations)
	}
}

func TestInMemoryMemoryRepositoryListRelationsDoesNotExpandThroughTemporalNodes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	now := time.Date(2026, 4, 9, 9, 0, 0, 0, time.UTC)

	root := domain.MemoryDocument{
		ID:        "summary-root",
		NodeID:    "node-summary-root",
		UserID:    "user-1",
		Type:      domain.MemoryTypeSummary,
		Content:   "Root memory document.",
		CreatedAt: now,
		UpdatedAt: now,
	}
	neighbor := domain.MemoryDocument{
		ID:        "summary-neighbor",
		NodeID:    "node-summary-neighbor",
		UserID:    "user-1",
		Type:      domain.MemoryTypeSummary,
		Content:   "Neighbor memory document.",
		CreatedAt: now,
		UpdatedAt: now,
	}
	for _, doc := range []domain.MemoryDocument{root, neighbor} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store doc %s: %v", doc.ID, err)
		}
	}
	if err := repo.LinkDocuments(ctx, domain.MemoryRelation{
		UserID:       "user-1",
		SrcNodeID:    neighbor.NodeID,
		DstNodeID:    "node-fact-neighbor",
		RelationType: "derived_from",
		Weight:       0.9,
		Source:       "test",
	}); err != nil {
		t.Fatalf("link neighbor relation: %v", err)
	}

	relations, err := repo.ListRelations(ctx, "user-1", "", []string{root.NodeID}, 2)
	if err != nil {
		t.Fatalf("list relations: %v", err)
	}
	for _, relation := range relations {
		if relation.RelationType == "derived_from" {
			t.Fatalf("expected temporal frontier walk to stay within the root graph, got %+v", relations)
		}
	}
}

func TestInMemoryMemoryRepositoryStoresFactSupersedesRelations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	now := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)

	older := domain.MemoryDocument{
		ID:        "fact-old",
		NodeID:    "node-fact-old",
		UserID:    "user-1",
		Type:      domain.MemoryTypeFact,
		Content:   "The user lives in Hangzhou.",
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: map[string]any{
			"fact_key":         "home_location",
			"superseded_by_id": "fact-new",
			"lineage_group_id": "fact-lineage:user-1:home_location",
			"revision_seq":     1,
		},
	}
	newer := domain.MemoryDocument{
		ID:        "fact-new",
		NodeID:    "node-fact-new",
		UserID:    "user-1",
		Type:      domain.MemoryTypeFact,
		Content:   "The user now lives in Shanghai.",
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: map[string]any{
			"fact_key":         "home_location",
			"lineage_group_id": "fact-lineage:user-1:home_location",
			"revision_seq":     2,
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
		t.Fatalf("list relations: %v", err)
	}
	found := false
	for _, relation := range relations {
		if relation.RelationType == domain.MemoryRelationSupersedes && relation.SrcNodeID == newer.NodeID && relation.DstNodeID == older.NodeID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected supersedes relation, got %+v", relations)
	}

	lineage, err := repo.ListFactLineage(ctx, "user-1", currentGenerationID("user-1"), newer.ID)
	if err != nil {
		t.Fatalf("list fact lineage: %v", err)
	}
	if lineage.CurrentHeadID != newer.ID || len(lineage.Entries) != 2 {
		t.Fatalf("expected 2-entry lineage with new head, got %+v", lineage)
	}
}

func TestInMemoryMemoryRepositoryRejectsDanglingFactSupersessionTargets(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	doc := domain.MemoryDocument{
		ID:      "fact-location-current",
		NodeID:  "node-fact-location-current",
		UserID:  "user-1",
		Type:    domain.MemoryTypeFact,
		Content: "The user now lives in Shanghai.",
		Metadata: map[string]any{
			"fact_key":            "home_location",
			"supersedes_fact_ids": []string{"fact-location-missing"},
		},
	}
	if err := repo.StoreDocument(ctx, doc); err == nil {
		t.Fatal("expected dangling supersession target to fail")
	}
	if _, err := repo.GetDocument(ctx, "user-1", doc.ID, domain.MemoryTypeFact); err == nil {
		t.Fatal("expected failed supersession write to roll back the fact")
	}
}

func TestInMemoryMemoryRepositoryAutoSupersedesLineageHead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	now := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)

	older := domain.MemoryDocument{
		ID:        "fact-home-old",
		NodeID:    "node-fact-home-old",
		UserID:    "user-1",
		Type:      domain.MemoryTypeFact,
		Content:   "The user lives in Hangzhou.",
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: map[string]any{
			"fact_key": "home_location",
		},
	}
	newer := domain.MemoryDocument{
		ID:        "fact-home-new",
		NodeID:    "node-fact-home-new",
		UserID:    "user-1",
		Type:      domain.MemoryTypeFact,
		Content:   "The user now lives in Shanghai.",
		CreatedAt: now.Add(time.Minute),
		UpdatedAt: now.Add(time.Minute),
		Metadata: map[string]any{
			"fact_key": "home_location",
		},
	}
	otherKey := domain.MemoryDocument{
		ID:        "fact-work-owner",
		NodeID:    "node-fact-work-owner",
		UserID:    "user-1",
		Type:      domain.MemoryTypeFact,
		Content:   "Alice owns the rollout.",
		CreatedAt: now.Add(2 * time.Minute),
		UpdatedAt: now.Add(2 * time.Minute),
		Metadata: map[string]any{
			"fact_key": "work_owner",
		},
	}

	for _, doc := range []domain.MemoryDocument{older, newer, otherKey} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store %s: %v", doc.ID, err)
		}
	}

	updatedOlder, err := repo.GetDocument(ctx, "user-1", older.ID, domain.MemoryTypeFact)
	if err != nil {
		t.Fatalf("get older fact: %v", err)
	}
	if updatedOlder.Metadata["superseded_by_id"] != newer.ID {
		t.Fatalf("expected older fact to be superseded by new lineage head, got %+v", updatedOlder.Metadata)
	}
	updatedOtherKey, err := repo.GetDocument(ctx, "user-1", otherKey.ID, domain.MemoryTypeFact)
	if err != nil {
		t.Fatalf("get other-key fact: %v", err)
	}
	if updatedOtherKey.Metadata["superseded_by_id"] != nil && updatedOtherKey.Metadata["superseded_by_id"] != "" {
		t.Fatalf("expected other fact key to remain independent, got %+v", updatedOtherKey.Metadata)
	}

	lineage, err := repo.ListFactLineage(ctx, "user-1", currentGenerationID("user-1"), older.ID)
	if err != nil {
		t.Fatalf("list fact lineage: %v", err)
	}
	if lineage.CurrentHeadID != newer.ID || lineage.RevisionDepth != 2 {
		t.Fatalf("expected new fact as lineage head, got %+v", lineage)
	}
}

func TestInMemoryMemoryRepositoryLineageDoesNotCrossUsers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()

	userOne := domain.MemoryDocument{
		ID:      "fact-user-one-location",
		NodeID:  "node-fact-user-one-location",
		UserID:  "user-1",
		Type:    domain.MemoryTypeFact,
		Content: "User one lives in Hangzhou.",
		Metadata: map[string]any{
			"fact_key": "home_location",
		},
	}
	userTwo := domain.MemoryDocument{
		ID:      "fact-user-two-location",
		NodeID:  "node-fact-user-two-location",
		UserID:  "user-2",
		Type:    domain.MemoryTypeFact,
		Content: "User two lives in Shanghai.",
		Metadata: map[string]any{
			"fact_key": "home_location",
		},
	}
	if err := repo.StoreDocument(ctx, userOne); err != nil {
		t.Fatalf("store user one fact: %v", err)
	}
	if err := repo.StoreDocument(ctx, userTwo); err != nil {
		t.Fatalf("store user two fact: %v", err)
	}
	updatedUserOne, err := repo.GetDocument(ctx, "user-1", userOne.ID, domain.MemoryTypeFact)
	if err != nil {
		t.Fatalf("get user one fact: %v", err)
	}
	if updatedUserOne.Metadata["superseded_by_id"] != nil && updatedUserOne.Metadata["superseded_by_id"] != "" {
		t.Fatalf("expected cross-user fact to be isolated, got %+v", updatedUserOne.Metadata)
	}
}

func TestInMemoryMemoryRepositoryNormalizesInvalidLineageGroup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	oldLocation := domain.MemoryDocument{
		ID:      "fact-location-old",
		NodeID:  "node-fact-location-old",
		UserID:  "user-1",
		Type:    domain.MemoryTypeFact,
		Content: "The user lives in Hangzhou.",
		Metadata: map[string]any{
			"fact_key":         "home_location",
			"lineage_group_id": "fact-lineage:user-2:home_location",
		},
	}
	newLocation := domain.MemoryDocument{
		ID:      "fact-location-new",
		NodeID:  "node-fact-location-new",
		UserID:  "user-1",
		Type:    domain.MemoryTypeFact,
		Content: "The user now lives in Shanghai.",
		Metadata: map[string]any{
			"fact_key": "home_location",
		},
	}
	workOwner := domain.MemoryDocument{
		ID:      "fact-work-owner",
		NodeID:  "node-fact-work-owner",
		UserID:  "user-1",
		Type:    domain.MemoryTypeFact,
		Content: "Alice owns the rollout.",
		Metadata: map[string]any{
			"fact_key":         "work_owner",
			"lineage_group_id": "fact-lineage:user-1:home_location",
		},
	}
	for _, doc := range []domain.MemoryDocument{oldLocation, newLocation, workOwner} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store %s: %v", doc.ID, err)
		}
	}
	updatedOld, err := repo.GetDocument(ctx, "user-1", oldLocation.ID, domain.MemoryTypeFact)
	if err != nil {
		t.Fatalf("get old location: %v", err)
	}
	if got := updatedOld.Metadata["lineage_group_id"]; got != "fact-lineage:user-1:home_location" {
		t.Fatalf("expected canonical location lineage group, got %+v", updatedOld.Metadata)
	}
	if got := updatedOld.Metadata["superseded_by_id"]; got != newLocation.ID {
		t.Fatalf("expected canonicalized old location to be superseded, got %+v", updatedOld.Metadata)
	}
	updatedWork, err := repo.GetDocument(ctx, "user-1", workOwner.ID, domain.MemoryTypeFact)
	if err != nil {
		t.Fatalf("get work owner: %v", err)
	}
	if got := updatedWork.Metadata["lineage_group_id"]; got != "fact-lineage:user-1:work_owner" {
		t.Fatalf("expected fact_key to override mismatched explicit group, got %+v", updatedWork.Metadata)
	}
	if got := updatedWork.Metadata["superseded_by_id"]; got != nil && got != "" {
		t.Fatalf("expected unrelated key not to be superseded, got %+v", updatedWork.Metadata)
	}
}

func TestInMemoryMemoryRepositoryRejectsFactSupersessionCycles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	first := domain.MemoryDocument{
		ID:      "fact-cycle-a",
		NodeID:  "node-fact-cycle-a",
		UserID:  "user-1",
		Type:    domain.MemoryTypeFact,
		Content: "First location fact.",
		Metadata: map[string]any{
			"fact_key": "home_location",
		},
	}
	second := domain.MemoryDocument{
		ID:      "fact-cycle-b",
		NodeID:  "node-fact-cycle-b",
		UserID:  "user-1",
		Type:    domain.MemoryTypeFact,
		Content: "Second location fact.",
		Metadata: map[string]any{
			"fact_key":         "home_location",
			"superseded_by_id": first.ID,
		},
	}
	if err := repo.StoreDocument(ctx, first); err != nil {
		t.Fatalf("store first fact: %v", err)
	}
	if err := repo.StoreDocument(ctx, second); err != nil {
		t.Fatalf("store second fact: %v", err)
	}
	first.Metadata = map[string]any{
		"fact_key":         "home_location",
		"superseded_by_id": second.ID,
	}
	if err := repo.StoreDocument(ctx, first); err == nil {
		t.Fatal("expected cycle write to fail")
	}
}

func TestInMemoryMemoryRepositorySearchKeywordMatchesUsesSummaryContent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	now := time.Date(2026, 4, 9, 9, 0, 0, 0, time.UTC)

	if err := repo.StoreDocument(ctx, domain.MemoryDocument{
		ID:        "summary-cn-ui-1",
		NodeID:    "node-summary-cn-ui-1",
		UserID:    "user-1",
		Type:      domain.MemoryTypeSummary,
		Content:   "用户喜欢中文界面，并且希望默认使用浅色主题。",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("store summary: %v", err)
	}

	matches, err := repo.SearchKeywordMatches(ctx, "user-1", currentGenerationID("user-1"), "ui_theme: 浅色主题", 10)
	if err != nil {
		t.Fatalf("search keyword matches: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one lexical summary match, got %+v", matches)
	}
	if matches[0].NodeID != "node-summary-cn-ui-1" {
		t.Fatalf("expected chinese summary node, got %+v", matches[0])
	}
	if len(matches[0].Keywords) == 0 || matches[0].Keywords[0] != "浅色主题" {
		t.Fatalf("expected matched lexical keyword, got %+v", matches[0])
	}
}

func TestInMemoryMemoryRepositorySearchKeywordMatchesUsesFactKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	now := time.Date(2026, 4, 9, 9, 0, 0, 0, time.UTC)

	if err := repo.StoreDocument(ctx, domain.MemoryDocument{
		ID:        "fact-home-location",
		NodeID:    "node-fact-home-location",
		UserID:    "user-1",
		Type:      domain.MemoryTypeFact,
		Content:   "The user now lives in Shanghai.",
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: map[string]any{
			"fact_key": "home_location",
		},
	}); err != nil {
		t.Fatalf("store fact: %v", err)
	}

	matches, err := repo.SearchKeywordMatches(ctx, "user-1", currentGenerationID("user-1"), "home_location", 10)
	if err != nil {
		t.Fatalf("search keyword matches: %v", err)
	}
	if len(matches) != 1 || matches[0].NodeID != "node-fact-home-location" {
		t.Fatalf("expected fact-key match, got %+v", matches)
	}
}

func TestInMemoryMemoryRepositoryUpsertKeyMemoryReplacesDocumentForSameKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()

	if err := repo.UpsertKeyMemory(ctx, domain.KeyMemory{
		ID:     "km-original",
		UserID: "user-1",
		Key:    "preferred_editor",
		Value:  "neovim",
		Metadata: map[string]any{
			"node_id": "node-km-original",
		},
	}); err != nil {
		t.Fatalf("upsert original key memory: %v", err)
	}
	if err := repo.UpsertKeyMemory(ctx, domain.KeyMemory{
		ID:     "km-conflicting",
		UserID: "user-1",
		Key:    "preferred_editor",
		Value:  "zed",
		Metadata: map[string]any{
			"node_id": "node-km-conflicting",
		},
	}); err != nil {
		t.Fatalf("upsert replacement key memory: %v", err)
	}

	memories, err := repo.ListKeyMemories(ctx, "user-1", 10)
	if err != nil {
		t.Fatalf("list key memories: %v", err)
	}
	if len(memories) != 1 || memories[0].ID != "km-original" || memories[0].Value != "zed" {
		t.Fatalf("expected replacement to reuse original identity, got %+v", memories)
	}

	docs, err := repo.ListDocuments(ctx, "user-1", []domain.MemoryType{domain.MemoryTypeKeyMemory}, 10)
	if err != nil {
		t.Fatalf("list key-memory documents: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != "km-original" || docs[0].NodeID != "node-km-original" || docs[0].Content != "preferred_editor: zed" {
		t.Fatalf("expected exactly one replacement document, got %+v", docs)
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

func TestInMemoryConversationRepositoryKeepsSegmentsIsolatedByGeneration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryConversationRepository()
	userID := "user-1"
	conversationID := "conv-1"
	currentGenerationID := currentGenerationID(userID)
	candidateGenerationID := "gen:rebuild:user-1"
	now := time.Date(2026, 4, 8, 9, 0, 0, 0, time.UTC)

	if err := repo.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: conversationID,
		UserID:         userID,
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		SummaryStatus:  domain.ProcessingStatusCompleted,
		AnalysisStatus: domain.ProcessingStatusCompleted,
		Metadata: map[string]any{
			"memory_generation_id": currentGenerationID,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("persist conversation: %v", err)
	}

	repo.segments[key(userID, conversationID)] = []domain.ConversationSegment{
		{
			ID:             candidateGenerationID + ":segment:" + conversationID + ":000",
			GenerationID:   candidateGenerationID,
			UserID:         userID,
			ConversationID: conversationID,
			SegmentIndex:   0,
			Content:        "candidate segment",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}

	if err := repo.ReplaceSegments(ctx, userID, conversationID, []domain.ConversationSegment{
		{
			ID:             "segment:" + conversationID + ":000",
			GenerationID:   currentGenerationID,
			UserID:         userID,
			ConversationID: conversationID,
			SegmentIndex:   0,
			Content:        "current segment",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}); err != nil {
		t.Fatalf("replace current generation segments: %v", err)
	}

	if len(repo.segments[key(userID, conversationID)]) != 2 {
		t.Fatalf("expected both current and candidate segments to be retained, got %d", len(repo.segments[key(userID, conversationID)]))
	}

	conversation, err := repo.GetConversation(ctx, userID, conversationID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if len(conversation.Segments) != 1 || conversation.Segments[0].GenerationID != currentGenerationID {
		t.Fatalf("expected current-generation segments only, got %+v", conversation.Segments)
	}

	if err := repo.UpdateConversationState(ctx, domain.ConversationStateUpdate{
		ConversationID: conversationID,
		UserID:         userID,
		Metadata: map[string]any{
			"memory_generation_id": candidateGenerationID,
		},
		UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("update conversation generation metadata: %v", err)
	}

	conversation, err = repo.GetConversation(ctx, userID, conversationID)
	if err != nil {
		t.Fatalf("get conversation after generation switch: %v", err)
	}
	if len(conversation.Segments) != 1 || conversation.Segments[0].GenerationID != candidateGenerationID {
		t.Fatalf("expected candidate-generation segments after switch, got %+v", conversation.Segments)
	}
}
