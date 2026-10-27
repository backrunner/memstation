package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

type directRecallRepository struct {
	ports.MemoryRepository
	search func(context.Context) ([]domain.KeywordMatch, error)
}

func (r directRecallRepository) SearchKeywordMatches(ctx context.Context, _, _, _ string, _ int, _ ...domain.MemoryType) ([]domain.KeywordMatch, error) {
	return r.search(ctx)
}

type directRecallVector struct {
	ports.VectorStore
	search func(context.Context) ([]domain.VectorSearchResult, error)
}

func (v directRecallVector) Search(ctx context.Context, _ domain.VectorSearchInput) ([]domain.VectorSearchResult, error) {
	return v.search(ctx)
}

func TestDirectRecallChannelsStartConcurrentlyAndKeepBothSignals(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	repo := directRecallRepository{search: func(ctx context.Context) ([]domain.KeywordMatch, error) {
		entered <- "keyword"
		select {
		case <-release:
			return []domain.KeywordMatch{{NodeID: "lexical", Score: 1}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	vectors := directRecallVector{search: func(ctx context.Context) ([]domain.VectorSearchResult, error) {
		entered <- "vector"
		select {
		case <-release:
			return []domain.VectorSearchResult{{NodeID: "dense", Score: 1}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	engine := NewEngine(repo, vectors)
	done := make(chan error, 1)
	go func() {
		k, v, err := engine.recallDirect(context.Background(), domain.SearchInput{UserID: "owner", TopN: 10}, 1, "model", &SearchTimings{})
		if err == nil && (len(k) != 1 || len(v) != 1 || k[0].NodeID != "lexical" || v[0].NodeID != "dense") {
			err = errors.New("direct signal lost")
		}
		done <- err
	}()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("independent recall channels were serialized")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDirectRecallAccessDenialCancelsOtherChannelAndFailsClosed(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	repo := directRecallRepository{search: func(ctx context.Context) ([]domain.KeywordMatch, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}}
	vectors := directRecallVector{search: func(context.Context) ([]domain.VectorSearchResult, error) {
		<-entered
		return nil, domain.ErrAccessDenied
	}}
	engine := NewEngine(repo, vectors)
	_, _, err := engine.recallDirect(context.Background(), domain.SearchInput{UserID: "owner", TopN: 10}, 1, "model", &SearchTimings{})
	if !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("authority failure masked", err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("authority failure did not cancel outstanding recall")
	}
}
