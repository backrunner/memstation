package cache

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const invalidationChannel = "memstation:cache:invalidate"

type invalidation struct {
	Source string `json:"source"`
	Key    string `json:"key,omitempty"`
	Prefix string `json:"prefix,omitempty"`
}

type JSONStore struct {
	id     string
	l1     *L1Cache
	redis  *redis.Client
	pubsub *redis.PubSub
	ttlMu  sync.RWMutex
	ttl    time.Duration
	cancel context.CancelFunc
	done   chan struct{}
}

func NewJSONStore(ctx context.Context, redisClient *redis.Client, ttl time.Duration) (*JSONStore, error) {
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}

	cache := &JSONStore{
		id:    uuid.NewString(),
		l1:    NewL1Cache(ttl),
		redis: redisClient,
		ttl:   ttl,
		done:  make(chan struct{}),
	}

	if redisClient == nil {
		close(cache.done)
		return cache, nil
	}

	listenCtx, cancel := context.WithCancel(ctx)
	pubsub := redisClient.Subscribe(listenCtx, invalidationChannel)
	if _, err := pubsub.Receive(listenCtx); err != nil {
		cancel()
		_ = pubsub.Close()
		close(cache.done)
		return nil, err
	}

	cache.cancel = cancel
	cache.pubsub = pubsub

	go cache.listenInvalidations()
	return cache, nil
}

func (c *JSONStore) Get(ctx context.Context, key string, target any) (bool, error) {
	if raw, ok := c.l1.Get(ctx, key); ok {
		payload, ok := raw.([]byte)
		if ok {
			return true, json.Unmarshal(cloneBytes(payload), target)
		}
	}

	if c.redis == nil {
		return false, nil
	}

	payload, err := c.redis.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	c.l1.Set(ctx, key, cloneBytes(payload))
	return true, json.Unmarshal(payload, target)
}

func (c *JSONStore) Set(ctx context.Context, key string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.l1.Set(ctx, key, cloneBytes(payload))
	if c.redis == nil {
		return nil
	}
	if err := c.redis.Set(ctx, key, payload, c.TTL()).Err(); err != nil {
		return err
	}
	return c.publish(ctx, invalidation{Source: c.id, Key: key})
}

func (c *JSONStore) TTL() time.Duration {
	if c == nil {
		return 0
	}
	c.ttlMu.RLock()
	defer c.ttlMu.RUnlock()
	return c.ttl
}

func (c *JSONStore) UpdateTTL(ttl time.Duration) {
	if c == nil {
		return
	}
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	c.ttlMu.Lock()
	c.ttl = ttl
	c.ttlMu.Unlock()
	if c.l1 != nil {
		c.l1.UpdateTTL(ttl)
	}
}

func (c *JSONStore) Delete(ctx context.Context, key string) error {
	c.l1.Delete(ctx, key)
	if c.redis == nil {
		return nil
	}
	if err := c.redis.Del(ctx, key).Err(); err != nil {
		return err
	}
	return c.publish(ctx, invalidation{Source: c.id, Key: key})
}

func (c *JSONStore) DeletePrefix(ctx context.Context, prefix string) error {
	c.l1.DeletePrefix(ctx, prefix)
	if c.redis == nil {
		return nil
	}
	if err := c.deleteRedisPrefix(ctx, prefix); err != nil {
		return err
	}
	return c.publish(ctx, invalidation{Source: c.id, Prefix: prefix})
}

func (c *JSONStore) Close(ctx context.Context) error {
	if c.cancel != nil {
		c.cancel()
	}
	if c.pubsub != nil {
		_ = c.pubsub.Close()
	}
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *JSONStore) listenInvalidations() {
	defer close(c.done)

	channel := c.pubsub.Channel()
	for message := range channel {
		var event invalidation
		if err := json.Unmarshal([]byte(message.Payload), &event); err != nil {
			continue
		}
		if event.Source == c.id {
			continue
		}
		switch {
		case event.Prefix != "":
			c.l1.DeletePrefix(context.Background(), event.Prefix)
		case event.Key != "":
			c.l1.Delete(context.Background(), event.Key)
		}
	}
}

func (c *JSONStore) deleteRedisPrefix(ctx context.Context, prefix string) error {
	var cursor uint64
	for {
		keys, next, err := c.redis.Scan(ctx, cursor, prefix+"*", 100).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := c.redis.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

func (c *JSONStore) publish(ctx context.Context, event invalidation) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return c.redis.Publish(ctx, invalidationChannel, payload).Err()
}

func cloneBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
