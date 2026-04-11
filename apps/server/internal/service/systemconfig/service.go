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
	DefaultProvider                     domain.Provider `json:"default_provider"`
	InternalDefaultProvider             domain.Provider `json:"internal_default_provider"`
	VectorStoreBackend                  string          `json:"vector_store_backend"`
	ConversationTimeoutSeconds          int             `json:"conversation_timeout_seconds"`
	ProviderTimeoutSeconds              int             `json:"provider_timeout_seconds"`
	MessageThreshold                    int             `json:"message_threshold"`
	TokenThreshold                      int             `json:"token_threshold"`
	MaxContextTokens                    int             `json:"max_context_tokens"`
	MaxInjectionTokens                  int             `json:"max_injection_tokens"`
	MemoryRelationMaxHops               int             `json:"memory_relation_max_hops"`
	MemoryRelationHopDecayLambda        float64         `json:"memory_relation_hop_decay_lambda"`
	MemoryRelationHopDecayExponent      float64         `json:"memory_relation_hop_decay_exponent"`
	RateLimitRPS                        int             `json:"rate_limit_rps"`
	RateLimitBurst                      int             `json:"rate_limit_burst"`
	CacheTTLSeconds                     int             `json:"cache_ttl_seconds"`
	CORSAllowedOrigins                  []string        `json:"cors_allowed_origins"`
	SessionCookieCrossSite              bool            `json:"session_cookie_cross_site"`
	MultimodalEnabled                   bool            `json:"multimodal_enabled"`
	MultimodalStorageBackend            string          `json:"multimodal_storage_backend"`
	MultimodalStorageLocalRoot          string          `json:"multimodal_storage_local_root"`
	MultimodalStorageBucket             string          `json:"multimodal_storage_bucket"`
	MultimodalExtractImageModel         string          `json:"multimodal_extract_image_model"`
	MultimodalExtractAudioModel         string          `json:"multimodal_extract_audio_model"`
	MultimodalExtractVideoModel         string          `json:"multimodal_extract_video_model"`
	MultimodalExtractFileModel          string          `json:"multimodal_extract_file_model"`
	MultimodalAudioTranscriptionEnabled bool            `json:"multimodal_audio_transcription_enabled"`
	MultimodalVideoFrameSamplingEnabled bool            `json:"multimodal_video_frame_sampling_enabled"`
	MultimodalMaxAssetBytes             int64           `json:"multimodal_max_asset_bytes"`
	MultimodalAllowedMIMETypes          []string        `json:"multimodal_allowed_mime_types"`
}

