package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

type Registry struct {
	adapters        map[domain.Provider]ports.ProviderAdapter
	defaultProvider domain.Provider
}

type RuntimeConfig struct {
	APIKey  string
	BaseURL string
}

type ConfigResolver interface {
	Resolve(ctx context.Context, provider domain.Provider) (RuntimeConfig, error)
}

type StaticConfigResolver map[domain.Provider]RuntimeConfig

func (r StaticConfigResolver) Resolve(_ context.Context, provider domain.Provider) (RuntimeConfig, error) {
	if cfg, ok := r[provider]; ok {
		return cfg, nil
	}
	return RuntimeConfig{}, fmt.Errorf("provider config unavailable: %s", provider)
}

func NewRegistry(defaultProvider domain.Provider, adapters ...ports.ProviderAdapter) *Registry {
	registry := &Registry{
		adapters:        map[domain.Provider]ports.ProviderAdapter{},
		defaultProvider: defaultProvider,
	}
	for _, adapter := range adapters {
		registry.adapters[adapter.Name()] = adapter
	}
	return registry
}

func (r *Registry) Resolve(explicit domain.Provider) (ports.ProviderAdapter, error) {
	provider := explicit
	if provider == "" {
		provider = r.defaultProvider
	}
	adapter, ok := r.adapters[provider]
	if !ok {
		return nil, fmt.Errorf("provider adapter unavailable: %s", provider)
	}
	return adapter, nil
}

func FlattenMessages(messages []domain.UnifiedMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		text := messageText(message)
		if text == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", message.Role, text))
	}
	return strings.Join(parts, "\n")
}

func MessageText(message domain.UnifiedMessage) string {
	return messageText(message)
}

func SplitMessages(messages []domain.UnifiedMessage) (system []string, chat []domain.UnifiedMessage) {
	system = make([]string, 0)
	chat = make([]domain.UnifiedMessage, 0, len(messages))
	for _, message := range messages {
		if message.Role == domain.RoleSystem {
			if text := messageText(message); text != "" {
				system = append(system, text)
			}
			continue
		}
		chat = append(chat, message)
	}
	return system, chat
}

type SSEEvent struct {
	Event string
	Data  string
}

func ReadSSE(reader io.Reader, fn func(SSEEvent) error) error {
	buffered := bufio.NewReader(reader)
	current := SSEEvent{}

	emit := func() error {
		if current.Event == "" && current.Data == "" {
			return nil
		}
		current.Data = strings.TrimSuffix(current.Data, "\n")
		if err := fn(current); err != nil {
			return err
		}
		current = SSEEvent{}
		return nil
	}

	for {
		line, err := buffered.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if emitErr := emit(); emitErr != nil {
				return emitErr
			}
		} else if !strings.HasPrefix(line, ":") {
			field := line
			value := ""
			if idx := strings.IndexByte(line, ':'); idx >= 0 {
				field = line[:idx]
				value = strings.TrimLeft(line[idx+1:], " ")
			}
			switch field {
			case "event":
				current.Event = value
			case "data":
				current.Data += value + "\n"
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return emit()
}

func JoinSystem(system []string) string {
	return strings.TrimSpace(strings.Join(system, "\n\n"))
}

func messageText(message domain.UnifiedMessage) string {
	if message.Text != "" {
		return message.Text
	}
	parts := make([]string, 0, len(message.Blocks))
	for _, block := range message.Blocks {
		switch block.Type {
		case domain.ContentBlockText:
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		default:
			if block.Raw != "" {
				parts = append(parts, block.Raw)
			}
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	if len(message.ToolResults) > 0 {
		raw, _ := json.Marshal(message.ToolResults)
		return string(raw)
	}
	return ""
}
