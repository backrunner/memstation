package bootstrap

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"

	"memstation/apps/server/internal/domain"
)

const (
	DefaultDevAdminToken = "memstation-local-admin-token"
	DefaultDevMasterKey  = "memstation-local-master-key"
	DefaultDevAPIKey     = "ms-local-dev"
)

type Config struct {
	IngestionWindowBudget            int
	DeletionJournalKey               string
	DeletionJournalPath              string
	MemoryAIBackend                  string
	MemoryBrokerBaseURL              string
	MemoryBrokerCredential           string
	MemoryBrokerSummaryAlias         string
	MemoryBrokerAnalysisAlias        string
	MemoryBrokerEmbeddingAlias       string
	MemoryBrokerRerankAlias          string
	MemoryBrokerEmbeddingModel       string
	MemoryBrokerEmbeddingFingerprint string
	MemoryBrokerEmbeddingDimensions  int
	AppName                          string
	Version                          string
	Port                             int
	GRPCPort                         int
	PublicBaseURL                    string
	DefaultProvider                  domain.Provider
	InternalDefaultProvider          domain.Provider
	RuntimeStatePath                 string
	DatabaseURL                      string
	RedisURL                         string
	VectorStoreBackend               string
	ChatModel                        string
	SummaryModel                     string
	AnalysisModel                    string
	EmbeddingModel                   string
	ConversationTimeout              time.Duration
	MaxContextTokens                 int
	MaxInjectionTokens               int
	MemoryRelationMaxHops            int
	MemoryRelationMaxEdgesPerNode    int
	MemoryRelationMaxEdges           int
	MemoryRelationMaxNodes           int
	MemoryRelationTimeoutMS          int
	MemoryRelationHopDecayLambda     float64
	MemoryRelationHopDecayExponent   float64
	MessageThreshold                 int
	TokenThreshold                   int
	ShutdownTimeout                  time.Duration
	AdminToken                       string
	MasterKey                        string
	DevAPIKey                        string
	OpenAIAPIKey                     string
	AnthropicAPIKey                  string
	GeminiAPIKey                     string
	CohereAPIKey                     string
	JinaAPIKey                       string
	OpenAIBaseURL                    string
	AnthropicBaseURL                 string
	GeminiBaseURL                    string
	CohereBaseURL                    string
	JinaBaseURL                      string
	EmbeddingAPIKey                  string
	EmbeddingBaseURL                 string
	RerankModel                      string
	RerankCandidateMultiplier        int
	RerankMinCandidates              int
	RerankMaxCandidates              int
	RerankBlendWeight                float64
	ProviderTimeout                  time.Duration
	KeyMemoryWriteKeys               []string
	KeyMemoryAutoKeys                []string
	RateLimitRPS                     int
	RateLimitBurst                   int
	OTLPHTTPEndpoint                 string
	LogLevel                         string
	CORSAllowedOrigins               []string
	SessionCookieSecure              bool
	SessionCookieCrossSite           bool
	CacheTTL                         time.Duration
	Multimodal                       MultimodalConfig
}

type MultimodalConfig struct {
	Enabled                   bool
	StorageBackend            string
	StorageLocalRoot          string
	StorageBucket             string
	ImageModel                string
	AudioModel                string
	VideoModel                string
	FileModel                 string
	AudioTranscriptionEnabled bool
	VideoFrameSamplingEnabled bool
	MaxAssetBytes             int64
	AllowedMIMETypes          []string
}