type FieldMeta struct {
	ApplyMode   string `json:"apply_mode"`
	Source      string `json:"source"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

type LiveApplier struct {
	SetDefaultProvider         func(domain.Provider)
	SetInternalDefaultProvider func(domain.Provider)
	SetThresholds              func(messageThreshold, tokenThreshold int)
	SetConversationTimeout     func(time.Duration)
	SetMemoryRelationRecall    func(maxHops int, decayLambda, decayExponent float64)
	SetRateLimit               func(rps, burst int)
}

type Overrides struct {
	DefaultProvider                     *domain.Provider
	InternalDefaultProvider             *domain.Provider
	VectorStoreBackend                  *string
	ConversationTimeoutSeconds          *int
	ProviderTimeoutSeconds              *int
	MessageThreshold                    *int
	TokenThreshold                      *int
	MaxContextTokens                    *int
	MaxInjectionTokens                  *int
	MemoryRelationMaxHops               *int
	MemoryRelationHopDecayLambda        *float64
	MemoryRelationHopDecayExponent      *float64
	RateLimitRPS                        *int
	RateLimitBurst                      *int
	CacheTTLSeconds                     *int
	CORSAllowedOrigins                  *[]string
	SessionCookieCrossSite              *bool
	MultimodalEnabled                   *bool
	MultimodalStorageBackend            *string
	MultimodalStorageLocalRoot          *string
	MultimodalStorageBucket             *string
	MultimodalExtractImageModel         *string
	MultimodalExtractAudioModel         *string
	MultimodalExtractVideoModel         *string
	MultimodalExtractFileModel          *string
	MultimodalAudioTranscriptionEnabled *bool
	MultimodalVideoFrameSamplingEnabled *bool
	MultimodalMaxAssetBytes             *int64
	MultimodalAllowedMIMETypes          *[]string
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
		DefaultProvider:                     cfg.DefaultProvider,
		InternalDefaultProvider:             cfg.InternalDefaultProvider,
		VectorStoreBackend:                  cfg.VectorStoreBackend,
		ConversationTimeoutSeconds:          int(cfg.ConversationTimeout.Seconds()),
		ProviderTimeoutSeconds:              int(cfg.ProviderTimeout.Seconds()),
		MessageThreshold:                    cfg.MessageThreshold,
		TokenThreshold:                      cfg.TokenThreshold,
		MaxContextTokens:                    cfg.MaxContextTokens,
		MaxInjectionTokens:                  cfg.MaxInjectionTokens,
		MemoryRelationMaxHops:               cfg.MemoryRelationMaxHops,
		MemoryRelationHopDecayLambda:        cfg.MemoryRelationHopDecayLambda,
		MemoryRelationHopDecayExponent:      cfg.MemoryRelationHopDecayExponent,
		RateLimitRPS:                        cfg.RateLimitRPS,
		RateLimitBurst:                      cfg.RateLimitBurst,
		CacheTTLSeconds:                     int(cfg.CacheTTL.Seconds()),
		CORSAllowedOrigins:                  append([]string(nil), cfg.CORSAllowedOrigins...),
		SessionCookieCrossSite:              cfg.SessionCookieCrossSite,
		MultimodalEnabled:                   cfg.Multimodal.Enabled,
		MultimodalStorageBackend:            cfg.Multimodal.StorageBackend,
		MultimodalStorageLocalRoot:          cfg.Multimodal.StorageLocalRoot,
		MultimodalStorageBucket:             cfg.Multimodal.StorageBucket,
		MultimodalExtractImageModel:         cfg.Multimodal.ImageModel,
		MultimodalExtractAudioModel:         cfg.Multimodal.AudioModel,
		MultimodalExtractVideoModel:         cfg.Multimodal.VideoModel,
		MultimodalExtractFileModel:          cfg.Multimodal.FileModel,
		MultimodalAudioTranscriptionEnabled: cfg.Multimodal.AudioTranscriptionEnabled,
		MultimodalVideoFrameSamplingEnabled: cfg.Multimodal.VideoFrameSamplingEnabled,
		MultimodalMaxAssetBytes:             cfg.Multimodal.MaxAssetBytes,
		MultimodalAllowedMIMETypes:          append([]string(nil), cfg.Multimodal.AllowedMIMETypes...),
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
	if hooks.SetInternalDefaultProvider != nil {
		hooks.SetInternalDefaultProvider(desired.InternalDefaultProvider)
		live.InternalDefaultProvider = desired.InternalDefaultProvider
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
	if hooks.SetMemoryRelationRecall != nil {
		hooks.SetMemoryRelationRecall(desired.MemoryRelationMaxHops, desired.MemoryRelationHopDecayLambda, desired.MemoryRelationHopDecayExponent)
		live.MemoryRelationMaxHops = desired.MemoryRelationMaxHops
		live.MemoryRelationHopDecayLambda = desired.MemoryRelationHopDecayLambda
		live.MemoryRelationHopDecayExponent = desired.MemoryRelationHopDecayExponent
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
		"default_provider":                        s.DefaultProvider,
		"internal_default_provider":               s.InternalDefaultProvider,
		"vector_store_backend":                    s.VectorStoreBackend,
		"conversation_timeout_seconds":            s.ConversationTimeoutSeconds,
		"provider_timeout_seconds":                s.ProviderTimeoutSeconds,
		"message_threshold":                       s.MessageThreshold,
		"token_threshold":                         s.TokenThreshold,
		"max_context_tokens":                      s.MaxContextTokens,
		"max_injection_tokens":                    s.MaxInjectionTokens,
		"memory_relation_max_hops":                s.MemoryRelationMaxHops,
		"memory_relation_hop_decay_lambda":        s.MemoryRelationHopDecayLambda,
		"memory_relation_hop_decay_exponent":      s.MemoryRelationHopDecayExponent,
		"rate_limit_rps":                          s.RateLimitRPS,
		"rate_limit_burst":                        s.RateLimitBurst,
		"cache_ttl_seconds":                       s.CacheTTLSeconds,
		"cors_allowed_origins":                    append([]string(nil), s.CORSAllowedOrigins...),
		"session_cookie_cross_site":               s.SessionCookieCrossSite,
		"multimodal_enabled":                      s.MultimodalEnabled,
		"multimodal_storage_backend":              s.MultimodalStorageBackend,
		"multimodal_storage_local_root":           s.MultimodalStorageLocalRoot,
		"multimodal_storage_bucket":               s.MultimodalStorageBucket,
		"multimodal_extract_image_model":          s.MultimodalExtractImageModel,
		"multimodal_extract_audio_model":          s.MultimodalExtractAudioModel,
		"multimodal_extract_video_model":          s.MultimodalExtractVideoModel,
		"multimodal_extract_file_model":           s.MultimodalExtractFileModel,
		"multimodal_audio_transcription_enabled":  s.MultimodalAudioTranscriptionEnabled,
		"multimodal_video_frame_sampling_enabled": s.MultimodalVideoFrameSamplingEnabled,
		"multimodal_max_asset_bytes":              s.MultimodalMaxAssetBytes,
		"multimodal_allowed_mime_types":           append([]string(nil), s.MultimodalAllowedMIMETypes...),
	}
}

func (s Snapshot) ApplyConfig(cfg bootstrap.Config) bootstrap.Config {
	cfg.DefaultProvider = s.DefaultProvider
	cfg.InternalDefaultProvider = s.InternalDefaultProvider
	cfg.VectorStoreBackend = s.VectorStoreBackend
	cfg.ConversationTimeout = time.Duration(s.ConversationTimeoutSeconds) * time.Second
	cfg.ProviderTimeout = time.Duration(s.ProviderTimeoutSeconds) * time.Second
	cfg.MessageThreshold = s.MessageThreshold
	cfg.TokenThreshold = s.TokenThreshold
	cfg.MaxContextTokens = s.MaxContextTokens
	cfg.MaxInjectionTokens = s.MaxInjectionTokens
	cfg.MemoryRelationMaxHops = s.MemoryRelationMaxHops
	cfg.MemoryRelationHopDecayLambda = s.MemoryRelationHopDecayLambda
	cfg.MemoryRelationHopDecayExponent = s.MemoryRelationHopDecayExponent
	cfg.RateLimitRPS = s.RateLimitRPS
	cfg.RateLimitBurst = s.RateLimitBurst
	cfg.CacheTTL = time.Duration(s.CacheTTLSeconds) * time.Second
	cfg.CORSAllowedOrigins = append([]string(nil), s.CORSAllowedOrigins...)
	cfg.SessionCookieCrossSite = s.SessionCookieCrossSite
	cfg.Multimodal = bootstrap.MultimodalConfig{
		Enabled:                   s.MultimodalEnabled,
		StorageBackend:            s.MultimodalStorageBackend,
		StorageLocalRoot:          s.MultimodalStorageLocalRoot,
		StorageBucket:             s.MultimodalStorageBucket,
		ImageModel:                s.MultimodalExtractImageModel,
		AudioModel:                s.MultimodalExtractAudioModel,
		VideoModel:                s.MultimodalExtractVideoModel,
		FileModel:                 s.MultimodalExtractFileModel,
		AudioTranscriptionEnabled: s.MultimodalAudioTranscriptionEnabled,
		VideoFrameSamplingEnabled: s.MultimodalVideoFrameSamplingEnabled,
		MaxAssetBytes:             s.MultimodalMaxAssetBytes,
		AllowedMIMETypes:          append([]string(nil), s.MultimodalAllowedMIMETypes...),
	}
	return cfg
}

func (o Overrides) Apply(defaults Snapshot) Snapshot {
	snapshot := defaults
	if o.DefaultProvider != nil {
		snapshot.DefaultProvider = *o.DefaultProvider
	}
	if o.InternalDefaultProvider != nil {
		snapshot.InternalDefaultProvider = *o.InternalDefaultProvider
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
	if o.MemoryRelationMaxHops != nil {
		snapshot.MemoryRelationMaxHops = *o.MemoryRelationMaxHops
	}
	if o.MemoryRelationHopDecayLambda != nil {
		snapshot.MemoryRelationHopDecayLambda = *o.MemoryRelationHopDecayLambda
	}
	if o.MemoryRelationHopDecayExponent != nil {
		snapshot.MemoryRelationHopDecayExponent = *o.MemoryRelationHopDecayExponent
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
	if o.MultimodalEnabled != nil {
		snapshot.MultimodalEnabled = *o.MultimodalEnabled
	}
	if o.MultimodalStorageBackend != nil {
		snapshot.MultimodalStorageBackend = *o.MultimodalStorageBackend
	}
	if o.MultimodalStorageLocalRoot != nil {
		snapshot.MultimodalStorageLocalRoot = *o.MultimodalStorageLocalRoot
	}
	if o.MultimodalStorageBucket != nil {
		snapshot.MultimodalStorageBucket = *o.MultimodalStorageBucket
	}
	if o.MultimodalExtractImageModel != nil {
		snapshot.MultimodalExtractImageModel = *o.MultimodalExtractImageModel
	}
	if o.MultimodalExtractAudioModel != nil {
		snapshot.MultimodalExtractAudioModel = *o.MultimodalExtractAudioModel
	}
	if o.MultimodalExtractVideoModel != nil {
		snapshot.MultimodalExtractVideoModel = *o.MultimodalExtractVideoModel
	}
	if o.MultimodalExtractFileModel != nil {
		snapshot.MultimodalExtractFileModel = *o.MultimodalExtractFileModel
	}
	if o.MultimodalAudioTranscriptionEnabled != nil {
		snapshot.MultimodalAudioTranscriptionEnabled = *o.MultimodalAudioTranscriptionEnabled
	}
	if o.MultimodalVideoFrameSamplingEnabled != nil {
		snapshot.MultimodalVideoFrameSamplingEnabled = *o.MultimodalVideoFrameSamplingEnabled
	}
	if o.MultimodalMaxAssetBytes != nil {
		snapshot.MultimodalMaxAssetBytes = *o.MultimodalMaxAssetBytes
	}
	if o.MultimodalAllowedMIMETypes != nil {
		snapshot.MultimodalAllowedMIMETypes = append([]string(nil), (*o.MultimodalAllowedMIMETypes)...)
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
			internal_default_provider,
			vector_store_backend,
			conversation_timeout_seconds,
			provider_timeout_seconds,
			message_threshold,
			token_threshold,
			max_context_tokens,
			max_injection_tokens,
			memory_relation_max_hops,
			memory_relation_hop_decay_lambda,
			memory_relation_hop_decay_exponent,
			rate_limit_rps,
			rate_limit_burst,
			cache_ttl_seconds,
			cors_allowed_origins,
			session_cookie_cross_site,
			multimodal_enabled,
			multimodal_storage_backend,
			multimodal_storage_local_root,
			multimodal_storage_bucket,
			multimodal_extract_image_model,
			multimodal_extract_audio_model,
			multimodal_extract_video_model,
			multimodal_extract_file_model,
			multimodal_audio_transcription_enabled,
			multimodal_video_frame_sampling_enabled,
			multimodal_max_asset_bytes,
			multimodal_allowed_mime_types
		FROM system_settings
		WHERE id = $1
	`, "global")

	var (
		defaultProvider                     sql.NullString
		internalDefaultProvider             sql.NullString
		vectorStoreBackend                  sql.NullString
		conversationTimeoutSeconds          sql.NullInt64
		providerTimeoutSeconds              sql.NullInt64
		messageThreshold                    sql.NullInt64
		tokenThreshold                      sql.NullInt64
		maxContextTokens                    sql.NullInt64
		maxInjectionTokens                  sql.NullInt64
		memoryRelationMaxHops               sql.NullInt64
		memoryRelationHopDecayLambda        sql.NullFloat64
		memoryRelationHopDecayExponent      sql.NullFloat64
		rateLimitRPS                        sql.NullInt64
		rateLimitBurst                      sql.NullInt64
		cacheTTLSeconds                     sql.NullInt64
		corsAllowedOrigins                  sql.NullString
		sessionCookieCrossSite              sql.NullBool
		multimodalEnabled                   sql.NullBool
		multimodalStorageBackend            sql.NullString
		multimodalStorageLocalRoot          sql.NullString
		multimodalStorageBucket             sql.NullString
		multimodalExtractImageModel         sql.NullString
		multimodalExtractAudioModel         sql.NullString
		multimodalExtractVideoModel         sql.NullString
		multimodalExtractFileModel          sql.NullString
		multimodalAudioTranscriptionEnabled sql.NullBool
		multimodalVideoFrameSamplingEnabled sql.NullBool
		multimodalMaxAssetBytes             sql.NullInt64
		multimodalAllowedMIMETypes          sql.NullString
	)
	if err := row.Scan(
		&defaultProvider,
		&internalDefaultProvider,
		&vectorStoreBackend,
		&conversationTimeoutSeconds,
		&providerTimeoutSeconds,
		&messageThreshold,
		&tokenThreshold,
		&maxContextTokens,
		&maxInjectionTokens,
		&memoryRelationMaxHops,
		&memoryRelationHopDecayLambda,
		&memoryRelationHopDecayExponent,
		&rateLimitRPS,
		&rateLimitBurst,
		&cacheTTLSeconds,
		&corsAllowedOrigins,
		&sessionCookieCrossSite,
		&multimodalEnabled,
		&multimodalStorageBackend,
		&multimodalStorageLocalRoot,
		&multimodalStorageBucket,
		&multimodalExtractImageModel,
		&multimodalExtractAudioModel,
		&multimodalExtractVideoModel,
		&multimodalExtractFileModel,
		&multimodalAudioTranscriptionEnabled,
		&multimodalVideoFrameSamplingEnabled,
		&multimodalMaxAssetBytes,
		&multimodalAllowedMIMETypes,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Overrides{}, nil
		}
		return Overrides{}, err
	}

	overrides := Overrides{}
	if defaultProvider.Valid {
		value := domain.NormalizeProvider(defaultProvider.String)
		overrides.DefaultProvider = &value
	}
	if internalDefaultProvider.Valid {
		value := domain.NormalizeProvider(internalDefaultProvider.String)
		overrides.InternalDefaultProvider = &value
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
	if memoryRelationMaxHops.Valid {
		value := int(memoryRelationMaxHops.Int64)
		overrides.MemoryRelationMaxHops = &value
	}
	if memoryRelationHopDecayLambda.Valid {
		value := memoryRelationHopDecayLambda.Float64
		overrides.MemoryRelationHopDecayLambda = &value
	}
	if memoryRelationHopDecayExponent.Valid {
		value := memoryRelationHopDecayExponent.Float64
		overrides.MemoryRelationHopDecayExponent = &value
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
	if multimodalEnabled.Valid {
		value := multimodalEnabled.Bool
		overrides.MultimodalEnabled = &value
	}
	if multimodalStorageBackend.Valid {
		value := multimodalStorageBackend.String
		overrides.MultimodalStorageBackend = &value
	}
	if multimodalStorageLocalRoot.Valid {
		value := multimodalStorageLocalRoot.String
		overrides.MultimodalStorageLocalRoot = &value
	}
	if multimodalStorageBucket.Valid {
		value := multimodalStorageBucket.String
		overrides.MultimodalStorageBucket = &value
	}
	if multimodalExtractImageModel.Valid {
		value := multimodalExtractImageModel.String
		overrides.MultimodalExtractImageModel = &value
	}
	if multimodalExtractAudioModel.Valid {
		value := multimodalExtractAudioModel.String
		overrides.MultimodalExtractAudioModel = &value
	}
	if multimodalExtractVideoModel.Valid {
		value := multimodalExtractVideoModel.String
		overrides.MultimodalExtractVideoModel = &value
	}
	if multimodalExtractFileModel.Valid {
		value := multimodalExtractFileModel.String
		overrides.MultimodalExtractFileModel = &value
	}
	if multimodalAudioTranscriptionEnabled.Valid {
		value := multimodalAudioTranscriptionEnabled.Bool
		overrides.MultimodalAudioTranscriptionEnabled = &value
	}
	if multimodalVideoFrameSamplingEnabled.Valid {
		value := multimodalVideoFrameSamplingEnabled.Bool
		overrides.MultimodalVideoFrameSamplingEnabled = &value
	}
	if multimodalMaxAssetBytes.Valid {
		value := multimodalMaxAssetBytes.Int64
		overrides.MultimodalMaxAssetBytes = &value
	}
	if multimodalAllowedMIMETypes.Valid {
		values := []string{}
		if err := json.Unmarshal([]byte(multimodalAllowedMIMETypes.String), &values); err != nil {
			return Overrides{}, err
		}
		overrides.MultimodalAllowedMIMETypes = &values
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
	var allowedMIMETypesJSON any
	if overrides.MultimodalAllowedMIMETypes != nil {
		payload, err := json.Marshal(*overrides.MultimodalAllowedMIMETypes)
		if err != nil {
			return err
		}
		allowedMIMETypesJSON = string(payload)
	}
	now := time.Now().UTC()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system_settings (
			id,
			default_provider,
			internal_default_provider,
			vector_store_backend,
			conversation_timeout_seconds,
			provider_timeout_seconds,
			message_threshold,
			token_threshold,
			max_context_tokens,
			max_injection_tokens,
			memory_relation_max_hops,
			memory_relation_hop_decay_lambda,
			memory_relation_hop_decay_exponent,
			rate_limit_rps,
			rate_limit_burst,
			cache_ttl_seconds,
			cors_allowed_origins,
			session_cookie_cross_site,
			multimodal_enabled,
			multimodal_storage_backend,
			multimodal_storage_local_root,
			multimodal_storage_bucket,
			multimodal_extract_image_model,
			multimodal_extract_audio_model,
			multimodal_extract_video_model,
			multimodal_extract_file_model,
			multimodal_audio_transcription_enabled,
			multimodal_video_frame_sampling_enabled,
			multimodal_max_asset_bytes,
			multimodal_allowed_mime_types,
			created_at,
			updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17::jsonb,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30::jsonb,$31,$31
		)
		ON CONFLICT (id) DO UPDATE SET
			default_provider = EXCLUDED.default_provider,
			internal_default_provider = EXCLUDED.internal_default_provider,
			vector_store_backend = EXCLUDED.vector_store_backend,
			conversation_timeout_seconds = EXCLUDED.conversation_timeout_seconds,
			provider_timeout_seconds = EXCLUDED.provider_timeout_seconds,
			message_threshold = EXCLUDED.message_threshold,
			token_threshold = EXCLUDED.token_threshold,
			max_context_tokens = EXCLUDED.max_context_tokens,
			max_injection_tokens = EXCLUDED.max_injection_tokens,
			memory_relation_max_hops = EXCLUDED.memory_relation_max_hops,
			memory_relation_hop_decay_lambda = EXCLUDED.memory_relation_hop_decay_lambda,
			memory_relation_hop_decay_exponent = EXCLUDED.memory_relation_hop_decay_exponent,
			rate_limit_rps = EXCLUDED.rate_limit_rps,
			rate_limit_burst = EXCLUDED.rate_limit_burst,
			cache_ttl_seconds = EXCLUDED.cache_ttl_seconds,
			cors_allowed_origins = EXCLUDED.cors_allowed_origins,
			session_cookie_cross_site = EXCLUDED.session_cookie_cross_site,
			multimodal_enabled = EXCLUDED.multimodal_enabled,
			multimodal_storage_backend = EXCLUDED.multimodal_storage_backend,
			multimodal_storage_local_root = EXCLUDED.multimodal_storage_local_root,
			multimodal_storage_bucket = EXCLUDED.multimodal_storage_bucket,
			multimodal_extract_image_model = EXCLUDED.multimodal_extract_image_model,
			multimodal_extract_audio_model = EXCLUDED.multimodal_extract_audio_model,
			multimodal_extract_video_model = EXCLUDED.multimodal_extract_video_model,
			multimodal_extract_file_model = EXCLUDED.multimodal_extract_file_model,
			multimodal_audio_transcription_enabled = EXCLUDED.multimodal_audio_transcription_enabled,
			multimodal_video_frame_sampling_enabled = EXCLUDED.multimodal_video_frame_sampling_enabled,
			multimodal_max_asset_bytes = EXCLUDED.multimodal_max_asset_bytes,
			multimodal_allowed_mime_types = EXCLUDED.multimodal_allowed_mime_types,
			updated_at = EXCLUDED.updated_at
	`,
		"global",
		nullableProvider(overrides.DefaultProvider),
		nullableProvider(overrides.InternalDefaultProvider),
		nullableString(overrides.VectorStoreBackend),
		nullableInt(overrides.ConversationTimeoutSeconds),
		nullableInt(overrides.ProviderTimeoutSeconds),
		nullableInt(overrides.MessageThreshold),
		nullableInt(overrides.TokenThreshold),
		nullableInt(overrides.MaxContextTokens),
		nullableInt(overrides.MaxInjectionTokens),
		nullableInt(overrides.MemoryRelationMaxHops),
		nullableFloat64(overrides.MemoryRelationHopDecayLambda),
		nullableFloat64(overrides.MemoryRelationHopDecayExponent),
		nullableInt(overrides.RateLimitRPS),
		nullableInt(overrides.RateLimitBurst),
		nullableInt(overrides.CacheTTLSeconds),
		corsJSON,
		nullableBool(overrides.SessionCookieCrossSite),
		nullableBool(overrides.MultimodalEnabled),
		nullableString(overrides.MultimodalStorageBackend),
		nullableString(overrides.MultimodalStorageLocalRoot),
		nullableString(overrides.MultimodalStorageBucket),
		nullableString(overrides.MultimodalExtractImageModel),
		nullableString(overrides.MultimodalExtractAudioModel),
		nullableString(overrides.MultimodalExtractVideoModel),
		nullableString(overrides.MultimodalExtractFileModel),
		nullableBool(overrides.MultimodalAudioTranscriptionEnabled),
		nullableBool(overrides.MultimodalVideoFrameSamplingEnabled),
		nullableInt64(overrides.MultimodalMaxAssetBytes),
		allowedMIMETypesJSON,
		now,
		now,
	)
	return err
}

func metadataForOverrides(overrides Overrides) map[string]FieldMeta {
	return map[string]FieldMeta{
		"default_provider": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.DefaultProvider != nil),
			Category:    "traffic",
			Description: "Fallback upstream provider set used by gateway proxy traffic when the request and user profile do not pin one explicitly.",
		},
		"internal_default_provider": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.InternalDefaultProvider != nil),
			Category:    "traffic",
			Description: "Fallback upstream provider set used by internal model-driven tasks when the configured model reference does not pin one explicitly.",
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
		"memory_relation_max_hops": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MemoryRelationMaxHops != nil),
			Category:    "memory",
			Description: "Maximum bounded graph depth used by online relation expansion during memory recall.",
		},
		"memory_relation_hop_decay_lambda": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MemoryRelationHopDecayLambda != nil),
			Category:    "memory",
			Description: "Strength of the stretched-exponential hop penalty; higher values suppress distant graph relations more aggressively.",
		},
		"memory_relation_hop_decay_exponent": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MemoryRelationHopDecayExponent != nil),
			Category:    "memory",
			Description: "Curvature of the hop penalty; values above 1 increase the distance penalty faster than linear growth.",
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
		"multimodal_enabled": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MultimodalEnabled != nil),
			Category:    "multimodal",
			Description: "Master switch for file/blob ingestion, timeline materialization, and multimodal extraction workers.",
		},
		"multimodal_storage_backend": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MultimodalStorageBackend != nil),
			Category:    "multimodal",
			Description: "Blob storage implementation used for retained source media.",
		},
		"multimodal_storage_local_root": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MultimodalStorageLocalRoot != nil),
			Category:    "multimodal",
			Description: "Filesystem root used by the local blob store backend in development and single-node deployments.",
		},
		"multimodal_storage_bucket": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MultimodalStorageBucket != nil),
			Category:    "multimodal",
			Description: "Bucket or container name used by remote blob storage backends.",
		},
		"multimodal_extract_image_model": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MultimodalExtractImageModel != nil),
			Category:    "multimodal",
			Description: "Configured image understanding model name used for captioning and OCR extraction.",
		},
		"multimodal_extract_audio_model": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MultimodalExtractAudioModel != nil),
			Category:    "multimodal",
			Description: "Configured audio model name used for transcription and optional audio summarization.",
		},
		"multimodal_extract_video_model": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MultimodalExtractVideoModel != nil),
			Category:    "multimodal",
			Description: "Configured video model name used for keyframe analysis and video-level summarization.",
		},
		"multimodal_extract_file_model": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MultimodalExtractFileModel != nil),
			Category:    "multimodal",
			Description: "Configured document extraction model used for scanned files and non-text-native document fallback.",
		},
		"multimodal_audio_transcription_enabled": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MultimodalAudioTranscriptionEnabled != nil),
			Category:    "multimodal",
			Description: "Enables transcript generation for supported audio inputs.",
		},
		"multimodal_video_frame_sampling_enabled": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MultimodalVideoFrameSamplingEnabled != nil),
			Category:    "multimodal",
			Description: "Enables frame sampling and visual summary extraction for video inputs.",
		},
		"multimodal_max_asset_bytes": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MultimodalMaxAssetBytes != nil),
			Category:    "multimodal",
			Description: "Maximum upload size accepted for retained multimodal assets before the proxy rejects the request.",
		},
		"multimodal_allowed_mime_types": {
			ApplyMode:   "restart",
			Source:      sourceFor(overrides.MultimodalAllowedMIMETypes != nil),
			Category:    "multimodal",
			Description: "Allow-list of MIME types accepted for hosted uploads and multimodal extraction.",
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
			value, ok := domain.ParseProvider(strings.TrimSpace(stringValue(raw)))
			if !ok {
				return Overrides{}, fmt.Errorf("unsupported default_provider: %s", raw)
			}
			next.DefaultProvider = &value
		case "internal_default_provider":
			if raw == nil {
				next.InternalDefaultProvider = nil
				continue
			}
			value, ok := domain.ParseProvider(strings.TrimSpace(stringValue(raw)))
			if !ok {
				return Overrides{}, fmt.Errorf("unsupported internal_default_provider: %s", raw)
			}
			next.InternalDefaultProvider = &value
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
		case "memory_relation_max_hops":
			value, err := intOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			if value != nil && (*value < 1 || *value > 4) {
				return Overrides{}, errors.New("memory_relation_max_hops must be between 1 and 4")
			}
			next.MemoryRelationMaxHops = value
		case "memory_relation_hop_decay_lambda":
			value, err := float64Override(raw)
			if err != nil {
				return Overrides{}, err
			}
			if value != nil && (*value < 0 || *value > 2) {
				return Overrides{}, errors.New("memory_relation_hop_decay_lambda must be between 0 and 2")
			}
			next.MemoryRelationHopDecayLambda = value
		case "memory_relation_hop_decay_exponent":
			value, err := float64Override(raw)
			if err != nil {
				return Overrides{}, err
			}
			if value != nil && (*value < 0.5 || *value > 3) {
				return Overrides{}, errors.New("memory_relation_hop_decay_exponent must be between 0.5 and 3")
			}
			next.MemoryRelationHopDecayExponent = value
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
		case "multimodal_enabled":
			value, err := boolOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.MultimodalEnabled = value
		case "multimodal_storage_backend":
			if raw == nil {
				next.MultimodalStorageBackend = nil
				continue
			}
			value := strings.TrimSpace(stringValue(raw))
			if value == "" {
				return Overrides{}, errors.New("multimodal_storage_backend cannot be empty")
			}
			next.MultimodalStorageBackend = &value
		case "multimodal_storage_local_root":
			if raw == nil {
				next.MultimodalStorageLocalRoot = nil
				continue
			}
			value := strings.TrimSpace(stringValue(raw))
			if value == "" {
				return Overrides{}, errors.New("multimodal_storage_local_root cannot be empty")
			}
			next.MultimodalStorageLocalRoot = &value
		case "multimodal_storage_bucket":
			if raw == nil {
				next.MultimodalStorageBucket = nil
				continue
			}
			value := strings.TrimSpace(stringValue(raw))
			if value == "" {
				return Overrides{}, errors.New("multimodal_storage_bucket cannot be empty")
			}
			next.MultimodalStorageBucket = &value
		case "multimodal_extract_image_model":
			if raw == nil {
				next.MultimodalExtractImageModel = nil
				continue
			}
			value := strings.TrimSpace(stringValue(raw))
			next.MultimodalExtractImageModel = &value
		case "multimodal_extract_audio_model":
			if raw == nil {
				next.MultimodalExtractAudioModel = nil
				continue
			}
			value := strings.TrimSpace(stringValue(raw))
			next.MultimodalExtractAudioModel = &value
		case "multimodal_extract_video_model":
			if raw == nil {
				next.MultimodalExtractVideoModel = nil
				continue
			}
			value := strings.TrimSpace(stringValue(raw))
			next.MultimodalExtractVideoModel = &value
		case "multimodal_extract_file_model":
			if raw == nil {
				next.MultimodalExtractFileModel = nil
				continue
			}
			value := strings.TrimSpace(stringValue(raw))
			next.MultimodalExtractFileModel = &value
		case "multimodal_audio_transcription_enabled":
			value, err := boolOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.MultimodalAudioTranscriptionEnabled = value
		case "multimodal_video_frame_sampling_enabled":
			value, err := boolOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.MultimodalVideoFrameSamplingEnabled = value
		case "multimodal_max_asset_bytes":
			value, err := int64Override(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.MultimodalMaxAssetBytes = value
		case "multimodal_allowed_mime_types":
			value, err := stringSliceOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.MultimodalAllowedMIMETypes = value
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

func int64Override(raw any) (*int64, error) {
	if raw == nil {
		return nil, nil
	}
	switch typed := raw.(type) {
	case int:
		value := int64(typed)
		return &value, nil
	case int32:
		value := int64(typed)
		return &value, nil
	case int64:
		value := typed
		return &value, nil
	case float64:
		value := int64(typed)
		return &value, nil
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return nil, err
		}
		value := parsed
		return &value, nil
	default:
		return nil, fmt.Errorf("expected integer, got %T", raw)
	}
}

func float64Override(raw any) (*float64, error) {
	if raw == nil {
		return nil, nil
	}
	switch typed := raw.(type) {
	case float32:
		value := float64(typed)
		return &value, nil
	case float64:
		value := typed
		return &value, nil
	case int:
		value := float64(typed)
		return &value, nil
	case int32:
		value := float64(typed)
		return &value, nil
	case int64:
		value := float64(typed)
		return &value, nil
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return nil, err
		}
		return &parsed, nil
	default:
		return nil, fmt.Errorf("expected number, got %T", raw)
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

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableFloat64(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
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
