package systemconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
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
	RerankModel                         string          `json:"rerank_model"`
	RerankCandidateMultiplier           int             `json:"rerank_candidate_multiplier"`
	RerankMinCandidates                 int             `json:"rerank_min_candidates"`
	RerankMaxCandidates                 int             `json:"rerank_max_candidates"`
	MemoryRelationMaxHops               int             `json:"memory_relation_max_hops"`
	MemoryRelationHopDecayLambda        float64         `json:"memory_relation_hop_decay_lambda"`
	MemoryRelationHopDecayExponent      float64         `json:"memory_relation_hop_decay_exponent"`
	RateLimitRPS                        int             `json:"rate_limit_rps"`
	RateLimitBurst                      int             `json:"rate_limit_burst"`
	CacheTTLSeconds                     int             `json:"cache_ttl_seconds"`
	CORSAllowedOrigins                  []string        `json:"cors_allowed_origins"`
	SessionCookieSecure                 bool            `json:"-"`
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
	Validate                   func(Snapshot) error
	SetDefaultProvider         func(domain.Provider)
	SetInternalDefaultProvider func(domain.Provider)
	SetVectorStoreBackend      func(string)
	SetThresholds              func(messageThreshold, tokenThreshold int)
	SetConversationTimeout     func(time.Duration)
	SetProviderTimeout         func(time.Duration)
	SetMaxContextTokens        func(int)
	SetMaxInjectionTokens      func(int)
	SetRerankConfig            func(model string, candidateMultiplier, minCandidates, maxCandidates int)
	SetMemoryRelationRecall    func(maxHops int, decayLambda, decayExponent float64)
	SetRateLimit               func(rps, burst int)
	SetCacheTTL                func(time.Duration)
	SetMultimodalConfig        func(bootstrap.MultimodalConfig)
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
	RerankModel                         *string
	RerankCandidateMultiplier           *int
	RerankMinCandidates                 *int
	RerankMaxCandidates                 *int
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
	store    *runtimeconfig.FileStore
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
		RerankModel:                         cfg.RerankModel,
		RerankCandidateMultiplier:           cfg.RerankCandidateMultiplier,
		RerankMinCandidates:                 cfg.RerankMinCandidates,
		RerankMaxCandidates:                 cfg.RerankMaxCandidates,
		MemoryRelationMaxHops:               cfg.MemoryRelationMaxHops,
		MemoryRelationHopDecayLambda:        cfg.MemoryRelationHopDecayLambda,
		MemoryRelationHopDecayExponent:      cfg.MemoryRelationHopDecayExponent,
		RateLimitRPS:                        cfg.RateLimitRPS,
		RateLimitBurst:                      cfg.RateLimitBurst,
		CacheTTLSeconds:                     int(cfg.CacheTTL.Seconds()),
		CORSAllowedOrigins:                  append([]string(nil), cfg.CORSAllowedOrigins...),
		SessionCookieSecure:                 cfg.SessionCookieSecure,
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

