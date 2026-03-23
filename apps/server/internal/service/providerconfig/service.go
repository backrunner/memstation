package providerconfig

import (
	"context"
	sqlpkg "database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/provider"
)

type Summary struct {
	Provider  domain.Provider `json:"provider"`
	BaseURL   string          `json:"base_url"`
	HasAPIKey bool            `json:"has_api_key"`
	Source    string          `json:"source"`
	UpdatedAt *time.Time      `json:"updated_at,omitempty"`
}

type Diagnostic struct {
	Provider              domain.Provider `json:"provider"`
	BaseURL               string          `json:"base_url"`
	Host                  string          `json:"host,omitempty"`
	Scheme                string          `json:"scheme,omitempty"`
	HasAPIKey             bool            `json:"has_api_key"`
	Source                string          `json:"source"`
	UpdatedAt             *time.Time      `json:"updated_at,omitempty"`
	IsDesiredDefault      bool            `json:"is_desired_default"`
	IsLiveDefault         bool            `json:"is_live_default"`
	DesiredTimeoutSeconds int             `json:"desired_timeout_seconds"`
	LiveTimeoutSeconds    int             `json:"live_timeout_seconds"`
	RestartRequired       bool            `json:"restart_required"`
	Status                string          `json:"status"`
	Issues                []string        `json:"issues"`
}

type UpdateInput struct {
	APIKey  *string `json:"api_key"`
	BaseURL *string `json:"base_url"`
}

type record struct {
	EncryptedAPIKey string
	BaseURL         string
	UpdatedAt       time.Time
}

type Service struct {
	db        *sqlpkg.DB
	encryptor ports.Encryptor

	mu       sync.RWMutex
	defaults map[domain.Provider]provider.RuntimeConfig
	records  map[domain.Provider]record
}

func NewService(db *sqlpkg.DB, encryptor ports.Encryptor, defaults map[domain.Provider]provider.RuntimeConfig) *Service {
	copiedDefaults := make(map[domain.Provider]provider.RuntimeConfig, len(defaults))
	for key, value := range defaults {
		copiedDefaults[key] = value
	}
	return &Service{
		db:        db,
		encryptor: encryptor,
		defaults:  copiedDefaults,
		records:   map[domain.Provider]record{},
	}
}

func (s *Service) Resolve(ctx context.Context, providerName domain.Provider) (provider.RuntimeConfig, error) {
	defaults := s.defaultConfig(providerName)
	if s.db == nil {
		s.mu.RLock()
		record, ok := s.records[providerName]
		s.mu.RUnlock()
		if !ok {
			return defaults, nil
		}
		return s.mergeWithRecord(ctx, defaults, record)
	}

	row := s.db.QueryRowContext(ctx, `
        SELECT encrypted_api_key, base_url, updated_at
        FROM provider_credentials
        WHERE provider = $1
    `, string(providerName))
	var encryptedAPIKey sqlpkg.NullString
	var baseURL sqlpkg.NullString
	var updatedAt sqlpkg.NullTime
	if err := row.Scan(&encryptedAPIKey, &baseURL, &updatedAt); err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return defaults, nil
		}
		return provider.RuntimeConfig{}, err
	}
	record := record{}
	if encryptedAPIKey.Valid {
		record.EncryptedAPIKey = encryptedAPIKey.String
	}
	if baseURL.Valid {
		record.BaseURL = baseURL.String
	}
	if updatedAt.Valid {
		record.UpdatedAt = updatedAt.Time
	}
	return s.mergeWithRecord(ctx, defaults, record)
}

