package systemconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/domain"
)

type Snapshot struct {
	DefaultProvider            domain.Provider `json:"default_provider"`
	VectorStoreBackend         string          `json:"vector_store_backend"`
	ConversationTimeoutSeconds int             `json:"conversation_timeout_seconds"`
	ProviderTimeoutSeconds     int             `json:"provider_timeout_seconds"`
	MessageThreshold           int             `json:"message_threshold"`
	TokenThreshold             int             `json:"token_threshold"`
	MaxContextTokens           int             `json:"max_context_tokens"`
	MaxInjectionTokens         int             `json:"max_injection_tokens"`
	RateLimitRPS               int             `json:"rate_limit_rps"`
	RateLimitBurst             int             `json:"rate_limit_burst"`
	CacheTTLSeconds            int             `json:"cache_ttl_seconds"`
	CORSAllowedOrigins         []string        `json:"cors_allowed_origins"`
	SessionCookieCrossSite     bool            `json:"session_cookie_cross_site"`
}

type FieldMeta struct {
	ApplyMode   string `json:"apply_mode"`
	Source      string `json:"source"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

type LiveApplier struct {
	SetDefaultProvider     func(domain.Provider)
	SetThresholds          func(messageThreshold, tokenThreshold int)
	SetConversationTimeout func(time.Duration)
	SetRateLimit           func(rps, burst int)
}

type Overrides struct {
	DefaultProvider            *domain.Provider
	VectorStoreBackend         *string
	ConversationTimeoutSeconds *int
	ProviderTimeoutSeconds     *int
	MessageThreshold           *int
	TokenThreshold             *int
	MaxContextTokens           *int
	MaxInjectionTokens         *int
	RateLimitRPS               *int
	RateLimitBurst             *int
	CacheTTLSeconds            *int
	CORSAllowedOrigins         *[]string
	SessionCookieCrossSite     *bool
}

type Service struct {
	db       *sql.DB
	defaults Snapshot

	mu        sync.RWMutex
	overrides Overrides
	live      Snapshot
	hooks     LiveApplier
}

func DefaultsFromConfig(cfg bootstrap.Config) Snapshot {
	return Snapshot{
		DefaultProvider:            cfg.DefaultProvider,
		VectorStoreBackend:         cfg.VectorStoreBackend,
		ConversationTimeoutSeconds: int(cfg.ConversationTimeout.Seconds()),
		ProviderTimeoutSeconds:     int(cfg.ProviderTimeout.Seconds()),
		MessageThreshold:           cfg.MessageThreshold,
		TokenThreshold:             cfg.TokenThreshold,
		MaxContextTokens:           cfg.MaxContextTokens,
		MaxInjectionTokens:         cfg.MaxInjectionTokens,
		RateLimitRPS:               cfg.RateLimitRPS,
		RateLimitBurst:             cfg.RateLimitBurst,
		CacheTTLSeconds:            int(cfg.CacheTTL.Seconds()),
		CORSAllowedOrigins:         append([]string(nil), cfg.CORSAllowedOrigins...),
		SessionCookieCrossSite:     cfg.SessionCookieCrossSite,
	}
}

func NewService(db *sql.DB, defaults Snapshot) *Service {
	return &Service{
		db:       db,
		defaults: defaults,
		live:     defaults,
	}
}

func (s *Service) Bootstrap(ctx context.Context) (Snapshot, error) {
	overrides, err := s.loadOverrides(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	desired := overrides.Apply(s.defaults)

	s.mu.Lock()
	s.overrides = overrides
	s.live = desired
	s.mu.Unlock()

	return desired, nil
}

func (s *Service) SetLiveApplier(hooks LiveApplier) {
	s.mu.Lock()
	s.hooks = hooks
	s.mu.Unlock()
}

func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.overrides.Apply(s.defaults)
}

func (s *Service) LiveSnapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.live
}

func (s *Service) Metadata() map[string]FieldMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return metadataForOverrides(s.overrides)
}

func (s *Service) Envelope() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{
		"runtime_defaults": s.defaults.AsMap(),
		"runtime":          s.overrides.Apply(s.defaults).AsMap(),
		"runtime_live":     s.live.AsMap(),
		"runtime_meta":     metadataForOverrides(s.overrides),
	}
}

func (s *Service) Update(ctx context.Context, fields map[string]any) (map[string]any, error) {
	s.mu.RLock()
	currentOverrides := s.overrides
	currentLive := s.live
	hooks := s.hooks
	s.mu.RUnlock()

	nextOverrides, err := applyFieldOverrides(currentOverrides, fields)
	if err != nil {
		return nil, err
	}
	if err := s.persistOverrides(ctx, nextOverrides); err != nil {
		return nil, err
	}

	desired := nextOverrides.Apply(s.defaults)
	live := currentLive

	if hooks.SetDefaultProvider != nil {
		hooks.SetDefaultProvider(desired.DefaultProvider)
		live.DefaultProvider = desired.DefaultProvider
	}
	if hooks.SetThresholds != nil {
		hooks.SetThresholds(desired.MessageThreshold, desired.TokenThreshold)
		live.MessageThreshold = desired.MessageThreshold
		live.TokenThreshold = desired.TokenThreshold
	}
	if hooks.SetConversationTimeout != nil {
		hooks.SetConversationTimeout(time.Duration(desired.ConversationTimeoutSeconds) * time.Second)
		live.ConversationTimeoutSeconds = desired.ConversationTimeoutSeconds
	}
	if hooks.SetRateLimit != nil {
		hooks.SetRateLimit(desired.RateLimitRPS, desired.RateLimitBurst)
		live.RateLimitRPS = desired.RateLimitRPS
		live.RateLimitBurst = desired.RateLimitBurst
	}

	s.mu.Lock()
	s.overrides = nextOverrides
	s.live = live
	s.mu.Unlock()

	return s.Envelope(), nil
}

func (s Snapshot) AsMap() map[string]any {
	return map[string]any{
		"default_provider":             s.DefaultProvider,
		"vector_store_backend":         s.VectorStoreBackend,
		"conversation_timeout_seconds": s.ConversationTimeoutSeconds,
		"provider_timeout_seconds":     s.ProviderTimeoutSeconds,
		"message_threshold":            s.MessageThreshold,
		"token_threshold":              s.TokenThreshold,
		"max_context_tokens":           s.MaxContextTokens,
		"max_injection_tokens":         s.MaxInjectionTokens,
		"rate_limit_rps":               s.RateLimitRPS,
		"rate_limit_burst":             s.RateLimitBurst,
		"cache_ttl_seconds":            s.CacheTTLSeconds,
		"cors_allowed_origins":         append([]string(nil), s.CORSAllowedOrigins...),
		"session_cookie_cross_site":    s.SessionCookieCrossSite,
	}
}

func (s Snapshot) ApplyConfig(cfg bootstrap.Config) bootstrap.Config {
	cfg.DefaultProvider = s.DefaultProvider
	cfg.VectorStoreBackend = s.VectorStoreBackend
	cfg.ConversationTimeout = time.Duration(s.ConversationTimeoutSeconds) * time.Second
	cfg.ProviderTimeout = time.Duration(s.ProviderTimeoutSeconds) * time.Second
	cfg.MessageThreshold = s.MessageThreshold
	cfg.TokenThreshold = s.TokenThreshold
	cfg.MaxContextTokens = s.MaxContextTokens
	cfg.MaxInjectionTokens = s.MaxInjectionTokens
	cfg.RateLimitRPS = s.RateLimitRPS
	cfg.RateLimitBurst = s.RateLimitBurst
	cfg.CacheTTL = time.Duration(s.CacheTTLSeconds) * time.Second
	cfg.CORSAllowedOrigins = append([]string(nil), s.CORSAllowedOrigins...)
	cfg.SessionCookieCrossSite = s.SessionCookieCrossSite
	return cfg
}

func (o Overrides) Apply(defaults Snapshot) Snapshot {
	snapshot := defaults
	if o.DefaultProvider != nil {
		snapshot.DefaultProvider = *o.DefaultProvider
	}
	if o.VectorStoreBackend != nil {
		snapshot.VectorStoreBackend = *o.VectorStoreBackend
	}
	if o.ConversationTimeoutSeconds != nil {
		snapshot.ConversationTimeoutSeconds = *o.ConversationTimeoutSeconds
	}
	if o.ProviderTimeoutSeconds != nil {
		snapshot.ProviderTimeoutSeconds = *o.ProviderTimeoutSeconds
	}
	if o.MessageThreshold != nil {
		snapshot.MessageThreshold = *o.MessageThreshold
	}
	if o.TokenThreshold != nil {
		snapshot.TokenThreshold = *o.TokenThreshold
	}
	if o.MaxContextTokens != nil {
		snapshot.MaxContextTokens = *o.MaxContextTokens
	}
	if o.MaxInjectionTokens != nil {
		snapshot.MaxInjectionTokens = *o.MaxInjectionTokens
	}
	if o.RateLimitRPS != nil {
		snapshot.RateLimitRPS = *o.RateLimitRPS
	}
	if o.RateLimitBurst != nil {
		snapshot.RateLimitBurst = *o.RateLimitBurst
	}
	if o.CacheTTLSeconds != nil {
		snapshot.CacheTTLSeconds = *o.CacheTTLSeconds
	}
	if o.CORSAllowedOrigins != nil {
		snapshot.CORSAllowedOrigins = append([]string(nil), (*o.CORSAllowedOrigins)...)
	}
	if o.SessionCookieCrossSite != nil {
		snapshot.SessionCookieCrossSite = *o.SessionCookieCrossSite
	}
	return snapshot
}

func (s *Service) loadOverrides(ctx context.Context) (Overrides, error) {
	if s.db == nil {
		return Overrides{}, nil
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT
			default_provider,
			vector_store_backend,
			conversation_timeout_seconds,
			provider_timeout_seconds,
			message_threshold,
			token_threshold,
			max_context_tokens,
			max_injection_tokens,
			rate_limit_rps,
			rate_limit_burst,
			cache_ttl_seconds,
			cors_allowed_origins,
			session_cookie_cross_site
		FROM system_settings
		WHERE id = $1
	`, "global")

	var (
		defaultProvider            sql.NullString
		vectorStoreBackend         sql.NullString
		conversationTimeoutSeconds sql.NullInt64
		providerTimeoutSeconds     sql.NullInt64
		messageThreshold           sql.NullInt64
		tokenThreshold             sql.NullInt64
		maxContextTokens           sql.NullInt64
		maxInjectionTokens         sql.NullInt64
		rateLimitRPS               sql.NullInt64
		rateLimitBurst             sql.NullInt64
		cacheTTLSeconds            sql.NullInt64
		corsAllowedOrigins         sql.NullString
		sessionCookieCrossSite     sql.NullBool
	)
	if err := row.Scan(
		&defaultProvider,
		&vectorStoreBackend,
		&conversationTimeoutSeconds,
		&providerTimeoutSeconds,
		&messageThreshold,
		&tokenThreshold,
		&maxContextTokens,
		&maxInjectionTokens,
		&rateLimitRPS,
		&rateLimitBurst,
		&cacheTTLSeconds,
		&corsAllowedOrigins,
		&sessionCookieCrossSite,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Overrides{}, nil
		}
		return Overrides{}, err
	}

	overrides := Overrides{}
	if defaultProvider.Valid {
		value := domain.Provider(defaultProvider.String)
		overrides.DefaultProvider = &value
	}
	if vectorStoreBackend.Valid {
		value := vectorStoreBackend.String
		overrides.VectorStoreBackend = &value
	}
	if conversationTimeoutSeconds.Valid {
		value := int(conversationTimeoutSeconds.Int64)
		overrides.ConversationTimeoutSeconds = &value
	}
	if providerTimeoutSeconds.Valid {
		value := int(providerTimeoutSeconds.Int64)
		overrides.ProviderTimeoutSeconds = &value
	}
	if messageThreshold.Valid {
		value := int(messageThreshold.Int64)
		overrides.MessageThreshold = &value
	}
	if tokenThreshold.Valid {
		value := int(tokenThreshold.Int64)
		overrides.TokenThreshold = &value
	}
	if maxContextTokens.Valid {
		value := int(maxContextTokens.Int64)
		overrides.MaxContextTokens = &value
	}
	if maxInjectionTokens.Valid {
		value := int(maxInjectionTokens.Int64)
		overrides.MaxInjectionTokens = &value
	}
	if rateLimitRPS.Valid {
		value := int(rateLimitRPS.Int64)
		overrides.RateLimitRPS = &value
	}
	if rateLimitBurst.Valid {
		value := int(rateLimitBurst.Int64)
		overrides.RateLimitBurst = &value
	}
	if cacheTTLSeconds.Valid {
		value := int(cacheTTLSeconds.Int64)
		overrides.CacheTTLSeconds = &value
	}
	if corsAllowedOrigins.Valid {
		values := []string{}
		if err := json.Unmarshal([]byte(corsAllowedOrigins.String), &values); err != nil {
			return Overrides{}, err
		}
		overrides.CORSAllowedOrigins = &values
	}
	if sessionCookieCrossSite.Valid {
		value := sessionCookieCrossSite.Bool
		overrides.SessionCookieCrossSite = &value
	}
	return overrides, nil
}

