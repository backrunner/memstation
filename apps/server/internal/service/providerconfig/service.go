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

type Scope string

const (
	ScopeGateway   Scope = "gateway"
	ScopeInternal  Scope = "internal"
	ScopeEmbedding Scope = "embedding"
)

type Summary struct {
	Scope     Scope           `json:"scope"`
	Provider  domain.Provider `json:"provider"`
	BaseURL   string          `json:"base_url"`
	HasAPIKey bool            `json:"has_api_key"`
	Source    string          `json:"source"`
	UpdatedAt *time.Time      `json:"updated_at,omitempty"`
}

type Diagnostic struct {
	Scope                 Scope           `json:"scope"`
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

type Defaults map[Scope]map[domain.Provider]provider.RuntimeConfig

type record struct {
	EncryptedAPIKey string
	BaseURL         string
	UpdatedAt       time.Time
}

type scopeProviderKey struct {
	Scope    Scope
	Provider domain.Provider
}

type Service struct {
	db        *sqlpkg.DB
	encryptor ports.Encryptor

	mu       sync.RWMutex
	defaults Defaults
	records  map[scopeProviderKey]record
}

type scopeResolver struct {
	service *Service
	scope   Scope
}

func NewService(db *sqlpkg.DB, encryptor ports.Encryptor, defaults Defaults) *Service {
	copiedDefaults := Defaults{}
	for _, scope := range supportedScopes() {
		normalized := make(map[domain.Provider]provider.RuntimeConfig, len(defaults[scope]))
		for providerName, value := range defaults[scope] {
			if normalizedProvider, ok := normalizeProviderForScope(scope, providerName); ok {
				normalized[normalizedProvider] = value
			}
		}
		copiedDefaults[scope] = normalized
	}
	return &Service{
		db:        db,
		encryptor: encryptor,
		defaults:  copiedDefaults,
		records:   map[scopeProviderKey]record{},
	}
}

func (s *Service) Resolver(scope Scope) provider.ConfigResolver {
	return scopeResolver{service: s, scope: scope}
}

func (r scopeResolver) Resolve(ctx context.Context, providerName domain.Provider) (provider.RuntimeConfig, error) {
	if r.service == nil {
		return provider.RuntimeConfig{}, errors.New("provider config service is not configured")
	}
	return r.service.Resolve(ctx, r.scope, providerName)
}

func (s *Service) Resolve(ctx context.Context, scope Scope, providerName domain.Provider) (provider.RuntimeConfig, error) {
	normalizedScope, err := normalizeScope(scope)
	if err != nil {
		return provider.RuntimeConfig{}, err
	}
	normalizedProvider, ok := normalizeProviderForScope(normalizedScope, providerName)
	if !ok {
		return provider.RuntimeConfig{}, fmt.Errorf("provider %q is not supported for scope %q", providerName, normalizedScope)
	}

	defaults := s.defaultConfig(normalizedScope, normalizedProvider)
	if s.db == nil {
		s.mu.RLock()
		record, ok := s.records[scopeProviderKey{Scope: normalizedScope, Provider: normalizedProvider}]
		s.mu.RUnlock()
		if !ok {
			return defaults, nil
		}
		return s.mergeWithRecord(ctx, defaults, record)
	}

	row := s.db.QueryRowContext(ctx, `
        SELECT encrypted_api_key, base_url, updated_at
        FROM provider_credentials
        WHERE scope = $1 AND provider = $2
    `, string(normalizedScope), string(normalizedProvider))
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

func (s *Service) ListByScope(ctx context.Context) (map[Scope][]Summary, error) {
	out := make(map[Scope][]Summary, len(supportedScopes()))
	for _, scope := range supportedScopes() {
		providers := supportedProvidersForScope(scope)
		summaries := make([]Summary, 0, len(providers))
		for _, providerName := range providers {
			summary, err := s.summaryForProvider(ctx, scope, providerName)
			if err != nil {
				return nil, err
			}
			summaries = append(summaries, summary)
		}
		out[scope] = summaries
	}
	return out, nil
}

func (s *Service) Diagnostics(ctx context.Context, desiredDefaults, liveDefaults map[Scope]domain.Provider, desiredTimeoutSeconds, liveTimeoutSeconds int) ([]Diagnostic, error) {
	items := make([]Diagnostic, 0, 12)
	for _, scope := range supportedScopes() {
		for _, providerName := range supportedProvidersForScope(scope) {
			item, err := s.diagnosticForProvider(ctx, scope, providerName, desiredDefaults[scope], liveDefaults[scope], desiredTimeoutSeconds, liveTimeoutSeconds)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
	}
	return items, nil
}

func (s *Service) Update(ctx context.Context, scope Scope, providerName domain.Provider, input UpdateInput) (Summary, error) {
	normalizedScope, err := normalizeScope(scope)
	if err != nil {
		return Summary{}, err
	}
	normalizedProvider, ok := normalizeProviderForScope(normalizedScope, providerName)
	if !ok {
		return Summary{}, fmt.Errorf("provider %q is not supported for scope %q", providerName, normalizedScope)
	}

	current, err := s.Resolve(ctx, normalizedScope, normalizedProvider)
	if err != nil {
		return Summary{}, err
	}

	next := current
	if input.APIKey != nil {
		next.APIKey = strings.TrimSpace(*input.APIKey)
	}
	if input.BaseURL != nil {
		next.BaseURL = strings.TrimSpace(*input.BaseURL)
	}

	encryptedAPIKey := ""
	if next.APIKey != "" {
		if s.db != nil && s.encryptor == nil {
			return Summary{}, errors.New("provider config encryptor is not configured")
		}
		if s.encryptor != nil {
			ciphertext, err := s.encryptor.Encrypt(ctx, []byte(next.APIKey))
			if err != nil {
				return Summary{}, err
			}
			encryptedAPIKey = ciphertext
		}
	}

	now := time.Now().UTC()
	key := scopeProviderKey{Scope: normalizedScope, Provider: normalizedProvider}
	if s.db == nil {
		s.mu.Lock()
		s.records[key] = record{
			EncryptedAPIKey: encryptedAPIKey,
			BaseURL:         next.BaseURL,
			UpdatedAt:       now,
		}
		s.mu.Unlock()
		return s.summaryForProvider(ctx, normalizedScope, normalizedProvider)
	}

	_, err = s.db.ExecContext(ctx, `
        INSERT INTO provider_credentials (scope, provider, encrypted_api_key, base_url, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$5)
        ON CONFLICT (scope, provider) DO UPDATE SET
            encrypted_api_key = EXCLUDED.encrypted_api_key,
            base_url = EXCLUDED.base_url,
            updated_at = EXCLUDED.updated_at
    `, string(normalizedScope), string(normalizedProvider), nullString(encryptedAPIKey), nullString(next.BaseURL), now)
	if err != nil {
		return Summary{}, err
	}
	return s.summaryForProvider(ctx, normalizedScope, normalizedProvider)
}

func (s *Service) summaryForProvider(ctx context.Context, scope Scope, providerName domain.Provider) (Summary, error) {
	cfg, err := s.Resolve(ctx, scope, providerName)
	if err != nil {
		return Summary{}, err
	}
	summary := Summary{
		Scope:     scope,
		Provider:  providerName,
		BaseURL:   cfg.BaseURL,
		HasAPIKey: cfg.APIKey != "",
		Source:    "default",
	}

	defaults := s.defaultConfig(scope, providerName)
	if cfg.APIKey != defaults.APIKey || cfg.BaseURL != defaults.BaseURL {
		summary.Source = "override"
	}

	if s.db == nil {
		s.mu.RLock()
		record, ok := s.records[scopeProviderKey{Scope: scope, Provider: providerName}]
		s.mu.RUnlock()
		if ok && !record.UpdatedAt.IsZero() {
			summary.Source = "override"
			summary.UpdatedAt = &record.UpdatedAt
		}
		return summary, nil
	}

	row := s.db.QueryRowContext(ctx, `SELECT updated_at FROM provider_credentials WHERE scope = $1 AND provider = $2`, string(scope), string(providerName))
	var updatedAt sqlpkg.NullTime
	if err := row.Scan(&updatedAt); err == nil && updatedAt.Valid {
		summary.Source = "override"
		summary.UpdatedAt = &updatedAt.Time
	}
	return summary, nil
}

func (s *Service) diagnosticForProvider(ctx context.Context, scope Scope, providerName, desiredDefault, liveDefault domain.Provider, desiredTimeoutSeconds, liveTimeoutSeconds int) (Diagnostic, error) {
	summary, err := s.summaryForProvider(ctx, scope, providerName)
	if err != nil {
		return Diagnostic{}, err
	}

	desiredDefault = domain.NormalizeProvider(string(desiredDefault))
	liveDefault = domain.NormalizeProvider(string(liveDefault))
	item := Diagnostic{
		Scope:                 scope,
		Provider:              summary.Provider,
		BaseURL:               summary.BaseURL,
		HasAPIKey:             summary.HasAPIKey,
		Source:                summary.Source,
		UpdatedAt:             summary.UpdatedAt,
		IsDesiredDefault:      scope != ScopeEmbedding && providerName == desiredDefault,
		IsLiveDefault:         scope != ScopeEmbedding && providerName == liveDefault,
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
	if record.EncryptedAPIKey != "" {
		if s.encryptor == nil {
			return provider.RuntimeConfig{}, errors.New("provider config encryptor is not configured")
		}
		plaintext, err := s.encryptor.Decrypt(ctx, record.EncryptedAPIKey)
		if err != nil {
			return provider.RuntimeConfig{}, err
		}
		result.APIKey = string(plaintext)
	}
	return result, nil
}

func (s *Service) defaultConfig(scope Scope, providerName domain.Provider) provider.RuntimeConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if scopedDefaults, ok := s.defaults[scope]; ok {
		return scopedDefaults[providerName]
	}
	return provider.RuntimeConfig{}
}

func ParseScopedUpdatePayload(payload any) (map[Scope]map[domain.Provider]UpdateInput, error) {
	rawScopes, ok := payload.(map[string]any)
	if !ok {
		return nil, errors.New("provider_sets must be an object")
	}
	out := map[Scope]map[domain.Provider]UpdateInput{}
	for rawScope, rawValue := range rawScopes {
		scope, err := normalizeScope(Scope(rawScope))
		if err != nil {
			return nil, err
		}
		scopeMap, ok := rawValue.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("provider set must be an object: %s", rawScope)
		}
		if nestedProviders, exists := scopeMap["providers"]; exists {
			scopeMap, ok = nestedProviders.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("providers must be an object for scope %s", rawScope)
			}
		}
		if len(scopeMap) == 0 {
			continue
		}
		out[scope] = map[domain.Provider]UpdateInput{}
		for rawProvider, rawConfig := range scopeMap {
			providerName, ok := normalizeProviderForScope(scope, domain.Provider(rawProvider))
			if !ok {
				return nil, fmt.Errorf("provider %q is not supported for scope %q", rawProvider, scope)
			}
			itemMap, ok := rawConfig.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("provider config must be an object: %s.%s", rawScope, rawProvider)
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
			out[scope][providerName] = update
		}
	}
	return out, nil
}

func ParseUpdatePayload(payload any) (map[domain.Provider]UpdateInput, error) {
	rawProviders, ok := payload.(map[string]any)
	if !ok {
		return nil, errors.New("providers must be an object")
	}
	out := map[domain.Provider]UpdateInput{}
	for rawProvider, rawConfig := range rawProviders {
		providerName, ok := normalizeProviderForScope(ScopeGateway, domain.Provider(rawProvider))
		if !ok {
			return nil, fmt.Errorf("provider %q is not supported for scope %q", rawProvider, ScopeGateway)
		}
		itemMap, ok := rawConfig.(map[string]any)
		if !ok {
			return nil, errors.New("provider config must be an object: " + rawProvider)
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

func supportedScopes() []Scope {
	return []Scope{ScopeGateway, ScopeInternal, ScopeEmbedding}
}

func supportedProvidersForScope(scope Scope) []domain.Provider {
	switch scope {
	case ScopeEmbedding:
		return domain.EmbeddingProviders()
	default:
		return domain.LLMProviders()
	}
}

func normalizeScope(scope Scope) (Scope, error) {
	switch Scope(strings.ToLower(strings.TrimSpace(string(scope)))) {
	case ScopeGateway:
		return ScopeGateway, nil
	case ScopeInternal:
		return ScopeInternal, nil
	case ScopeEmbedding:
		return ScopeEmbedding, nil
	default:
		return "", fmt.Errorf("unknown provider scope: %s", scope)
	}
}

func normalizeProviderForScope(scope Scope, providerName domain.Provider) (domain.Provider, bool) {
	normalized := domain.NormalizeProvider(string(providerName))
	for _, candidate := range supportedProvidersForScope(scope) {
		if normalized == candidate {
			return candidate, true
		}
	}
	return "", false
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
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
