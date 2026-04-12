package runtimeconfig

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"sync"

	"memstation/apps/server/internal/bootstrap"
)

type DBHandle interface {
	Current() *sql.DB
}

type StaticDBHandle struct {
	db *sql.DB
}

func NewStaticDBHandle(db *sql.DB) *StaticDBHandle {
	return &StaticDBHandle{db: db}
}

func (h *StaticDBHandle) Current() *sql.DB {
	if h == nil {
		return nil
	}
	return h.db
}

type PostConnectHook func(context.Context, *sql.DB) error

type SwitchableDB struct {
	logger      *slog.Logger
	afterConnect PostConnectHook

	mu       sync.RWMutex
	current  *sql.DB
	url      string
	lastErr  error
}

func NewSwitchableDB(logger *slog.Logger, afterConnect PostConnectHook) *SwitchableDB {
	return &SwitchableDB{
		logger:       logger,
		afterConnect: afterConnect,
	}
}

func (h *SwitchableDB) Current() *sql.DB {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.current
}

func (h *SwitchableDB) CurrentURL() string {
	if h == nil {
		return ""
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.url
}

func (h *SwitchableDB) LastError() error {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastErr
}

func (h *SwitchableDB) ApplyURL(ctx context.Context, rawURL string) error {
	if h == nil {
		return nil
	}

	url := strings.TrimSpace(rawURL)

	h.mu.RLock()
	current := h.current
	currentURL := h.url
	h.mu.RUnlock()

	if url == "" {
		h.swap(nil, "", nil)
		if current != nil {
			_ = current.Close()
		}
		return nil
	}

	if current != nil && currentURL == url {
		h.swap(current, url, nil)
		return nil
	}

	db, err := bootstrap.OpenPostgres(ctx, bootstrap.Config{DatabaseURL: url})
	if err != nil {
		h.setError(err)
		return err
	}
	if h.afterConnect != nil {
		if err := h.afterConnect(ctx, db); err != nil {
			_ = db.Close()
			h.setError(err)
			return err
		}
	}

	old := h.swap(db, url, nil)
	if old != nil && old != db {
		_ = old.Close()
	}
	return nil
}

func (h *SwitchableDB) Close() error {
	if h == nil {
		return nil
	}
	old := h.swap(nil, "", nil)
	if old != nil {
		return old.Close()
	}
	return nil
}

func (h *SwitchableDB) Health(ctx context.Context) error {
	db := h.Current()
	if db == nil {
		return nil
	}
	return db.PingContext(ctx)
}

func (h *SwitchableDB) setError(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastErr = err
	if err != nil && h.logger != nil {
		h.logger.Warn("database handle update failed", "error", err)
	}
}

func (h *SwitchableDB) swap(db *sql.DB, url string, err error) *sql.DB {
	h.mu.Lock()
	defer h.mu.Unlock()
	previous := h.current
	h.current = db
	h.url = url
	h.lastErr = err
	return previous
}