func (s *Service) persistOverrides(ctx context.Context, overrides Overrides) error {
	if s.db == nil {
		return nil
	}

	var corsJSON any
	if overrides.CORSAllowedOrigins != nil {
		payload, err := json.Marshal(*overrides.CORSAllowedOrigins)
		if err != nil {
			return err
		}
		corsJSON = string(payload)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system_settings (
			id,
			default_provider,
			vector_store_backend,
			conversation_timeout_seconds,
			provider_timeout_seconds,
			message_threshold,
			token_threshold,
			max_context_tokens,
			max_injection_tokens,
			rate_limit_rps,
			rate_limit_burst,
			cache_ttl_seconds,
			cors_allowed_origins,
			session_cookie_cross_site,
			created_at,
			updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::jsonb,$14,$15,$15
		)
		ON CONFLICT (id) DO UPDATE SET
			default_provider = EXCLUDED.default_provider,
			vector_store_backend = EXCLUDED.vector_store_backend,
			conversation_timeout_seconds = EXCLUDED.conversation_timeout_seconds,
			provider_timeout_seconds = EXCLUDED.provider_timeout_seconds,
			message_threshold = EXCLUDED.message_threshold,
			token_threshold = EXCLUDED.token_threshold,
			max_context_tokens = EXCLUDED.max_context_tokens,
			max_injection_tokens = EXCLUDED.max_injection_tokens,
			rate_limit_rps = EXCLUDED.rate_limit_rps,
			rate_limit_burst = EXCLUDED.rate_limit_burst,
			cache_ttl_seconds = EXCLUDED.cache_ttl_seconds,
			cors_allowed_origins = EXCLUDED.cors_allowed_origins,
			session_cookie_cross_site = EXCLUDED.session_cookie_cross_site,
			updated_at = EXCLUDED.updated_at
	`,
		"global",
		nullableProvider(overrides.DefaultProvider),
		nullableString(overrides.VectorStoreBackend),
		nullableInt(overrides.ConversationTimeoutSeconds),
		nullableInt(overrides.ProviderTimeoutSeconds),
		nullableInt(overrides.MessageThreshold),
		nullableInt(overrides.TokenThreshold),
		nullableInt(overrides.MaxContextTokens),
		nullableInt(overrides.MaxInjectionTokens),
		nullableInt(overrides.RateLimitRPS),
		nullableInt(overrides.RateLimitBurst),
		nullableInt(overrides.CacheTTLSeconds),
		corsJSON,
		nullableBool(overrides.SessionCookieCrossSite),
		time.Now().UTC(),
	)
	return err
}

func metadataForOverrides(overrides Overrides) map[string]FieldMeta {
	return map[string]FieldMeta{
		"default_provider": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.DefaultProvider != nil),
			Category:    "traffic",
			Description: "Fallback upstream provider when the request and user profile do not pin one explicitly.",
		},
		"vector_store_backend": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.VectorStoreBackend != nil),
			Category:    "memory",
			Description: "Primary vector backend implementation used by the memory retrieval layer.",
		},
		"conversation_timeout_seconds": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.ConversationTimeoutSeconds != nil),
			Category:    "traffic",
			Description: "Idle window before the scheduler marks an active conversation as timed out.",
		},
		"provider_timeout_seconds": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.ProviderTimeoutSeconds != nil),
			Category:    "traffic",
			Description: "Per-request upstream timeout for provider adapters and HTTP clients.",
		},
		"message_threshold": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MessageThreshold != nil),
			Category:    "traffic",
			Description: "Automatic finalize threshold based on the number of conversation messages.",
		},
		"token_threshold": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.TokenThreshold != nil),
			Category:    "traffic",
			Description: "Automatic finalize threshold based on accumulated request and response tokens.",
		},
		"max_context_tokens": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MaxContextTokens != nil),
			Category:    "memory",
			Description: "Upper budget available to request assembly before upstream truncation or rejection.",
		},
		"max_injection_tokens": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MaxInjectionTokens != nil),
			Category:    "memory",
			Description: "Injection budget used by memory context assembly and related defaults.",
		},
		"rate_limit_rps": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.RateLimitRPS != nil),
			Category:    "security",
			Description: "Sustained request rate allowed for each authenticated API key and user pair.",
		},
		"rate_limit_burst": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.RateLimitBurst != nil),
			Category:    "security",
			Description: "Burst capacity allowed before proxy requests begin returning rate-limit responses.",
		},
		"cache_ttl_seconds": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.CacheTTLSeconds != nil),
			Category:    "memory",
			Description: "Default TTL for Redis-backed and process-local JSON cache entries.",
		},
		"cors_allowed_origins": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.CORSAllowedOrigins != nil),
			Category:    "web",
			Description: "Cross-origin origins allowed to use the admin session and direct target API requests.",
		},
		"session_cookie_cross_site": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.SessionCookieCrossSite != nil),
			Category:    "web",
			Description: "Controls whether the admin session cookie is issued with cross-site semantics.",
		},
	}
}

func applyFieldOverrides(base Overrides, fields map[string]any) (Overrides, error) {
	next := base
	for key, raw := range fields {
		switch key {
		case "default_provider":
			if raw == nil {
				next.DefaultProvider = nil
				continue
			}
			value := domain.Provider(strings.TrimSpace(stringValue(raw)))
			switch value {
			case domain.ProviderOpenAI, domain.ProviderAnthropic, domain.ProviderGemini:
			default:
				return Overrides{}, fmt.Errorf("unsupported default_provider: %s", value)
			}
			next.DefaultProvider = &value
		case "vector_store_backend":
			if raw == nil {
				next.VectorStoreBackend = nil
				continue
			}
			value := strings.TrimSpace(stringValue(raw))
			if value == "" {
				return Overrides{}, errors.New("vector_store_backend cannot be empty")
			}
			next.VectorStoreBackend = &value
		case "conversation_timeout_seconds":
			value, err := intOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.ConversationTimeoutSeconds = value
		case "provider_timeout_seconds":
			value, err := intOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.ProviderTimeoutSeconds = value
		case "message_threshold":
			value, err := intOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.MessageThreshold = value
		case "token_threshold":
			value, err := intOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.TokenThreshold = value
		case "max_context_tokens":
			value, err := intOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.MaxContextTokens = value
		case "max_injection_tokens":
			value, err := intOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.MaxInjectionTokens = value
		case "rate_limit_rps":
			value, err := intOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.RateLimitRPS = value
		case "rate_limit_burst":
			value, err := intOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.RateLimitBurst = value
		case "cache_ttl_seconds":
			value, err := intOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.CacheTTLSeconds = value
		case "cors_allowed_origins":
			value, err := stringSliceOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.CORSAllowedOrigins = value
		case "session_cookie_cross_site":
			value, err := boolOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.SessionCookieCrossSite = value
		}
	}
	return next, nil
}

func intOverride(raw any) (*int, error) {
	if raw == nil {
		return nil, nil
	}
	switch typed := raw.(type) {
	case int:
		value := typed
		return &value, nil
	case int32:
		value := int(typed)
		return &value, nil
	case int64:
		value := int(typed)
		return &value, nil
	case float64:
		value := int(typed)
		return &value, nil
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return nil, err
		}
		value := int(parsed)
		return &value, nil
	default:
		return nil, fmt.Errorf("expected integer, got %T", raw)
	}
}

func boolOverride(raw any) (*bool, error) {
	if raw == nil {
		return nil, nil
	}
	typed, ok := raw.(bool)
	if !ok {
		return nil, fmt.Errorf("expected boolean, got %T", raw)
	}
	return &typed, nil
}

func stringSliceOverride(raw any) (*[]string, error) {
	if raw == nil {
		return nil, nil
	}
	out := []string{}
	switch typed := raw.(type) {
	case string:
		for _, item := range strings.FieldsFunc(typed, func(r rune) bool { return r == ',' || r == '\n' }) {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	case []string:
		for _, item := range typed {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	case []any:
		for _, item := range typed {
			if trimmed := strings.TrimSpace(stringValue(item)); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	default:
		return nil, fmt.Errorf("expected string array, got %T", raw)
	}
	return &out, nil
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

func sourceFor(overridden bool) string {
	if overridden {
		return "override"
	}
	return "default"
}

func nullableProvider(value *domain.Provider) any {
	if value == nil {
		return nil
	}
	return string(*value)
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}
