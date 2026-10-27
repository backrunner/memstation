package memory

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"memstation/apps/server/internal/domain"
)

// Direct channels share the same authorized scope and bounded candidate budget.
// Run independent SQL/lexical work while the vector backend embeds the query.
// Per-channel wall times overlap and must not be added to estimate total latency.
func (e *Engine) recallDirect(ctx context.Context, input domain.SearchInput, version int, model string, timings *SearchTimings) ([]domain.KeywordMatch, []domain.VectorSearchResult, error) {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	var keywords []domain.KeywordMatch
	var vectors []domain.VectorSearchResult
	var keywordErr, vectorErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		start := time.Now()
		defer func() { timings.KeywordMillis = float64(time.Since(start).Microseconds()) / 1000 }()
		keywords, keywordErr = e.repo.SearchKeywordMatches(child, input.UserID, input.GenerationID, strings.TrimSpace(input.Query), e.candidateRecallLimit(input.TopN), repoSearchTypes(input)...)
		if keywordErr != nil {
			cancel()
		}
	})
	if e.vectors != nil {
		wg.Go(func() {
			start := time.Now()
			defer func() { timings.VectorMillis = float64(time.Since(start).Microseconds()) / 1000 }()
			vectors, vectorErr = e.vectors.Search(child, domain.VectorSearchInput{WorkspaceID: input.WorkspaceID, UserID: input.UserID, GenerationID: input.GenerationID, Query: strings.TrimSpace(input.Query), TopK: e.candidateRecallLimit(input.TopN), AllowedTypes: vectorAllowedTypes(input), EmbeddingVersion: version, EmbeddingModel: model})
			if errors.Is(vectorErr, domain.ErrAccessDenied) {
				cancel()
			}
		})
	}
	wg.Wait()
	if errors.Is(vectorErr, domain.ErrAccessDenied) {
		return nil, nil, vectorErr
	}
	if keywordErr != nil {
		return nil, nil, keywordErr
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	// Preserve the existing hybrid fallback for a non-authority vector failure.
	// Evaluation observes provider errors separately and never counts it as a pass.
	if vectorErr != nil {
		vectors = nil
	}
	return keywords, vectors, nil
}
