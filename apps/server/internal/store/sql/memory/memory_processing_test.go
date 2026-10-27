package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"

	"github.com/google/uuid"
)

func TestLLMReplayPreservesManualCorrections(t *testing.T) {
	runLLMReplayEdits(t, NewInMemoryMemoryRepository())
}

func TestPostgresLLMReplayPreservesManualCorrections(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("database not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runLLMReplayEdits(t, NewPostgresMemoryRepository(db))
}

func runLLMReplayEdits(t *testing.T, repo ports.MemoryRepository) {
	t.Helper()
	ctx := context.Background()
	owner := "manual-replay-" + uuid.NewString()
	provisionFixtureGeneration(t, repo, ctx, owner, "")
	for _, kind := range []domain.MemoryType{domain.MemoryTypeSummary, domain.MemoryTypeFact, domain.MemoryTypeImpression, domain.MemoryTypeTask} {
		original := domain.MemoryDocument{ID: uuid.NewString(), UserID: owner, Type: kind, Content: "model output", Metadata: map[string]any{"extraction_method": "llm"}}
		if err := repo.StoreDocument(ctx, original); err != nil {
			t.Fatal(err)
		}
		stored, err := repo.GetDocument(ctx, owner, original.ID, kind)
		if err != nil {
			t.Fatal(err)
		}
		_, err = repo.(ports.ConditionalMemoryRepository).UpdateDocumentConditional(ctx, owner, stored.ID, kind, "human correction", domain.MemoryWriteCondition{UpdatedAt: stored.UpdatedAt, GenerationID: stored.GenerationID})
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.StoreDocument(ctx, original); !errors.Is(err, domain.ErrAccessConflict) {
			t.Fatal("model replay was allowed to replace an edited document", kind, err)
		}
		current, err := repo.GetDocument(ctx, owner, stored.ID, kind)
		if err != nil || current.Content != "human correction" {
			t.Fatal(kind, current, err)
		}
	}
}

func TestSummaryCheckpoints(t *testing.T) {
	runSummaryCheckpoints(t, NewInMemoryMemoryRepository(), nil)
}

func TestPostgresSummaryCheckpoints(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runSummaryCheckpoints(t, NewPostgresMemoryRepository(db), db)
}

func runSummaryCheckpoints(t *testing.T, repo ports.MemoryRepository, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	owner := "checkpoint-" + uuid.NewString()
	provisionFixtureGeneration(t, repo, ctx, owner, "")
	doc := domain.MemoryDocument{ID: uuid.NewString(), UserID: owner, Type: domain.MemoryTypeSummary, Content: "validated summary", Metadata: map[string]any{"extraction_method": "llm", "_llm_summary_checkpoint": map[string]any{"version": 1}}}
	if err := repo.StoreDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetDocument(ctx, owner, doc.ID, doc.Type)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Metadata["_llm_summary_checkpoint"] == nil {
		t.Fatal("summary processing metadata was not persisted")
	}
	if db != nil {
		if _, err := db.ExecContext(ctx, `UPDATE memory_nodes SET embedding_status='ready' WHERE user_id=$1 AND id=$2`, owner, stored.NodeID); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint := repo.(ports.SummaryCheckpointRepository)
	for _, key := range []string{"_llm_analysis_checkpoint", "_llm_keymemory_checkpoint", "_llm_analysis_batch_00", "_llm_analysis_batch_31"} {
		if err := checkpoint.StoreSummaryCheckpoint(ctx, owner, stored.GenerationID, doc.ID, doc.Content, key, map[string]any{"version": 1}); err != nil {
			t.Fatal(err)
		}
	}
	// A new repository instance represents a process restart, not an in-memory cache.
	if db != nil {
		repo = NewPostgresMemoryRepository(db)
	}
	after, err := repo.GetDocument(ctx, owner, doc.ID, doc.Type)
	if err != nil {
		t.Fatal(err)
	}
	if !after.UpdatedAt.Equal(stored.UpdatedAt) || after.Content != stored.Content || after.Metadata["_llm_analysis_checkpoint"] == nil || after.Metadata["_llm_keymemory_checkpoint"] == nil {
		t.Fatal("checkpoint modified document or was lost", after)
	}
	if db != nil {
		var status string
		if err := db.QueryRowContext(ctx, `SELECT embedding_status FROM memory_nodes WHERE user_id=$1 AND id=$2`, owner, stored.NodeID).Scan(&status); err != nil || status != "ready" {
			t.Fatal("checkpoint invalidated embedding", status, err)
		}
	}
	raw, _ := json.Marshal(after)
	if strings.Contains(string(raw), "_llm_") {
		t.Fatal("private processing result exposed")
	}
	if after.Metadata["_llm_analysis_batch_00"] == nil || after.Metadata["_llm_analysis_batch_31"] == nil {
		t.Fatal("batch checkpoints lost")
	}
	for _, key := range []string{"_llm_analysis_batch_32", "_llm_analysis_batch_0", "_llm_analysis_batch_xx"} {
		if err := checkpoint.StoreSummaryCheckpoint(ctx, owner, stored.GenerationID, doc.ID, doc.Content, key, map[string]any{"version": 1}); err == nil {
			t.Fatal("unbounded checkpoint slot accepted", key)
		}
	}
	if err := checkpoint.StoreSummaryCheckpoint(ctx, owner, stored.GenerationID, doc.ID, doc.Content, "_llm_analysis_batch_01", map[string]any{"version": 1, "source_hash": "oversize", "text": strings.Repeat("x", 256*1024)}); err == nil {
		t.Fatal("oversize batch checkpoint accepted")
	}
	if err := checkpoint.StoreSummaryCheckpoint(ctx, owner, stored.GenerationID, doc.ID, "outdated body", "_llm_analysis_checkpoint", nil); !errors.Is(err, domain.ErrAccessConflict) {
		t.Fatal("stale body accepted", err)
	}
	if err := checkpoint.StoreSummaryCheckpoint(ctx, "foreign", stored.GenerationID, doc.ID, doc.Content, "_llm_analysis_checkpoint", nil); err == nil {
		t.Fatal("foreign checkpoint write accepted")
	}
	if err := checkpoint.StoreSummaryCheckpoint(ctx, owner, stored.GenerationID, doc.ID, doc.Content, "topic_label", "overwrite"); err == nil {
		t.Fatal("public metadata overwrite accepted")
	}
	// A model's explicit empty replacement decision must survive storage. A
	// repeated semantic key alone is insufficient to discard the older fact.
	for index, content := range []string{"User owns project A", "User also owns project B"} {
		fact := domain.MemoryDocument{ID: uuid.NewString(), UserID: owner, Type: domain.MemoryTypeFact, Content: content, Metadata: map[string]any{"fact_key": "projects", "subject": "user", "extraction_method": "llm", "supersedes_fact_ids": []string{}, "source_quotes": []any{map[string]any{"quote": content}}}}
		if index == 0 {
			fact.Metadata["valid_from"] = "2026-10-23T00:00:00Z"
		} else {
			fact.Metadata["valid_from"] = "2026-10-24T00:00:00Z"
		}
		if err := repo.StoreDocument(ctx, fact); err != nil {
			t.Fatal(err)
		}
	}
	facts, err := repo.ListDocuments(ctx, owner, []domain.MemoryType{domain.MemoryTypeFact}, 10)
	if err != nil || len(facts) != 2 {
		t.Fatal(facts, err)
	}
	for _, fact := range facts {
		if fact.Metadata["extraction_method"] != "llm" || len(fact.Metadata["source_quotes"].([]any)) != 1 || dbutilstore.BoolValue(fact.Metadata, "is_superseded") {
			t.Fatal("LLM decision/provenance was lost", fact)
		}
	}
	history, err := repo.SearchFactsAt(ctx, domain.SearchInput{UserID: owner, GenerationID: stored.GenerationID})
	if err != nil || len(history) != 2 {
		t.Fatal("historical facts missing", history, err)
	}
	for _, fact := range history {
		if fact.Metadata["extraction_method"] != "llm" || fact.Metadata["source_quotes"] == nil || fact.Metadata["metadata"] != nil {
			t.Fatal("history lost flattened model provenance", fact.Metadata)
		}
	}
	impression := domain.MemoryDocument{ID: uuid.NewString(), UserID: owner, Type: domain.MemoryTypeImpression, Content: "Supported observation", Metadata: map[string]any{"extraction_method": "llm", "source_quotes": []any{map[string]any{"quote": "source"}}}}
	if err := repo.StoreDocument(ctx, impression); err != nil {
		t.Fatal(err)
	}
	read, err := repo.GetDocument(ctx, owner, impression.ID, impression.Type)
	if err != nil || read.Metadata["extraction_method"] != "llm" || len(read.Metadata["source_quotes"].([]any)) != 1 {
		t.Fatal("impression provenance was lost", read, err)
	}
}

func TestMemoryEditAuditUsesTrustedWorkerActor(t *testing.T) {
	ctx := context.Background()
	event := memoryEditEvent(ctx, "owner", "update_key_memory", "key_memory", "key", &domain.MemoryWriteCondition{ActorType: "system", ActorID: "worker"})
	if event.ActorType != "system" || event.ActorID != "worker" {
		t.Fatal(event)
	}
	event = memoryEditEvent(ctx, "owner", "update_key_memory", "key_memory", "key", &domain.MemoryWriteCondition{})
	if event.ActorType != "admin" || event.ActorID != "admin" {
		t.Fatal(event)
	}
}