func (s *Service) List(ctx context.Context) ([]Summary, error) {
	providers := []domain.Provider{domain.ProviderOpenAI, domain.ProviderAnthropic, domain.ProviderGemini}
	out := make([]Summary, 0, len(providers))
	for _, providerName := range providers {
		summary, err := s.summaryForProvider(ctx, providerName)
		if err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return out, nil
}

func (s *Service) Diagnostics(ctx context.Context, desiredDefault, liveDefault domain.Provider, desiredTimeoutSeconds, liveTimeoutSeconds int) ([]Diagnostic, error) {
	providers := []domain.Provider{domain.ProviderOpenAI, domain.ProviderAnthropic, domain.ProviderGemini}
	out := make([]Diagnostic, 0, len(providers))
	for _, providerName := range providers {
		item, err := s.diagnosticForProvider(ctx, providerName, desiredDefault, liveDefault, desiredTimeoutSeconds, liveTimeoutSeconds)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) Update(ctx context.Context, providerName domain.Provider, input UpdateInput) (Summary, error) {
	current, err := s.Resolve(ctx, providerName)
	if err != nil {
		return Summary{}, err
	}

	next := current
	if input.APIKey != nil {
		next.APIKey = *input.APIKey
	}
	if input.BaseURL != nil {
		next.BaseURL = *input.BaseURL
	}

	encryptedAPIKey := ""
	if next.APIKey != "" && s.encryptor != nil {
		ciphertext, err := s.encryptor.Encrypt(ctx, []byte(next.APIKey))
		if err != nil {
			return Summary{}, err
		}
		encryptedAPIKey = ciphertext
	}

	now := time.Now().UTC()
	if s.db == nil {
		s.mu.Lock()
		s.records[providerName] = record{
			EncryptedAPIKey: encryptedAPIKey,
			BaseURL:         next.BaseURL,
			UpdatedAt:       now,
		}
		s.mu.Unlock()
		summary, err := s.summaryForProvider(ctx, providerName)
		if err != nil {
			return Summary{}, err
		}
		return summary, nil
	}

	_, err = s.db.ExecContext(ctx, `
        INSERT INTO provider_credentials (provider, encrypted_api_key, base_url, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$4)
        ON CONFLICT (provider) DO UPDATE SET
            encrypted_api_key = EXCLUDED.encrypted_api_key,
            base_url = EXCLUDED.base_url,
            updated_at = EXCLUDED.updated_at
    `, string(providerName), nullString(encryptedAPIKey), nullString(next.BaseURL), now)
	if err != nil {
		return Summary{}, err
	}
	return s.summaryForProvider(ctx, providerName)
}

func (s *Service) summaryForProvider(ctx context.Context, providerName domain.Provider) (Summary, error) {
	cfg, err := s.Resolve(ctx, providerName)
	if err != nil {
		return Summary{}, err
	}
	summary := Summary{
		Provider:  providerName,
		BaseURL:   cfg.BaseURL,
		HasAPIKey: cfg.APIKey != "",
		Source:    "default",
	}
	defaults := s.defaultConfig(providerName)
	if cfg.APIKey != defaults.APIKey || cfg.BaseURL != defaults.BaseURL {
		summary.Source = "override"
	}

	if s.db == nil {
		s.mu.RLock()
		record, ok := s.records[providerName]
		s.mu.RUnlock()
		if ok && !record.UpdatedAt.IsZero() {
			summary.Source = "override"
			summary.UpdatedAt = &record.UpdatedAt
		}
		return summary, nil
	}

	row := s.db.QueryRowContext(ctx, `SELECT updated_at FROM provider_credentials WHERE provider = $1`, string(providerName))
	var updatedAt sqlpkg.NullTime
	if err := row.Scan(&updatedAt); err == nil && updatedAt.Valid {
		summary.Source = "override"
		summary.UpdatedAt = &updatedAt.Time
	}
	return summary, nil
}

func (s *Service) diagnosticForProvider(ctx context.Context, providerName domain.Provider, desiredDefault, liveDefault domain.Provider, desiredTimeoutSeconds, liveTimeoutSeconds int) (Diagnostic, error) {
	summary, err := s.summaryForProvider(ctx, providerName)
	if err != nil {
		return Diagnostic{}, err
	}

	item := Diagnostic{
		Provider:              summary.Provider,
		BaseURL:               summary.BaseURL,
		HasAPIKey:             summary.HasAPIKey,
		Source:                summary.Source,
		UpdatedAt:             summary.UpdatedAt,
		IsDesiredDefault:      providerName == desiredDefault,
		IsLiveDefault:         providerName == liveDefault,
		DesiredTimeoutSeconds: desiredTimeoutSeconds,
		LiveTimeoutSeconds:    liveTimeoutSeconds,
		RestartRequired:       desiredTimeoutSeconds != liveTimeoutSeconds,
		Status:                "ready",
		Issues:                []string{},
	}

	trimmedURL := strings.TrimSpace(summary.BaseURL)
	if trimmedURL == "" {
		item.Issues = append(item.Issues, "Base URL is not configured.")
	} else {
		parsed, err := url.Parse(trimmedURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			item.Issues = append(item.Issues, "Base URL is invalid.")
		} else {
			item.Host = parsed.Host
			item.Scheme = parsed.Scheme
			if parsed.Scheme != "https" && !isLocalProviderHost(parsed.Hostname()) {
				item.Issues = append(item.Issues, "Base URL is not HTTPS.")
			}
		}
	}
	if !summary.HasAPIKey {
		item.Issues = append(item.Issues, "API key is not configured.")
	}
	if item.IsDesiredDefault && !summary.HasAPIKey {
		item.Issues = append(item.Issues, "Desired default provider cannot serve traffic without credentials.")
	}
	if item.IsDesiredDefault && trimmedURL == "" {
		item.Issues = append(item.Issues, "Desired default provider does not have a valid endpoint.")
	}
	if item.RestartRequired {
		item.Issues = append(item.Issues, fmt.Sprintf("Provider timeout change requires restart (%ds live, %ds desired).", liveTimeoutSeconds, desiredTimeoutSeconds))
	}

	switch {
	case item.IsDesiredDefault && (!summary.HasAPIKey || trimmedURL == "" || item.Host == "" || item.Scheme == ""):
		item.Status = "degraded"
	case len(item.Issues) > 0:
		item.Status = "warning"
	default:
		item.Status = "ready"
	}
	return item, nil
}

func (s *Service) mergeWithRecord(ctx context.Context, defaults provider.RuntimeConfig, record record) (provider.RuntimeConfig, error) {
	result := defaults
	if record.BaseURL != "" {
		result.BaseURL = record.BaseURL
	}
	if record.EncryptedAPIKey != "" && s.encryptor != nil {
		plaintext, err := s.encryptor.Decrypt(ctx, record.EncryptedAPIKey)
		if err != nil {
			return provider.RuntimeConfig{}, err
		}
		result.APIKey = string(plaintext)
	}
	return result, nil
}

func (s *Service) defaultConfig(providerName domain.Provider) provider.RuntimeConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.defaults[providerName]
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func ParseUpdatePayload(payload any) (map[domain.Provider]UpdateInput, error) {
	rawProviders, ok := payload.(map[string]any)
	if !ok {
		return nil, errors.New("providers must be an object")
	}
	out := map[domain.Provider]UpdateInput{}
	for key, raw := range rawProviders {
		providerName := domain.Provider(key)
		switch providerName {
		case domain.ProviderOpenAI, domain.ProviderAnthropic, domain.ProviderGemini:
		default:
			return nil, errors.New("unknown provider: " + key)
		}
		itemMap, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("provider config must be an object: " + key)
		}
		update := UpdateInput{}
		if value, exists := itemMap["api_key"]; exists {
			text := stringValue(value)
			update.APIKey = &text
		}
		if value, exists := itemMap["base_url"]; exists {
			text := stringValue(value)
			update.BaseURL = &text
		}
		out[providerName] = update
	}
	return out, nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if typed, ok := value.(string); ok {
		return typed
	}
	return fmt.Sprintf("%v", value)
}

func isLocalProviderHost(host string) bool {
	value := strings.TrimSpace(strings.ToLower(host))
	switch value {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return strings.HasSuffix(value, ".local")
	}
}
