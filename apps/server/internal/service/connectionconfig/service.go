package connectionconfig

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/runtimeconfig"
)

type DatabaseSnapshot struct {
	Configured bool       `json:"configured"`
	Source     string     `json:"source"`
	Status     string     `json:"status"`
	Display    string     `json:"display,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

type Snapshot struct {
	Database DatabaseSnapshot `json:"database"`
}

type UpdateInput struct {
	URL   *string `json:"url,omitempty"`
	Clear bool    `json:"clear,omitempty"`
}

type Service struct {
	store      *runtimeconfig.FileStore
	encryptor  ports.Encryptor
	dbHandle   *runtimeconfig.SwitchableDB
	defaultURL string

	mu          sync.RWMutex
	overrideURL string
	updatedAt   *time.Time
	lastError   string
}

func NewService(store *runtimeconfig.FileStore, encryptor ports.Encryptor, dbHandle *runtimeconfig.SwitchableDB, defaultURL string) *Service {
	return &Service{
		store:      store,
		encryptor:  encryptor,
		dbHandle:   dbHandle,
		defaultURL: strings.TrimSpace(defaultURL),
	}
}

func (s *Service) Bootstrap(ctx context.Context) (Snapshot, string, error) {
	overrideURL, updatedAt, err := s.loadOverride(ctx)
	if err != nil {
		return Snapshot{}, "", err
	}

	s.mu.Lock()
	s.overrideURL = overrideURL
	s.updatedAt = updatedAt
	s.lastError = ""
	s.mu.Unlock()

	effectiveURL := s.effectiveURLLocked()
	if s.dbHandle != nil {
		if err := s.dbHandle.ApplyURL(ctx, effectiveURL); err != nil {
			s.mu.Lock()
			s.lastError = err.Error()
			s.mu.Unlock()
		}
	}

	return s.Snapshot(), effectiveURL, nil
}

func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	effectiveURL := s.effectiveURLLocked()
	source := "unset"
	switch {
	case strings.TrimSpace(s.overrideURL) != "":
		source = "override"
	case strings.TrimSpace(s.defaultURL) != "":
		source = "env"
	}

	status := "inactive"
	if effectiveURL != "" {
		status = "ready"
	}
	lastError := strings.TrimSpace(s.lastError)
	if lastError == "" && s.dbHandle != nil && s.dbHandle.LastError() != nil {
		lastError = s.dbHandle.LastError().Error()
	}
	if lastError != "" {
		status = "degraded"
	}

	return Snapshot{
		Database: DatabaseSnapshot{
			Configured: effectiveURL != "",
			Source:     source,
			Status:     status,
			Display:    maskDatabaseURL(effectiveURL),
			LastError:  lastError,
			UpdatedAt:  cloneTimePtr(s.updatedAt),
		},
	}
}

func (s *Service) Update(ctx context.Context, input UpdateInput) (Snapshot, error) {
	if s == nil {
		return Snapshot{}, nil
	}

	s.mu.RLock()
	currentOverride := s.overrideURL
	currentUpdatedAt := cloneTimePtr(s.updatedAt)
	currentError := s.lastError
	s.mu.RUnlock()

	nextOverride := currentOverride
	nextUpdatedAt := currentUpdatedAt

	switch {
	case input.Clear:
		nextOverride = ""
		nextUpdatedAt = nil
	case input.URL != nil:
		value := strings.TrimSpace(*input.URL)
		if value == "" {
			return Snapshot{}, errors.New("database url is required")
		}
		nextOverride = value
		now := time.Now().UTC()
		nextUpdatedAt = &now
	default:
		return s.Snapshot(), nil
	}

	effectiveURL := strings.TrimSpace(nextOverride)
	if effectiveURL == "" {
		effectiveURL = strings.TrimSpace(s.defaultURL)
	}

	if s.dbHandle != nil {
		if err := s.dbHandle.ApplyURL(ctx, effectiveURL); err != nil {
			return Snapshot{}, err
		}
	}

	if _, err := s.persistOverride(ctx, nextOverride, nextUpdatedAt); err != nil {
		if s.dbHandle != nil {
			_ = s.dbHandle.ApplyURL(ctx, firstNonEmpty(currentOverride, s.defaultURL))
		}
		s.mu.Lock()
		s.lastError = currentError
		s.mu.Unlock()
		return Snapshot{}, err
	}

	s.mu.Lock()
	s.overrideURL = nextOverride
	s.updatedAt = nextUpdatedAt
	s.lastError = ""
	s.mu.Unlock()

	return s.Snapshot(), nil
}

func (s *Service) loadOverride(ctx context.Context) (string, *time.Time, error) {
	if s == nil || s.store == nil {
		return "", nil, nil
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return "", nil, err
	}
	encrypted := strings.TrimSpace(state.Connections.Database.EncryptedURL)
	if encrypted == "" {
		return "", cloneTime(state.Connections.Database.UpdatedAt), nil
	}
	if s.encryptor == nil {
		return "", nil, errors.New("connection config encryptor is not configured")
	}
	plaintext, err := s.encryptor.Decrypt(ctx, encrypted)
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(string(plaintext)), cloneTime(state.Connections.Database.UpdatedAt), nil
}

func (s *Service) persistOverride(ctx context.Context, overrideURL string, updatedAt *time.Time) (runtimeconfig.State, error) {
	if s == nil || s.store == nil {
		return runtimeconfig.State{}, nil
	}
	encrypted := ""
	if strings.TrimSpace(overrideURL) != "" {
		if s.encryptor == nil {
			return runtimeconfig.State{}, errors.New("connection config encryptor is not configured")
		}
		ciphertext, err := s.encryptor.Encrypt(ctx, []byte(strings.TrimSpace(overrideURL)))
		if err != nil {
			return runtimeconfig.State{}, err
		}
		encrypted = ciphertext
	}
	return s.store.Update(ctx, func(state *runtimeconfig.State) error {
		state.Connections.Database.EncryptedURL = encrypted
		if updatedAt != nil {
			state.Connections.Database.UpdatedAt = updatedAt.UTC()
		} else {
			state.Connections.Database.UpdatedAt = time.Time{}
		}
		return nil
	})
}

func (s *Service) effectiveURLLocked() string {
	if strings.TrimSpace(s.overrideURL) != "" {
		return strings.TrimSpace(s.overrideURL)
	}
	return strings.TrimSpace(s.defaultURL)
}

func maskDatabaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "[configured]"
	}
	if parsed.User != nil {
		username := parsed.User.Username()
		if _, ok := parsed.User.Password(); ok {
			parsed.User = url.UserPassword(username, "****")
		}
	}
	return parsed.String()
}

func cloneTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copied := value.UTC()
	return &copied
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := value.UTC()
	return &copied
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
