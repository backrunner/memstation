package cache

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type cachedSettings struct {
	ChatModel string `json:"chat_model"`
}

func TestJSONStoreRetainsLocalValueAfterSet(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	store, err := NewJSONStore(ctx, client, time.Minute)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer func() { _ = store.Close(context.Background()) }()

	if err := store.Set(ctx, "settings:user:user-1", cachedSettings{ChatModel: "gpt-5"}); err != nil {
		t.Fatalf("set cache: %v", err)
	}
	if err := client.Del(ctx, "settings:user:user-1").Err(); err != nil {
		t.Fatalf("delete redis key: %v", err)
	}

	var snapshot cachedSettings
	ok, err := store.Get(ctx, "settings:user:user-1", &snapshot)
	if err != nil {
		t.Fatalf("get cached value: %v", err)
	}
	if !ok {
		t.Fatal("expected l1 cache hit after redis key deletion")
	}
	if snapshot.ChatModel != "gpt-5" {
		t.Fatalf("expected cached chat model gpt-5, got %q", snapshot.ChatModel)
	}
}

func TestJSONStoreRemoteInvalidation(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	clientOne := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = clientOne.Close() }()
	clientTwo := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = clientTwo.Close() }()

	ctx := context.Background()
	storeOne, err := NewJSONStore(ctx, clientOne, time.Minute)
	if err != nil {
		t.Fatalf("new first store: %v", err)
	}
	defer func() { _ = storeOne.Close(context.Background()) }()

	storeTwo, err := NewJSONStore(ctx, clientTwo, time.Minute)
	if err != nil {
		t.Fatalf("new second store: %v", err)
	}
	defer func() { _ = storeTwo.Close(context.Background()) }()

	if err := storeOne.Set(ctx, "settings:user:user-1", cachedSettings{ChatModel: "gpt-5-mini"}); err != nil {
		t.Fatalf("set initial value: %v", err)
	}

	var initial cachedSettings
	ok, err := storeTwo.Get(ctx, "settings:user:user-1", &initial)
	if err != nil {
		t.Fatalf("warm second store: %v", err)
	}
	if !ok || initial.ChatModel != "gpt-5-mini" {
		t.Fatalf("expected warm cache hit with gpt-5-mini, got ok=%v value=%q", ok, initial.ChatModel)
	}

	if err := storeOne.Set(ctx, "settings:user:user-1", cachedSettings{ChatModel: "claude-sonnet"}); err != nil {
		t.Fatalf("set updated value: %v", err)
	}

	eventually(t, func() bool {
		var updated cachedSettings
		ok, err := storeTwo.Get(ctx, "settings:user:user-1", &updated)
		return err == nil && ok && updated.ChatModel == "claude-sonnet"
	})
}

func TestJSONStoreDeletePrefixInvalidatesOtherStores(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	clientOne := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = clientOne.Close() }()
	clientTwo := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = clientTwo.Close() }()

	ctx := context.Background()
	storeOne, err := NewJSONStore(ctx, clientOne, time.Minute)
	if err != nil {
		t.Fatalf("new first store: %v", err)
	}
	defer func() { _ = storeOne.Close(context.Background()) }()

	storeTwo, err := NewJSONStore(ctx, clientTwo, time.Minute)
	if err != nil {
		t.Fatalf("new second store: %v", err)
	}
	defer func() { _ = storeTwo.Close(context.Background()) }()

	const prefix = "memory:keymemories:user:user-1:"
	keys := []string{
		prefix + "limit:1",
		prefix + "limit:50",
	}
	for _, key := range keys {
		if err := storeOne.Set(ctx, key, []string{"remember this"}); err != nil {
			t.Fatalf("seed key %q: %v", key, err)
		}
		var cached []string
		ok, err := storeTwo.Get(ctx, key, &cached)
		if err != nil {
			t.Fatalf("warm key %q: %v", key, err)
		}
		if !ok || len(cached) != 1 {
			t.Fatalf("expected warm cache hit for %q, got ok=%v len=%d", key, ok, len(cached))
		}
	}

	if err := storeOne.DeletePrefix(ctx, prefix); err != nil {
		t.Fatalf("delete prefix: %v", err)
	}

	eventually(t, func() bool {
		for _, key := range keys {
			var cached []string
			ok, err := storeTwo.Get(ctx, key, &cached)
			if err != nil || ok {
				return false
			}
		}
		return true
	})
}

func TestJSONStoreDeleteInvalidatesOtherStores(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	clientOne := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = clientOne.Close() }()
	clientTwo := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = clientTwo.Close() }()

	ctx := context.Background()
	storeOne, err := NewJSONStore(ctx, clientOne, time.Minute)
	if err != nil {
		t.Fatalf("new first store: %v", err)
	}
	defer func() { _ = storeOne.Close(context.Background()) }()

	storeTwo, err := NewJSONStore(ctx, clientTwo, time.Minute)
	if err != nil {
		t.Fatalf("new second store: %v", err)
	}
	defer func() { _ = storeTwo.Close(context.Background()) }()

	if err := storeOne.Set(ctx, "settings:user:user-2", cachedSettings{ChatModel: "gpt-4.1"}); err != nil {
		t.Fatalf("seed key: %v", err)
	}

	var cached cachedSettings
	ok, err := storeTwo.Get(ctx, "settings:user:user-2", &cached)
	if err != nil {
		t.Fatalf("warm second store: %v", err)
	}
	if !ok || cached.ChatModel != "gpt-4.1" {
		t.Fatalf("expected warm cache hit, got ok=%v value=%q", ok, cached.ChatModel)
	}

	if err := storeOne.Delete(ctx, "settings:user:user-2"); err != nil {
		t.Fatalf("delete key: %v", err)
	}

	eventually(t, func() bool {
		var updated cachedSettings
		ok, err := storeTwo.Get(ctx, "settings:user:user-2", &updated)
		return err == nil && !ok
	})
}

func eventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
