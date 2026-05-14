package runtimeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const stateVersion = 1

type State struct {
	Version     int                            `json:"version"`
	System      map[string]json.RawMessage     `json:"system,omitempty"`
	Providers   map[string]ProviderRecordState `json:"providers,omitempty"`
	Connections ConnectionState                `json:"connections,omitempty"`
}

type ProviderRecordState struct {
	Scope           string    `json:"scope"`
	Provider        string    `json:"provider"`
	BaseURL         string    `json:"base_url,omitempty"`
	EncryptedAPIKey string    `json:"encrypted_api_key,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

type ConnectionState struct {
	Database DatabaseState `json:"database,omitempty"`
}

type DatabaseState struct {
	EncryptedURL string    `json:"encrypted_url,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

type FileStore struct {
	path string
	mu   sync.Mutex
}

func NewFileStore(path string) *FileStore {
	return &FileStore{path: filepath.Clean(path)}
}

func (s *FileStore) Load(_ context.Context) (State, error) {
	if s == nil || s.path == "" {
		return newState(), nil
	}

	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newState(), nil
		}
		return State{}, err
	}
	if len(raw) == 0 {
		return newState(), nil
	}

	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		return State{}, err
	}
	return normalizeState(state), nil
}

func (s *FileStore) Update(ctx context.Context, mutator func(*State) error) (State, error) {
	if s == nil || s.path == "" {
		state := newState()
		if mutator != nil {
			if err := mutator(&state); err != nil {
				return State{}, err
			}
		}
		return normalizeState(state), nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.Load(ctx)
	if err != nil {
		return State{}, err
	}
	if mutator != nil {
		if err := mutator(&state); err != nil {
			return State{}, err
		}
	}
	state = normalizeState(state)

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return State{}, err
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return State{}, err
	}
	tempPath := s.path + ".tmp"
	if err := os.WriteFile(tempPath, payload, 0o600); err != nil {
		return State{}, err
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return State{}, err
	}
	return state, nil
}

func newState() State {
	return State{
		Version:     stateVersion,
		System:      map[string]json.RawMessage{},
		Providers:   map[string]ProviderRecordState{},
		Connections: ConnectionState{},
	}
}

func normalizeState(state State) State {
	if state.Version <= 0 {
		state.Version = stateVersion
	}
	if state.System == nil {
		state.System = map[string]json.RawMessage{}
	}
	if state.Providers == nil {
		state.Providers = map[string]ProviderRecordState{}
	}
	return state
}
