package cache

import (
	"context"
	"strings"
	"sync"
	"time"
)

type item struct {
	value     any
	expiresAt time.Time
}

type L1Cache struct {
	mu    sync.RWMutex
	ttl   time.Duration
	items map[string]item
}

func NewL1Cache(ttl time.Duration) *L1Cache {
	return &L1Cache{ttl: ttl, items: map[string]item{}}
}

func (c *L1Cache) Get(_ context.Context, key string) (any, bool) {
	c.mu.RLock()
	entry, ok := c.items[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.value, true
}

func (c *L1Cache) Set(_ context.Context, key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = item{value: value, expiresAt: time.Now().Add(c.ttl)}
}

func (c *L1Cache) Delete(_ context.Context, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

func (c *L1Cache) DeletePrefix(_ context.Context, prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.items {
		if prefix == "" || strings.HasPrefix(key, prefix) {
			delete(c.items, key)
		}
	}
}
