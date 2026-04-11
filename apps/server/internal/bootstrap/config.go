package bootstrap

import (
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
	AppName                        string
	Version                        string
	Port                           int
	GRPCPort                       int
	DefaultProvider                domain.Provider
	InternalDefaultProvider        domain.Provider
	DatabaseURL                    string
	RedisURL                       string
	VectorStoreBackend             string
	ChatModel                      string
	SummaryModel                   string
	AnalysisModel                  string
	EmbeddingModel                 string
	ConversationTimeout            time.Duration
	MaxContextTokens               int
	MaxInjectionTokens             int
	MemoryRelationMaxHops          int
	MemoryRelationHopDecayLambda   float64
	MemoryRelationHopDecayExponent float64
	MessageThreshold               int
	TokenThreshold                 int
	ShutdownTimeout                time.Duration
	AdminToken                     string
	MasterKey                      string
	DevAPIKey                      string
	OpenAIAPIKey                   string
	AnthropicAPIKey                string
	GeminiAPIKey                   string
	OpenAIBaseURL                  string
	AnthropicBaseURL               string
	GeminiBaseURL                  string
	EmbeddingAPIKey                string
	EmbeddingBaseURL               string
	ProviderTimeout                time.Duration
	KeyMemoryWriteKeys             []string
	RateLimitRPS                   int
	RateLimitBurst                 int
	OTLPHTTPEndpoint               string
	LogLevel                       string
	CORSAllowedOrigins             []string
	SessionCookieSecure            bool
	SessionCookieCrossSite         bool
	CacheTTL                       time.Duration
	Multimodal                     MultimodalConfig
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
	bind("default_provider", "MEMSTATION_DEFAULT_PROVIDER")
	bind("internal_default_provider", "MEMSTATION_INTERNAL_DEFAULT_PROVIDER")
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
	bind("memory_relation_max_hops", "MEMSTATION_MEMORY_RELATION_MAX_HOPS")
	bind("memory_relation_hop_decay_lambda", "MEMSTATION_MEMORY_RELATION_HOP_DECAY_LAMBDA")
	bind("memory_relation_hop_decay_exponent", "MEMSTATION_MEMORY_RELATION_HOP_DECAY_EXPONENT")
	bind("message_threshold", "MESSAGE_THRESHOLD")
	bind("token_threshold", "TOKEN_THRESHOLD")
	bind("shutdown_timeout_seconds", "MEMSTATION_SHUTDOWN_TIMEOUT_SECONDS")
	bind("admin_token", "MEMSTATION_ADMIN_TOKEN")
	bind("master_key", "MEMSTATION_MASTER_KEY")
	bind("dev_api_key", "MEMSTATION_DEV_API_KEY")
	bind("openai_api_key", "OPENAI_API_KEY")
	bind("anthropic_api_key", "ANTHROPIC_API_KEY")
	bind("gemini_api_key", "GEMINI_API_KEY")
	bind("openai_base_url", "OPENAI_BASE_URL")
	bind("anthropic_base_url", "ANTHROPIC_BASE_URL")
	bind("gemini_base_url", "GEMINI_BASE_URL")
	bind("embedding_api_key", "EMBEDDING_API_KEY")
	bind("embedding_base_url", "EMBEDDING_BASE_URL")
	bind("provider_timeout_seconds", "MEMSTATION_PROVIDER_TIMEOUT_SECONDS")
	bind("key_memory_write_keys", "MEMSTATION_KEY_MEMORY_WRITE_KEYS")
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
	setDefault("vector_store_backend", "pgvector")
	setDefault("conversation_timeout_minutes", 15)
	setDefault("max_context_tokens", 8192)
	setDefault("max_injection_tokens", 2048)
	setDefault("memory_relation_max_hops", 2)
	setDefault("memory_relation_hop_decay_lambda", 0.35)
	setDefault("memory_relation_hop_decay_exponent", 1.25)
	setDefault("message_threshold", 32)
	setDefault("token_threshold", 64000)
	setDefault("shutdown_timeout_seconds", 20)
	setDefault("admin_token", DefaultDevAdminToken)
	setDefault("master_key", DefaultDevMasterKey)
	setDefault("dev_api_key", DefaultDevAPIKey)
	setDefault("openai_base_url", "https://api.openai.com/v1")
	setDefault("anthropic_base_url", "https://api.anthropic.com/v1")
	setDefault("gemini_base_url", "https://generativelanguage.googleapis.com/v1beta")
	setDefault("embedding_base_url", "https://api.openai.com/v1")
	setDefault("provider_timeout_seconds", 60)
	setDefault("key_memory_write_keys", "recent_conversation")
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

	return Config{
		AppName:                        v.GetString("app_name"),
		Version:                        v.GetString("version"),
		Port:                           v.GetInt("port"),
		GRPCPort:                       v.GetInt("grpc_port"),
		DefaultProvider:                domain.NormalizeProvider(v.GetString("default_provider")),
		InternalDefaultProvider:        domain.NormalizeProvider(firstNonEmpty(v.GetString("internal_default_provider"), v.GetString("default_provider"))),
		DatabaseURL:                    v.GetString("database_url"),
		RedisURL:                       v.GetString("redis_url"),
		VectorStoreBackend:             v.GetString("vector_store_backend"),
		ChatModel:                      v.GetString("chat_model"),
		SummaryModel:                   v.GetString("summary_model"),
		AnalysisModel:                  v.GetString("analysis_model"),
		EmbeddingModel:                 v.GetString("embedding_model"),
		ConversationTimeout:            time.Duration(v.GetInt("conversation_timeout_minutes")) * time.Minute,
		MaxContextTokens:               v.GetInt("max_context_tokens"),
		MaxInjectionTokens:             v.GetInt("max_injection_tokens"),
		MemoryRelationMaxHops:          v.GetInt("memory_relation_max_hops"),
		MemoryRelationHopDecayLambda:   v.GetFloat64("memory_relation_hop_decay_lambda"),
		MemoryRelationHopDecayExponent: v.GetFloat64("memory_relation_hop_decay_exponent"),
		MessageThreshold:               v.GetInt("message_threshold"),
		TokenThreshold:                 v.GetInt("token_threshold"),
		ShutdownTimeout:                time.Duration(v.GetInt("shutdown_timeout_seconds")) * time.Second,
		AdminToken:                     v.GetString("admin_token"),
		MasterKey:                      v.GetString("master_key"),
		DevAPIKey:                      v.GetString("dev_api_key"),
		OpenAIAPIKey:                   v.GetString("openai_api_key"),
		AnthropicAPIKey:                v.GetString("anthropic_api_key"),
		GeminiAPIKey:                   v.GetString("gemini_api_key"),
		OpenAIBaseURL:                  v.GetString("openai_base_url"),
		AnthropicBaseURL:               v.GetString("anthropic_base_url"),
		GeminiBaseURL:                  v.GetString("gemini_base_url"),
		EmbeddingAPIKey:                firstNonEmpty(v.GetString("embedding_api_key"), v.GetString("openai_api_key")),
		EmbeddingBaseURL:               firstNonEmpty(v.GetString("embedding_base_url"), v.GetString("openai_base_url")),
		ProviderTimeout:                time.Duration(v.GetInt("provider_timeout_seconds")) * time.Second,
		KeyMemoryWriteKeys:             csvList(v.GetString("key_memory_write_keys")),
		RateLimitRPS:                   v.GetInt("rate_limit_rps"),
		RateLimitBurst:                 v.GetInt("rate_limit_burst"),
		OTLPHTTPEndpoint:               v.GetString("otlp_http_endpoint"),
		LogLevel:                       v.GetString("log_level"),
		CORSAllowedOrigins:             csvList(v.GetString("cors_allowed_origins")),
		SessionCookieSecure:            v.GetBool("session_cookie_secure"),
		SessionCookieCrossSite:         v.GetBool("session_cookie_cross_site"),
		CacheTTL:                       time.Duration(v.GetInt("cache_ttl_seconds")) * time.Second,
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
	}, nil
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
