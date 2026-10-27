package memory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

func TestLexicalCandidateCoverage(t *testing.T) {
	t.Run("memory", func(t *testing.T) { lexicalCandidateContract(t, NewInMemoryMemoryRepository()) })
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
		if dsn == "" {
			t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		lexicalCandidateContract(t, NewPostgresMemoryRepository(db))
	})
}

func lexicalCandidateContract(t *testing.T, repo ports.MemoryRepository) {
	t.Helper()
	ctx := context.Background()
	owner := fmt.Sprintf("lexical-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	store := func(id, user, generation string, kind domain.MemoryType, content string, age time.Duration, metadata map[string]any) {
		t.Helper()
		generation = user + "-" + generation
		provisionFixtureGeneration(t, repo, ctx, user, generation)
		doc := domain.MemoryDocument{ID: owner + id, NodeID: owner + id, UserID: user, GenerationID: generation, Type: kind, Content: content, CreatedAt: now.Add(-age), UpdatedAt: now.Add(-age), Metadata: metadata}
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := repo.DeleteDocument(ctx, user, doc.ID, kind); err != nil {
				t.Error(err)
			}
		})
	}
	search := func(query string, limit int, types ...domain.MemoryType) []domain.KeywordMatch {
		t.Helper()
		matches, err := repo.SearchKeywordMatches(ctx, owner, owner+"-g1", query, limit, types...)
		if err != nil {
			t.Fatal(err)
		}
		return matches
	}
	for _, kind := range []domain.MemoryType{domain.MemoryTypeSummary, domain.MemoryTypeFact, domain.MemoryTypeEpisode, domain.MemoryTypeEvent, domain.MemoryTypeSegment, domain.MemoryTypeImpression} {
		store(string(kind), owner, "g1", kind, "Aurora expedition reached glacier", 24*time.Hour, nil)
		matches := search("glacier", 1, kind)
		if len(matches) != 1 || matches[0].NodeID != owner+string(kind) {
			t.Fatalf("body/type recall %s: %+v", kind, matches)
		}
	}
	store("old-exact", owner, "g1", domain.MemoryTypeFact, "Aurora expedition glacier telescope", 365*24*time.Hour, map[string]any{"fact_key": "observatory_location"})
	for i := 0; i < 30; i++ {
		store(fmt.Sprintf("recent-%02d", i), owner, "g1", domain.MemoryTypeFact, "Aurora", 0, nil)
	}
	store("other-generation", owner, "g2", domain.MemoryTypeFact, "Aurora expedition glacier telescope", 0, nil)
	store("other-user", owner+"-other", "g1", domain.MemoryTypeFact, "Aurora expedition glacier telescope", 0, nil)
	matches := search("Aurora expedition glacier telescope", 1, domain.MemoryTypeFact)
	if len(matches) != 1 || matches[0].NodeID != owner+"old-exact" {
		t.Fatalf("recent distractors or scope displaced old evidence: %+v", matches)
	}
	if got := search("observatory_location", 1); len(got) != 1 || got[0].NodeID != owner+"old-exact" {
		t.Fatalf("fact key lost: %+v", got)
	}
	// Underscores are literal characters, not SQL LIKE wildcards.
	if got := search("observatoryXlocation", 1); len(got) != 0 {
		t.Fatalf("wildcard match: %+v", got)
	}
}