func LoadConfig() (Config, error) {
	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	setDefault := func(key string, value any) { v.SetDefault(key, value) }
	bind := func(key string, envs ...string) {
		args := append([]string{key}, envs...)
		_ = v.BindEnv(args...)
	}

	bind("app_name", "MEMSTATION_APP_NAME")
	bind("version", "MEMSTATION_VERSION")
	bind("port", "MEMSTATION_PORT")
	bind("grpc_port", "MEMSTATION_GRPC_PORT")
	bind("public_base_url", "MEMSTATION_PUBLIC_BASE_URL")
	bind("default_provider", "MEMSTATION_DEFAULT_PROVIDER")
	bind("internal_default_provider", "MEMSTATION_INTERNAL_DEFAULT_PROVIDER")
	bind("runtime_state_path", "MEMSTATION_RUNTIME_STATE_PATH")
	bind("database_url", "DATABASE_URL")
	bind("redis_url", "REDIS_URL")
	bind("vector_store_backend", "VECTOR_STORE_BACKEND")
	bind("chat_model", "CHAT_MODEL")
	bind("summary_model", "SUMMARY_MODEL")
	bind("analysis_model", "ANALYSIS_MODEL")
	bind("embedding_model", "EMBEDDING_MODEL")
	bind("conversation_timeout_minutes", "CONVERSATION_TIMEOUT_MINUTES")
	bind("max_context_tokens", "MAX_CONTEXT_TOKENS")
	bind("max_injection_tokens", "MAX_INJECTION_TOKENS")
	bind("memory_relation_max_edges_per_node", "MEMSTATION_MEMORY_RELATION_MAX_EDGES_PER_NODE")
	bind("memory_relation_max_edges", "MEMSTATION_MEMORY_RELATION_MAX_EDGES")
	bind("memory_relation_max_nodes", "MEMSTATION_MEMORY_RELATION_MAX_NODES")
	bind("memory_relation_timeout_ms", "MEMSTATION_MEMORY_RELATION_TIMEOUT_MS")
	bind("memory_relation_max_hops", "MEMSTATION_MEMORY_RELATION_MAX_HOPS")
	bind("memory_relation_hop_decay_lambda", "MEMSTATION_MEMORY_RELATION_HOP_DECAY_LAMBDA")
	bind("memory_relation_hop_decay_exponent", "MEMSTATION_MEMORY_RELATION_HOP_DECAY_EXPONENT")
	bind("message_threshold", "MESSAGE_THRESHOLD")
	bind("token_threshold", "TOKEN_THRESHOLD")
	bind("shutdown_timeout_seconds", "MEMSTATION_SHUTDOWN_TIMEOUT_SECONDS")
	bind("admin_token", "MEMSTATION_ADMIN_TOKEN")
	bind("master_key", "MEMSTATION_MASTER_KEY")
	bind("memory_broker_embedding_dimensions", "MEMSTATION_MEMORY_BROKER_EMBEDDING_DIMENSIONS")
	bind("memory_broker_embedding_fingerprint", "MEMSTATION_MEMORY_BROKER_EMBEDDING_FINGERPRINT")
	bind("memory_broker_embedding_model", "MEMSTATION_MEMORY_BROKER_EMBEDDING_MODEL")
	bind("memory_broker_rerank_alias", "MEMSTATION_MEMORY_BROKER_RERANK_ALIAS")
	bind("memory_broker_embedding_alias", "MEMSTATION_MEMORY_BROKER_EMBEDDING_ALIAS")
	bind("memory_broker_analysis_alias", "MEMSTATION_MEMORY_BROKER_ANALYSIS_ALIAS")
	bind("memory_broker_summary_alias", "MEMSTATION_MEMORY_BROKER_SUMMARY_ALIAS")
	bind("memory_broker_credential", "MEMSTATION_MEMORY_BROKER_CREDENTIAL")
	bind("memory_broker_base_url", "MEMSTATION_MEMORY_BROKER_BASE_URL")
	bind("memory_ai_backend", "MEMSTATION_MEMORY_AI_BACKEND")
	bind("ingestion_window_budget", "MEMSTATION_INGESTION_WINDOW_BUDGET")
	bind("deletion_journal_path", "MEMSTATION_DELETION_JOURNAL_PATH")
	bind("deletion_journal_key", "MEMSTATION_DELETION_JOURNAL_KEY")
	bind("dev_api_key", "MEMSTATION_DEV_API_KEY")
	bind("openai_api_key", "OPENAI_API_KEY")
	bind("anthropic_api_key", "ANTHROPIC_API_KEY")
	bind("gemini_api_key", "GEMINI_API_KEY")
	bind("cohere_api_key", "COHERE_API_KEY")
	bind("jina_api_key", "JINA_API_KEY")
	bind("openai_base_url", "OPENAI_BASE_URL")
	bind("anthropic_base_url", "ANTHROPIC_BASE_URL")
	bind("gemini_base_url", "GEMINI_BASE_URL")
	bind("cohere_base_url", "COHERE_BASE_URL")
	bind("jina_base_url", "JINA_BASE_URL")
	bind("embedding_api_key", "EMBEDDING_API_KEY")
	bind("embedding_base_url", "EMBEDDING_BASE_URL")
	bind("rerank_model", "MEMSTATION_RERANK_MODEL")
	bind("rerank_candidate_multiplier", "MEMSTATION_RERANK_CANDIDATE_MULTIPLIER")
	bind("rerank_min_candidates", "MEMSTATION_RERANK_MIN_CANDIDATES")
	bind("rerank_max_candidates", "MEMSTATION_RERANK_MAX_CANDIDATES")
	bind("rerank_blend_weight", "MEMSTATION_RERANK_BLEND_WEIGHT")
	bind("provider_timeout_seconds", "MEMSTATION_PROVIDER_TIMEOUT_SECONDS")
	bind("key_memory_write_keys", "MEMSTATION_KEY_MEMORY_WRITE_KEYS")
	bind("key_memory_auto_keys", "MEMSTATION_KEY_MEMORY_AUTO_KEYS")
	bind("rate_limit_rps", "MEMSTATION_RATE_LIMIT_RPS")
	bind("rate_limit_burst", "MEMSTATION_RATE_LIMIT_BURST")
	bind("otlp_http_endpoint", "OTEL_EXPORTER_OTLP_ENDPOINT")
	bind("log_level", "MEMSTATION_LOG_LEVEL")
	bind("cors_allowed_origins", "MEMSTATION_CORS_ALLOWED_ORIGINS")
	bind("session_cookie_secure", "MEMSTATION_SESSION_COOKIE_SECURE")
	bind("session_cookie_cross_site", "MEMSTATION_SESSION_COOKIE_CROSS_SITE")
	bind("cache_ttl_seconds", "MEMSTATION_CACHE_TTL_SECONDS")
	bind("multimodal_enabled", "MEMSTATION_MULTIMODAL_ENABLED")
	bind("multimodal_storage_backend", "MEMSTATION_MULTIMODAL_STORAGE_BACKEND")
	bind("multimodal_storage_local_root", "MEMSTATION_MULTIMODAL_STORAGE_LOCAL_ROOT")
	bind("multimodal_storage_bucket", "MEMSTATION_MULTIMODAL_STORAGE_BUCKET")
	bind("multimodal_extract_image_model", "MEMSTATION_MULTIMODAL_EXTRACT_IMAGE_MODEL")
	bind("multimodal_extract_audio_model", "MEMSTATION_MULTIMODAL_EXTRACT_AUDIO_MODEL")
	bind("multimodal_extract_video_model", "MEMSTATION_MULTIMODAL_EXTRACT_VIDEO_MODEL")
	bind("multimodal_extract_file_model", "MEMSTATION_MULTIMODAL_EXTRACT_FILE_MODEL")
	bind("multimodal_audio_transcription_enabled", "MEMSTATION_MULTIMODAL_AUDIO_TRANSCRIPTION_ENABLED")
	bind("multimodal_video_frame_sampling_enabled", "MEMSTATION_MULTIMODAL_VIDEO_FRAME_SAMPLING_ENABLED")
	bind("multimodal_max_asset_bytes", "MEMSTATION_MULTIMODAL_MAX_ASSET_BYTES")
	bind("multimodal_allowed_mime_types", "MEMSTATION_MULTIMODAL_ALLOWED_MIME_TYPES")

	setDefault("app_name", "memstation")
	setDefault("version", "0.1.0")
	setDefault("port", 8080)
	setDefault("grpc_port", 9090)
	setDefault("default_provider", string(domain.ProviderOpenAICompatible))
	setDefault("internal_default_provider", string(domain.ProviderOpenAICompatible))
	setDefault("runtime_state_path", "./data/runtime-state.json")
	setDefault("vector_store_backend", "pgvector")
	setDefault("conversation_timeout_minutes", 15)
	setDefault("max_context_tokens", 8192)
	setDefault("max_injection_tokens", 2048)
	setDefault("memory_relation_max_edges_per_node", 24)
	setDefault("memory_relation_max_edges", 256)
	setDefault("memory_relation_max_nodes", 128)
	setDefault("memory_relation_timeout_ms", 150)
	setDefault("memory_relation_max_hops", 2)
	setDefault("memory_relation_hop_decay_lambda", 0.35)
	setDefault("memory_relation_hop_decay_exponent", 1.25)
	setDefault("message_threshold", 32)
	setDefault("token_threshold", 64000)
	setDefault("shutdown_timeout_seconds", 20)
	setDefault("admin_token", DefaultDevAdminToken)
	setDefault("master_key", DefaultDevMasterKey)
	setDefault("memory_broker_embedding_fingerprint", "")
	setDefault("memory_broker_embedding_model", "")
	setDefault("memory_broker_rerank_alias", "memory.rerank")
	setDefault("memory_broker_embedding_alias", "memory.embedding")
	setDefault("memory_broker_analysis_alias", "memory.analysis")
	setDefault("memory_broker_summary_alias", "memory.summary")
	setDefault("memory_broker_credential", "")
	setDefault("memory_broker_base_url", "")
	setDefault("memory_ai_backend", "direct")
	setDefault("ingestion_window_budget", 8192)
	setDefault("deletion_journal_path", "./data/deletion-journal/history.jsonl")
	setDefault("dev_api_key", DefaultDevAPIKey)
	setDefault("openai_base_url", "https://api.openai.com/v1")
	setDefault("anthropic_base_url", "https://api.anthropic.com/v1")
	setDefault("gemini_base_url", "https://generativelanguage.googleapis.com/v1beta")
	setDefault("cohere_base_url", "https://api.cohere.com/v2")
	setDefault("jina_base_url", "https://api.jina.ai/v1")
	setDefault("embedding_base_url", "https://api.openai.com/v1")
	setDefault("rerank_candidate_multiplier", 6)
	setDefault("rerank_min_candidates", 20)
	setDefault("rerank_max_candidates", 50)
	setDefault("rerank_blend_weight", 0.35)
	setDefault("provider_timeout_seconds", 60)
	setDefault("key_memory_write_keys", "recent_conversation")
	setDefault("key_memory_auto_keys", "identity,preferences,projects")
	setDefault("rate_limit_rps", 20)
	setDefault("rate_limit_burst", 40)
	setDefault("log_level", "info")
	setDefault("session_cookie_secure", false)
	setDefault("cache_ttl_seconds", 120)
	setDefault("multimodal_enabled", true)
	setDefault("multimodal_storage_backend", "local")
	setDefault("multimodal_storage_local_root", "./data/blobs")
	setDefault("multimodal_audio_transcription_enabled", true)
	setDefault("multimodal_video_frame_sampling_enabled", true)
	setDefault("multimodal_max_asset_bytes", 104857600)
	setDefault("multimodal_allowed_mime_types", strings.Join([]string{
		"image/png",
		"image/jpeg",
		"image/webp",
		"application/pdf",
		"text/plain",
		"text/markdown",
		"application/json",
		"audio/mpeg",
		"audio/wav",
		"audio/mp4",
		"video/mp4",
		"video/quicktime",
	}, ","))

	// Viper normally ignores empty environment values. This allowlist uses
	// an explicit empty value to disable automatic memory writes.
	if value, present := os.LookupEnv("MEMSTATION_KEY_MEMORY_AUTO_KEYS"); present {
		v.Set("key_memory_auto_keys", value)
	}
	cfg := Config{
		IngestionWindowBudget:            v.GetInt("ingestion_window_budget"),
		DeletionJournalKey:               v.GetString("deletion_journal_key"),
		DeletionJournalPath:              v.GetString("deletion_journal_path"),
		MemoryAIBackend:                  v.GetString("memory_ai_backend"),
		MemoryBrokerBaseURL:              v.GetString("memory_broker_base_url"),
		MemoryBrokerCredential:           v.GetString("memory_broker_credential"),
		MemoryBrokerSummaryAlias:         v.GetString("memory_broker_summary_alias"),
		MemoryBrokerAnalysisAlias:        v.GetString("memory_broker_analysis_alias"),
		MemoryBrokerEmbeddingAlias:       v.GetString("memory_broker_embedding_alias"),
		MemoryBrokerRerankAlias:          v.GetString("memory_broker_rerank_alias"),
		MemoryBrokerEmbeddingModel:       v.GetString("memory_broker_embedding_model"),
		MemoryBrokerEmbeddingFingerprint: v.GetString("memory_broker_embedding_fingerprint"),
		MemoryBrokerEmbeddingDimensions:  v.GetInt("memory_broker_embedding_dimensions"),
		AppName:                          v.GetString("app_name"),
		Version:                          v.GetString("version"),
		Port:                             v.GetInt("port"),
		GRPCPort:                         v.GetInt("grpc_port"),
		PublicBaseURL:                    strings.TrimSpace(v.GetString("public_base_url")),
		DefaultProvider:                  domain.NormalizeProvider(v.GetString("default_provider")),
		InternalDefaultProvider:          domain.NormalizeProvider(firstNonEmpty(v.GetString("internal_default_provider"), v.GetString("default_provider"))),
		RuntimeStatePath:                 strings.TrimSpace(v.GetString("runtime_state_path")),
		DatabaseURL:                      v.GetString("database_url"),
		RedisURL:                         v.GetString("redis_url"),
		VectorStoreBackend:               v.GetString("vector_store_backend"),
		ChatModel:                        v.GetString("chat_model"),
		SummaryModel:                     v.GetString("summary_model"),
		AnalysisModel:                    v.GetString("analysis_model"),
		EmbeddingModel:                   v.GetString("embedding_model"),
		ConversationTimeout:              time.Duration(v.GetInt("conversation_timeout_minutes")) * time.Minute,
		MaxContextTokens:                 v.GetInt("max_context_tokens"),
		MaxInjectionTokens:               v.GetInt("max_injection_tokens"),
		MemoryRelationMaxEdgesPerNode:    v.GetInt("memory_relation_max_edges_per_node"),
		MemoryRelationMaxEdges:           v.GetInt("memory_relation_max_edges"),
		MemoryRelationMaxNodes:           v.GetInt("memory_relation_max_nodes"),
		MemoryRelationTimeoutMS:          v.GetInt("memory_relation_timeout_ms"),
		MemoryRelationMaxHops:            v.GetInt("memory_relation_max_hops"),
		MemoryRelationHopDecayLambda:     v.GetFloat64("memory_relation_hop_decay_lambda"),
		MemoryRelationHopDecayExponent:   v.GetFloat64("memory_relation_hop_decay_exponent"),
		MessageThreshold:                 v.GetInt("message_threshold"),
		TokenThreshold:                   v.GetInt("token_threshold"),
		ShutdownTimeout:                  time.Duration(v.GetInt("shutdown_timeout_seconds")) * time.Second,
		AdminToken:                       v.GetString("admin_token"),
		MasterKey:                        v.GetString("master_key"),
		DevAPIKey:                        v.GetString("dev_api_key"),
		OpenAIAPIKey:                     v.GetString("openai_api_key"),
		AnthropicAPIKey:                  v.GetString("anthropic_api_key"),
		GeminiAPIKey:                     v.GetString("gemini_api_key"),
		CohereAPIKey:                     v.GetString("cohere_api_key"),
		JinaAPIKey:                       v.GetString("jina_api_key"),
		OpenAIBaseURL:                    v.GetString("openai_base_url"),
		AnthropicBaseURL:                 v.GetString("anthropic_base_url"),
		GeminiBaseURL:                    v.GetString("gemini_base_url"),
		CohereBaseURL:                    v.GetString("cohere_base_url"),
		JinaBaseURL:                      v.GetString("jina_base_url"),
		EmbeddingAPIKey:                  firstNonEmpty(v.GetString("embedding_api_key"), v.GetString("openai_api_key")),
		EmbeddingBaseURL:                 firstNonEmpty(v.GetString("embedding_base_url"), v.GetString("openai_base_url")),
		RerankModel:                      strings.TrimSpace(v.GetString("rerank_model")),
		RerankCandidateMultiplier:        v.GetInt("rerank_candidate_multiplier"),
		RerankMinCandidates:              v.GetInt("rerank_min_candidates"),
		RerankMaxCandidates:              v.GetInt("rerank_max_candidates"),
		RerankBlendWeight:                v.GetFloat64("rerank_blend_weight"),
		ProviderTimeout:                  time.Duration(v.GetInt("provider_timeout_seconds")) * time.Second,
		KeyMemoryWriteKeys:               csvList(v.GetString("key_memory_write_keys")),
		KeyMemoryAutoKeys:                csvList(v.GetString("key_memory_auto_keys")),
		RateLimitRPS:                     v.GetInt("rate_limit_rps"),
		RateLimitBurst:                   v.GetInt("rate_limit_burst"),
		OTLPHTTPEndpoint:                 v.GetString("otlp_http_endpoint"),
		LogLevel:                         v.GetString("log_level"),
		CORSAllowedOrigins:               csvList(v.GetString("cors_allowed_origins")),
		SessionCookieSecure:              v.GetBool("session_cookie_secure"),
		SessionCookieCrossSite:           v.GetBool("session_cookie_cross_site"),
		CacheTTL:                         time.Duration(v.GetInt("cache_ttl_seconds")) * time.Second,
		Multimodal: MultimodalConfig{
			Enabled:                   v.GetBool("multimodal_enabled"),
			StorageBackend:            v.GetString("multimodal_storage_backend"),
			StorageLocalRoot:          v.GetString("multimodal_storage_local_root"),
			StorageBucket:             v.GetString("multimodal_storage_bucket"),
			ImageModel:                v.GetString("multimodal_extract_image_model"),
			AudioModel:                v.GetString("multimodal_extract_audio_model"),
			VideoModel:                v.GetString("multimodal_extract_video_model"),
			FileModel:                 v.GetString("multimodal_extract_file_model"),
			AudioTranscriptionEnabled: v.GetBool("multimodal_audio_transcription_enabled"),
			VideoFrameSamplingEnabled: v.GetBool("multimodal_video_frame_sampling_enabled"),
			MaxAssetBytes:             v.GetInt64("multimodal_max_asset_bytes"),
			AllowedMIMETypes:          csvList(v.GetString("multimodal_allowed_mime_types")),
		},
	}
	if err := validateCredentialedCORSOrigins(cfg.CORSAllowedOrigins); err != nil {
		return Config{}, err
	}
	if cfg.SessionCookieCrossSite && !cfg.SessionCookieSecure {
		return Config{}, fmt.Errorf("session_cookie_secure must be true when session_cookie_cross_site is true")
	}
	return cfg, nil
}

func validateCredentialedCORSOrigins(origins []string) error {
	for _, origin := range origins {
		if strings.TrimSpace(origin) == "*" {
			return fmt.Errorf("cors_allowed_origins must list explicit origins when admin sessions use cookies")
		}
	}
	return nil
}

func csvList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