func NewService(store *runtimeconfig.FileStore, defaults Snapshot) *Service {
	return &Service{
		store:    store,
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
	if err := validateSnapshot(desired); err != nil {
		return Snapshot{}, err
	}

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
	desired := nextOverrides.Apply(s.defaults)
	if err := validateSnapshot(desired); err != nil {
		return nil, err
	}
	if hooks.Validate != nil {
		if err := hooks.Validate(desired); err != nil {
			return nil, err
		}
	}
	if err := s.persistOverrides(ctx, nextOverrides); err != nil {
		return nil, err
	}

	live := currentLive

	if hooks.SetDefaultProvider != nil {
		hooks.SetDefaultProvider(desired.DefaultProvider)
		live.DefaultProvider = desired.DefaultProvider
	}
	if hooks.SetInternalDefaultProvider != nil {
		hooks.SetInternalDefaultProvider(desired.InternalDefaultProvider)
		live.InternalDefaultProvider = desired.InternalDefaultProvider
	}
	if hooks.SetVectorStoreBackend != nil {
		hooks.SetVectorStoreBackend(desired.VectorStoreBackend)
		live.VectorStoreBackend = desired.VectorStoreBackend
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
	if hooks.SetProviderTimeout != nil {
		hooks.SetProviderTimeout(time.Duration(desired.ProviderTimeoutSeconds) * time.Second)
		live.ProviderTimeoutSeconds = desired.ProviderTimeoutSeconds
	}
	if hooks.SetMaxContextTokens != nil {
		hooks.SetMaxContextTokens(desired.MaxContextTokens)
		live.MaxContextTokens = desired.MaxContextTokens
	}
	if hooks.SetMaxInjectionTokens != nil {
		hooks.SetMaxInjectionTokens(desired.MaxInjectionTokens)
		live.MaxInjectionTokens = desired.MaxInjectionTokens
	}
	if hooks.SetRerankConfig != nil {
		hooks.SetRerankConfig(
			desired.RerankModel,
			desired.RerankCandidateMultiplier,
			desired.RerankMinCandidates,
			desired.RerankMaxCandidates,
		)
		live.RerankModel = desired.RerankModel
		live.RerankCandidateMultiplier = desired.RerankCandidateMultiplier
		live.RerankMinCandidates = desired.RerankMinCandidates
		live.RerankMaxCandidates = desired.RerankMaxCandidates
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
	if hooks.SetCacheTTL != nil {
		hooks.SetCacheTTL(time.Duration(desired.CacheTTLSeconds) * time.Second)
		live.CacheTTLSeconds = desired.CacheTTLSeconds
	}
	live.CORSAllowedOrigins = append([]string(nil), desired.CORSAllowedOrigins...)
	live.SessionCookieCrossSite = desired.SessionCookieCrossSite
	if hooks.SetMultimodalConfig != nil {
		hooks.SetMultimodalConfig(desired.MultimodalConfig())
		live.MultimodalEnabled = desired.MultimodalEnabled
		live.MultimodalStorageBackend = desired.MultimodalStorageBackend
		live.MultimodalStorageLocalRoot = desired.MultimodalStorageLocalRoot
		live.MultimodalStorageBucket = desired.MultimodalStorageBucket
		live.MultimodalExtractImageModel = desired.MultimodalExtractImageModel
		live.MultimodalExtractAudioModel = desired.MultimodalExtractAudioModel
		live.MultimodalExtractVideoModel = desired.MultimodalExtractVideoModel
		live.MultimodalExtractFileModel = desired.MultimodalExtractFileModel
		live.MultimodalAudioTranscriptionEnabled = desired.MultimodalAudioTranscriptionEnabled
		live.MultimodalVideoFrameSamplingEnabled = desired.MultimodalVideoFrameSamplingEnabled
		live.MultimodalMaxAssetBytes = desired.MultimodalMaxAssetBytes
		live.MultimodalAllowedMIMETypes = append([]string(nil), desired.MultimodalAllowedMIMETypes...)
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
		"rerank_model":                            s.RerankModel,
		"rerank_candidate_multiplier":             s.RerankCandidateMultiplier,
		"rerank_min_candidates":                   s.RerankMinCandidates,
		"rerank_max_candidates":                   s.RerankMaxCandidates,
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
	cfg.RerankModel = s.RerankModel
	cfg.RerankCandidateMultiplier = s.RerankCandidateMultiplier
	cfg.RerankMinCandidates = s.RerankMinCandidates
	cfg.RerankMaxCandidates = s.RerankMaxCandidates
	cfg.MemoryRelationMaxHops = s.MemoryRelationMaxHops
	cfg.MemoryRelationHopDecayLambda = s.MemoryRelationHopDecayLambda
	cfg.MemoryRelationHopDecayExponent = s.MemoryRelationHopDecayExponent
	cfg.RateLimitRPS = s.RateLimitRPS
	cfg.RateLimitBurst = s.RateLimitBurst
	cfg.CacheTTL = time.Duration(s.CacheTTLSeconds) * time.Second
	cfg.CORSAllowedOrigins = append([]string(nil), s.CORSAllowedOrigins...)
	cfg.SessionCookieSecure = s.SessionCookieSecure
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

func (s Snapshot) MultimodalConfig() bootstrap.MultimodalConfig {
	return s.ApplyConfig(bootstrap.Config{}).Multimodal
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
	if o.RerankModel != nil {
		snapshot.RerankModel = *o.RerankModel
	}
	if o.RerankCandidateMultiplier != nil {
		snapshot.RerankCandidateMultiplier = *o.RerankCandidateMultiplier
	}
	if o.RerankMinCandidates != nil {
		snapshot.RerankMinCandidates = *o.RerankMinCandidates
	}
	if o.RerankMaxCandidates != nil {
		snapshot.RerankMaxCandidates = *o.RerankMaxCandidates
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
	if s.store == nil {
		return Overrides{}, nil
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return Overrides{}, err
	}
	fields := map[string]any{}
	for key, raw := range state.System {
		if len(raw) == 0 {
			fields[key] = nil
			continue
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return Overrides{}, err
		}
		fields[key] = decoded
	}
	return applyFieldOverrides(Overrides{}, fields)
}

func (s *Service) persistOverrides(ctx context.Context, overrides Overrides) error {
	if s.store == nil {
		return nil
	}
	fields, err := overridesToPersistedMap(overrides)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, func(state *runtimeconfig.State) error {
		state.System = map[string]json.RawMessage{}
		for key, value := range fields {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			state.System[key] = raw
		}
		return nil
	})
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
			ApplyMode:   "live",
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
			ApplyMode:   "live",
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
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MaxContextTokens != nil),
			Category:    "memory",
			Description: "Upper budget available to request assembly before upstream truncation or rejection.",
		},
		"max_injection_tokens": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MaxInjectionTokens != nil),
			Category:    "memory",
			Description: "Injection budget used by memory context assembly and related defaults.",
		},
		"rerank_model": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.RerankModel != nil),
			Category:    "memory",
			Description: "Reranker model reference: cohere:model, jina:model, openrouter:vendor/model or rerank_compatible:model. Leave empty to disable.",
		},
		"rerank_candidate_multiplier": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.RerankCandidateMultiplier != nil),
			Category:    "memory",
			Description: "Multiplier used to widen vector recall before reranking the candidate set.",
		},
		"rerank_min_candidates": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.RerankMinCandidates != nil),
			Category:    "memory",
			Description: "Minimum number of vector candidates promoted into the reranking stage.",
		},
		"rerank_max_candidates": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.RerankMaxCandidates != nil),
			Category:    "memory",
			Description: "Maximum number of retrieved candidates submitted to the reranker.",
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
			ApplyMode:   "live",
			Source:      sourceFor(overrides.CacheTTLSeconds != nil),
			Category:    "memory",
			Description: "Default TTL for Redis-backed and process-local JSON cache entries.",
		},
		"cors_allowed_origins": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.CORSAllowedOrigins != nil),
			Category:    "web",
			Description: "Optional cross-origin whitelist for admin and public APIs. Empty reflects the incoming Origin.",
		},
		"session_cookie_cross_site": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.SessionCookieCrossSite != nil),
			Category:    "web",
			Description: "Controls whether the admin session cookie is issued with cross-site semantics.",
		},
		"multimodal_enabled": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MultimodalEnabled != nil),
			Category:    "multimodal",
			Description: "Master switch for file/blob ingestion, timeline materialization, and multimodal extraction workers.",
		},
		"multimodal_storage_backend": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MultimodalStorageBackend != nil),
			Category:    "multimodal",
			Description: "Blob storage implementation used for retained source media.",
		},
		"multimodal_storage_local_root": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MultimodalStorageLocalRoot != nil),
			Category:    "multimodal",
			Description: "Filesystem root used by the local blob store backend in development and single-node deployments.",
		},
		"multimodal_storage_bucket": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MultimodalStorageBucket != nil),
			Category:    "multimodal",
			Description: "Bucket or container name used by remote blob storage backends.",
		},
		"multimodal_extract_image_model": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MultimodalExtractImageModel != nil),
			Category:    "multimodal",
			Description: "Configured image understanding model name used for captioning and OCR extraction.",
		},
		"multimodal_extract_audio_model": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MultimodalExtractAudioModel != nil),
			Category:    "multimodal",
			Description: "Configured audio model name used for transcription and optional audio summarization.",
		},
		"multimodal_extract_video_model": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MultimodalExtractVideoModel != nil),
			Category:    "multimodal",
			Description: "Configured video model name used for keyframe analysis and video-level summarization.",
		},
		"multimodal_extract_file_model": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MultimodalExtractFileModel != nil),
			Category:    "multimodal",
			Description: "Configured document extraction model used for scanned files and non-text-native document fallback.",
		},
		"multimodal_audio_transcription_enabled": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MultimodalAudioTranscriptionEnabled != nil),
			Category:    "multimodal",
			Description: "Enables transcript generation for supported audio inputs.",
		},
		"multimodal_video_frame_sampling_enabled": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MultimodalVideoFrameSamplingEnabled != nil),
			Category:    "multimodal",
			Description: "Enables frame sampling and visual summary extraction for video inputs.",
		},
		"multimodal_max_asset_bytes": {
			ApplyMode:   "live",
			Source:      sourceFor(overrides.MultimodalMaxAssetBytes != nil),
			Category:    "multimodal",
			Description: "Maximum upload size accepted for retained multimodal assets before the proxy rejects the request.",
		},
		"multimodal_allowed_mime_types": {
			ApplyMode:   "live",
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
		case "rerank_model":
			if raw == nil {
				next.RerankModel = nil
				continue
			}
			value := strings.TrimSpace(stringValue(raw))
			if err := validateRerankModelReference(value); err != nil {
				return Overrides{}, err
			}
			next.RerankModel = &value
		case "rerank_candidate_multiplier":
			value, err := intOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.RerankCandidateMultiplier = value
		case "rerank_min_candidates":
			value, err := intOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.RerankMinCandidates = value
		case "rerank_max_candidates":
			value, err := intOverride(raw)
			if err != nil {
				return Overrides{}, err
			}
			next.RerankMaxCandidates = value
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
			if err := validateCORSAllowedOrigins(value); err != nil {
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
		default:
			return Overrides{}, fmt.Errorf("unsupported runtime field: %s", key)
		}
	}
	if next.RerankCandidateMultiplier != nil && (*next.RerankCandidateMultiplier < 1 || *next.RerankCandidateMultiplier > 16) {
		return Overrides{}, errors.New("rerank_candidate_multiplier must be between 1 and 16")
	}
	if next.RerankMinCandidates != nil && *next.RerankMinCandidates < 1 {
		return Overrides{}, errors.New("rerank_min_candidates must be at least 1")
	}
	if next.RerankMaxCandidates != nil && *next.RerankMaxCandidates < 1 {
		return Overrides{}, errors.New("rerank_max_candidates must be at least 1")
	}
	if next.RerankMinCandidates != nil && next.RerankMaxCandidates != nil && *next.RerankMaxCandidates < *next.RerankMinCandidates {
		return Overrides{}, errors.New("rerank_max_candidates must be greater than or equal to rerank_min_candidates")
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

func validateCORSAllowedOrigins(origins *[]string) error {
	if origins == nil {
		return nil
	}
	for _, origin := range *origins {
		if strings.TrimSpace(origin) == "*" {
			return errors.New("cors_allowed_origins must list explicit origins when admin sessions use cookies")
		}
	}
	return nil
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

func validateRerankModelReference(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	_, _, err := domain.ParseRerankModel(value)
	return err
}

func validateSnapshot(snapshot Snapshot) error {
	if normalized := strings.TrimSpace(strings.ToLower(snapshot.VectorStoreBackend)); normalized != "" && normalized != "pgvector" {
		return fmt.Errorf("vector_store_backend %q is not supported", snapshot.VectorStoreBackend)
	}
	if snapshot.ProviderTimeoutSeconds <= 0 {
		return errors.New("provider_timeout_seconds must be greater than 0")
	}
	if snapshot.MaxContextTokens <= 0 {
		return errors.New("max_context_tokens must be greater than 0")
	}
	if snapshot.MaxInjectionTokens <= 0 {
		return errors.New("max_injection_tokens must be greater than 0")
	}
	if snapshot.MaxInjectionTokens > snapshot.MaxContextTokens {
		return errors.New("max_injection_tokens must be less than or equal to max_context_tokens")
	}
	if snapshot.CacheTTLSeconds <= 0 {
		return errors.New("cache_ttl_seconds must be greater than 0")
	}
	backend := strings.TrimSpace(strings.ToLower(snapshot.MultimodalStorageBackend))
	switch backend {
	case "", "local":
		if strings.TrimSpace(snapshot.MultimodalStorageLocalRoot) == "" {
			return errors.New("multimodal_storage_local_root cannot be empty when using the local storage backend")
		}
	default:
		return fmt.Errorf("multimodal_storage_backend %q is not supported", snapshot.MultimodalStorageBackend)
	}
	if snapshot.RerankCandidateMultiplier < 1 || snapshot.RerankCandidateMultiplier > 16 {
		return errors.New("rerank_candidate_multiplier must be between 1 and 16")
	}
	if snapshot.RerankMinCandidates < 1 {
		return errors.New("rerank_min_candidates must be at least 1")
	}
	if snapshot.RerankMaxCandidates < 1 {
		return errors.New("rerank_max_candidates must be at least 1")
	}
	if snapshot.RerankMaxCandidates < snapshot.RerankMinCandidates {
		return errors.New("rerank_max_candidates must be greater than or equal to rerank_min_candidates")
	}
	if err := validateCORSAllowedOrigins(&snapshot.CORSAllowedOrigins); err != nil {
		return err
	}
	if snapshot.SessionCookieCrossSite && !snapshot.SessionCookieSecure {
		return errors.New("session_cookie_secure must be true when session_cookie_cross_site is true")
	}
	return validateRerankModelReference(snapshot.RerankModel)
}

func overridesToPersistedMap(overrides Overrides) (map[string]any, error) {
	out := map[string]any{}
	if value := nullableProvider(overrides.DefaultProvider); value != nil {
		out["default_provider"] = value
	}
	if value := nullableProvider(overrides.InternalDefaultProvider); value != nil {
		out["internal_default_provider"] = value
	}
	if value := nullableString(overrides.VectorStoreBackend); value != nil {
		out["vector_store_backend"] = value
	}
	if value := nullableInt(overrides.ConversationTimeoutSeconds); value != nil {
		out["conversation_timeout_seconds"] = value
	}
	if value := nullableInt(overrides.ProviderTimeoutSeconds); value != nil {
		out["provider_timeout_seconds"] = value
	}
	if value := nullableInt(overrides.MessageThreshold); value != nil {
		out["message_threshold"] = value
	}
	if value := nullableInt(overrides.TokenThreshold); value != nil {
		out["token_threshold"] = value
	}
	if value := nullableInt(overrides.MaxContextTokens); value != nil {
		out["max_context_tokens"] = value
	}
	if value := nullableInt(overrides.MaxInjectionTokens); value != nil {
		out["max_injection_tokens"] = value
	}
	if value := nullableString(overrides.RerankModel); value != nil {
		out["rerank_model"] = value
	}
	if value := nullableInt(overrides.RerankCandidateMultiplier); value != nil {
		out["rerank_candidate_multiplier"] = value
	}
	if value := nullableInt(overrides.RerankMinCandidates); value != nil {
		out["rerank_min_candidates"] = value
	}
	if value := nullableInt(overrides.RerankMaxCandidates); value != nil {
		out["rerank_max_candidates"] = value
	}
	if value := nullableInt(overrides.MemoryRelationMaxHops); value != nil {
		out["memory_relation_max_hops"] = value
	}
	if value := nullableFloat64(overrides.MemoryRelationHopDecayLambda); value != nil {
		out["memory_relation_hop_decay_lambda"] = value
	}
	if value := nullableFloat64(overrides.MemoryRelationHopDecayExponent); value != nil {
		out["memory_relation_hop_decay_exponent"] = value
	}
	if value := nullableInt(overrides.RateLimitRPS); value != nil {
		out["rate_limit_rps"] = value
	}
	if value := nullableInt(overrides.RateLimitBurst); value != nil {
		out["rate_limit_burst"] = value
	}
	if value := nullableInt(overrides.CacheTTLSeconds); value != nil {
		out["cache_ttl_seconds"] = value
	}
	if overrides.CORSAllowedOrigins != nil {
		out["cors_allowed_origins"] = append([]string(nil), (*overrides.CORSAllowedOrigins)...)
	}
	if value := nullableBool(overrides.SessionCookieCrossSite); value != nil {
		out["session_cookie_cross_site"] = value
	}
	if value := nullableBool(overrides.MultimodalEnabled); value != nil {
		out["multimodal_enabled"] = value
	}
	if value := nullableString(overrides.MultimodalStorageBackend); value != nil {
		out["multimodal_storage_backend"] = value
	}
	if value := nullableString(overrides.MultimodalStorageLocalRoot); value != nil {
		out["multimodal_storage_local_root"] = value
	}
	if value := nullableString(overrides.MultimodalStorageBucket); value != nil {
		out["multimodal_storage_bucket"] = value
	}
	if value := nullableString(overrides.MultimodalExtractImageModel); value != nil {
		out["multimodal_extract_image_model"] = value
	}
	if value := nullableString(overrides.MultimodalExtractAudioModel); value != nil {
		out["multimodal_extract_audio_model"] = value
	}
	if value := nullableString(overrides.MultimodalExtractVideoModel); value != nil {
		out["multimodal_extract_video_model"] = value
	}
	if value := nullableString(overrides.MultimodalExtractFileModel); value != nil {
		out["multimodal_extract_file_model"] = value
	}
	if value := nullableBool(overrides.MultimodalAudioTranscriptionEnabled); value != nil {
		out["multimodal_audio_transcription_enabled"] = value
	}
	if value := nullableBool(overrides.MultimodalVideoFrameSamplingEnabled); value != nil {
		out["multimodal_video_frame_sampling_enabled"] = value
	}
	if value := nullableInt64(overrides.MultimodalMaxAssetBytes); value != nil {
		out["multimodal_max_asset_bytes"] = value
	}
	if overrides.MultimodalAllowedMIMETypes != nil {
		out["multimodal_allowed_mime_types"] = append([]string(nil), (*overrides.MultimodalAllowedMIMETypes)...)
	}
	return out, nil
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
