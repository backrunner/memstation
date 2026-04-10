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
	AppName                string
	Version                string
	GRPCPort               int
	Port                   int
	DefaultProvider        domain.Provider
	DatabaseURL            string
	RedisURL               string
	VectorStoreBackend     string
	ChatModel              string
	SummaryModel           string
	AnalysisModel          string
	EmbeddingModel         string
	ConversationTimeout    time.Duration
	MaxContextTokens       int
	MaxInjectionTokens     int
	MessageThreshold       int
	TokenThreshold         int
	ShutdownTimeout        time.Duration
	AdminToken             string
	MasterKey              string
	DevAPIKey              string
	OpenAIAPIKey           string
	AnthropicAPIKey        string
	GeminiAPIKey           string
	OpenAIBaseURL          string
	AnthropicBaseURL       string
	GeminiBaseURL          string
	ProviderTimeout        time.Duration
	KeyMemoryWriteKeys     []string
	RateLimitRPS           int
	RateLimitBurst         int
	OTLPHTTPEndpoint       string
	LogLevel               string
	CORSAllowedOrigins     []string
	SessionCookieSecure    bool
	SessionCookieCrossSite bool
	CacheTTL               time.Duration
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

	setDefault("app_name", "memstation")
	setDefault("version", "0.1.0")
	setDefault("port", 8080)
	setDefault("grpc_port", 9090)
	setDefault("default_provider", string(domain.ProviderOpenAI))
	setDefault("vector_store_backend", "pgvector")
	setDefault("conversation_timeout_minutes", 15)
	setDefault("max_context_tokens", 8192)
	setDefault("max_injection_tokens", 2048)
	setDefault("message_threshold", 32)
	setDefault("token_threshold", 64000)
	setDefault("shutdown_timeout_seconds", 20)
	setDefault("admin_token", DefaultDevAdminToken)
	setDefault("master_key", DefaultDevMasterKey)
	setDefault("dev_api_key", DefaultDevAPIKey)
	setDefault("openai_base_url", "https://api.openai.com/v1")
	setDefault("anthropic_base_url", "https://api.anthropic.com/v1")
	setDefault("gemini_base_url", "https://generativelanguage.googleapis.com/v1beta")
	setDefault("provider_timeout_seconds", 60)
	setDefault("key_memory_write_keys", "recent_conversation")
	setDefault("rate_limit_rps", 20)
	setDefault("rate_limit_burst", 40)
	setDefault("log_level", "info")
	setDefault("session_cookie_secure", false)
	setDefault("cache_ttl_seconds", 120)

	return Config{
		AppName:                v.GetString("app_name"),
		Version:                v.GetString("version"),
		GRPCPort:               v.GetInt("grpc_port"),
		Port:                   v.GetInt("port"),
		DefaultProvider:        domain.Provider(v.GetString("default_provider")),
		DatabaseURL:            v.GetString("database_url"),
		RedisURL:               v.GetString("redis_url"),
		VectorStoreBackend:     v.GetString("vector_store_backend"),
		ChatModel:              v.GetString("chat_model"),
		SummaryModel:           v.GetString("summary_model"),
		AnalysisModel:          v.GetString("analysis_model"),
		EmbeddingModel:         v.GetString("embedding_model"),
		ConversationTimeout:    time.Duration(v.GetInt("conversation_timeout_minutes")) * time.Minute,
		MaxContextTokens:       v.GetInt("max_context_tokens"),
		MaxInjectionTokens:     v.GetInt("max_injection_tokens"),
		MessageThreshold:       v.GetInt("message_threshold"),
		TokenThreshold:         v.GetInt("token_threshold"),
		ShutdownTimeout:        time.Duration(v.GetInt("shutdown_timeout_seconds")) * time.Second,
		AdminToken:             v.GetString("admin_token"),
		MasterKey:              v.GetString("master_key"),
		DevAPIKey:              v.GetString("dev_api_key"),
		OpenAIAPIKey:           v.GetString("openai_api_key"),
		AnthropicAPIKey:        v.GetString("anthropic_api_key"),
		GeminiAPIKey:           v.GetString("gemini_api_key"),
		OpenAIBaseURL:          v.GetString("openai_base_url"),
		AnthropicBaseURL:       v.GetString("anthropic_base_url"),
		GeminiBaseURL:          v.GetString("gemini_base_url"),
		ProviderTimeout:        time.Duration(v.GetInt("provider_timeout_seconds")) * time.Second,
		KeyMemoryWriteKeys:     csvList(v.GetString("key_memory_write_keys")),
		RateLimitRPS:           v.GetInt("rate_limit_rps"),
		RateLimitBurst:         v.GetInt("rate_limit_burst"),
		OTLPHTTPEndpoint:       v.GetString("otlp_http_endpoint"),
		LogLevel:               v.GetString("log_level"),
		CORSAllowedOrigins:     csvList(v.GetString("cors_allowed_origins")),
		SessionCookieSecure:    v.GetBool("session_cookie_secure"),
		SessionCookieCrossSite: v.GetBool("session_cookie_cross_site"),
		CacheTTL:               time.Duration(v.GetInt("cache_ttl_seconds")) * time.Second,
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
