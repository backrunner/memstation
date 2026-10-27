package memory

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"

	"github.com/google/uuid"
)

func TestProcessingResultsKeepFirstValidatedWriter(t *testing.T) {
	runProcessingFirstWriter(t, NewInMemoryMemoryRepository())
}

func TestPostgresProcessingResultsKeepFirstValidatedWriter(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("database not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runProcessingFirstWriter(t, NewPostgresMemoryRepository(db))
}

func runProcessingFirstWriter(t *testing.T, repo ports.MemoryRepository) {
	t.Helper()
	ctx := context.Background()
	owner := "llm-review-" + uuid.NewString()
	provisionFixtureGeneration(t, repo, ctx, owner, "")
	doc := domain.MemoryDocument{ID: uuid.NewString(), UserID: owner, Type: domain.MemoryTypeSummary, Content: "first validated summary", Metadata: map[string]any{"_llm_summary_checkpoint": map[string]any{"version": 1, "source_hash": "same-source"}}}
	if err := repo.StoreDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetDocument(ctx, owner, doc.ID, doc.Type)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := repo.(ports.SummaryCheckpointRepository)
	for _, key := range []string{"_llm_analysis_checkpoint", "_llm_analysis_batch_00"} {
		for _, value := range []string{"first", "competing"} {
			if err := checkpoint.StoreSummaryCheckpoint(ctx, owner, stored.GenerationID, doc.ID, doc.Content, key, map[string]any{"version": 1, "source_hash": "same-analysis", "value": value}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := checkpoint.StoreSummaryCheckpoint(ctx, owner, stored.GenerationID, doc.ID, doc.Content, "_llm_analysis_batch_00", map[string]any{"version": 1, "source_hash": "same-analysis", "value": strings.Repeat("x", 300*1024)}); err != nil {
		t.Fatal("same-input replay did not keep bounded first writer", err)
	}
	after, err := repo.GetDocument(ctx, owner, doc.ID, doc.Type)
	if err != nil {
		t.Fatal(err)
	}
	if dbutilstore.StringValue(after.Metadata["_llm_analysis_checkpoint"].(map[string]any), "value") != "first" || dbutilstore.StringValue(after.Metadata["_llm_analysis_batch_00"].(map[string]any), "value") != "first" {
		t.Fatal("competing task replaced validated analysis")
	}
	doc.Content = "different result for same input"
	if err := repo.StoreDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	after, err = repo.GetDocument(ctx, owner, doc.ID, doc.Type)
	if err != nil {
		t.Fatal(err)
	}
	if after.Content != stored.Content || after.Metadata["_llm_analysis_checkpoint"] == nil {
		t.Fatal("competing summary replaced validated result", after)
	}
}
