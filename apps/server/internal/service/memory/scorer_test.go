package memory

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/memoryllm"
	sqlstore "memstation/apps/server/internal/store/sql"
)

type scoreGenerator func(context.Context, memoryllm.Request) (json.RawMessage, error)

func (f scoreGenerator) Generate(c context.Context, r memoryllm.Request) (json.RawMessage, error) {
	return f(c, r)
}

func TestScoreFailuresNeverProduceHeuristicValuesOrWrites(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		err       error
	}{
		{name: "unavailable", err: errors.New("model unavailable")},
		{name: "invalid JSON", raw: `not JSON`},
		{name: "missing fields", raw: `{}`},
		{name: "out of range", raw: `{"importance":5,"decay_factor":1,"activity_at":"2026-10-24T00:00:00Z","key_memory_candidate":true,"reason":"model"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scorer := &Scorer{generator: scoreGenerator(func(context.Context, memoryllm.Request) (json.RawMessage, error) {
				return json.RawMessage(tc.raw), tc.err
			})}
			repo := sqlstore.NewInMemoryMemoryRepository()
			service := NewService(nil, repo)
			service.SetScorer(scorer)
			if _, err := service.UpsertKeyMemory(context.Background(), domain.KeyMemory{UserID: "owner", Key: "preferences", Value: "I strongly prefer this permanently"}); err == nil {
				t.Fatal("model failure ignored")
			}
			keys, err := repo.ListKeyMemories(context.Background(), "owner", 10)
			if err != nil || len(keys) != 0 {
				t.Fatal("failed scoring wrote memory", keys, err)
			}
		})
	}
}
