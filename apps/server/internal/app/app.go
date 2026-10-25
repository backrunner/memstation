package app

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	memorycontrolservice "memstation/apps/server/internal/service/memorycontrol"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	graphresolver "memstation/apps/server/graph/resolver"
	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/cache"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/modelcap"
	"memstation/apps/server/internal/observability"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/provider"
	anthropicprovider "memstation/apps/server/internal/provider/anthropic"
	geminiprovider "memstation/apps/server/internal/provider/gemini"
	openaiprovider "memstation/apps/server/internal/provider/openai"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	adminservice "memstation/apps/server/internal/service/admin"
	"memstation/apps/server/internal/service/aibroker"
	analysisservice "memstation/apps/server/internal/service/analysis"
	apikeyservice "memstation/apps/server/internal/service/apikey"
	auditsvc "memstation/apps/server/internal/service/audit"
	connectionconfigservice "memstation/apps/server/internal/service/connectionconfig"
	conversationservice "memstation/apps/server/internal/service/conversation"
	embeddingservice "memstation/apps/server/internal/service/embedding"
	gatewayservice "memstation/apps/server/internal/service/gateway"
	ingestionservice "memstation/apps/server/internal/service/ingestion"
	lifecycleservice "memstation/apps/server/internal/service/lifecycle"
	memoryservice "memstation/apps/server/internal/service/memory"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	"memstation/apps/server/internal/service/memoryllm"
	multimodalservice "memstation/apps/server/internal/service/multimodal"
	providerconfigservice "memstation/apps/server/internal/service/providerconfig"
	rerankservice "memstation/apps/server/internal/service/rerank"
	settingsservice "memstation/apps/server/internal/service/settings"
	systemconfigservice "memstation/apps/server/internal/service/systemconfig"
	usageservice "memstation/apps/server/internal/service/usage"
	userservice "memstation/apps/server/internal/service/user"
	blobstore "memstation/apps/server/internal/store/blob"
	"memstation/apps/server/internal/store/deletionjournal"
	sqlstore "memstation/apps/server/internal/store/sql"
	"memstation/apps/server/internal/store/vector"
	grpctransport "memstation/apps/server/internal/transport/grpc"
	httptransport "memstation/apps/server/internal/transport/http"
	"memstation/apps/server/internal/worker"
)

type Container struct {
	Config        bootstrap.Config
	Logger        *slog.Logger
	DB            *sql.DB
	Redis         *redis.Client
	Health        *observability.Registry
	Auth          security.Authenticator
	RateLimiter   *security.RateLimiter
	Session       *security.AdminSessionManager
	Models        *modelcap.Registry
	Users         *userservice.Service
	APIKeys       *apikeyservice.Service
	Settings      *settingsservice.Service
	ConnectionCfg *connectionconfigservice.Service
	SystemConfig  *systemconfigservice.Service
	ProviderCfg   *providerconfigservice.Service
	Audit         *auditsvc.Service
	Conversations *conversationservice.Service
	Multimodal    *multimodalservice.Service
	Memory        *memoryservice.Service
	Lifecycle     *sqlstore.MemoryLifecycleRepository
	Generations   *memorygenservice.Service
	Admin         *adminservice.Service
	Usage         *usageservice.Service
	Gateway       *gatewayservice.Service
	GraphQL       *graphresolver.Resolver
	VectorStore   *vector.PGVectorStore
	Router        http.Handler
	GRPCServer    *grpc.Server
	Publisher     *worker.Publisher
	Inspector     *asynq.Inspector
	Relay         *worker.Relay
	Scheduler     *worker.Scheduler
	WorkerMux     *asynq.ServeMux
	AsynqOpt      *asynq.RedisClientOpt
	cleanup       []func(context.Context) error
}

func buildContainer(ctx context.Context) (*Container, error) {
	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		return nil, err
	}
	shutdownTelemetry, err := bootstrap.SetupTelemetry(ctx, cfg)
	if err != nil {
		return nil, err
	}
	logger := bootstrap.NewLogger(cfg)
	if cfg.AdminToken == bootstrap.DefaultDevAdminToken || cfg.MasterKey == bootstrap.DefaultDevMasterKey {
		logger.WarnContext(ctx, "running with local development secrets; rotate MEMSTATION_ADMIN_TOKEN and MEMSTATION_MASTER_KEY before sharing or deploying")
	}

	cleanup := []func(context.Context) error{shutdownTelemetry}
	health := observability.NewRegistry()

	encryptor, err := security.NewAESGCMEncryptor(cfg.MasterKey)
	if err != nil {
		return nil, err
	}

	runtimeStore := runtimeconfig.NewFileStore(cfg.RuntimeStatePath)
	dbHandle := runtimeconfig.NewSwitchableDB(logger, nil)
	cleanup = append(cleanup, func(context.Context) error { return dbHandle.Close() })

	connectionCfg := connectionconfigservice.NewService(runtimeStore, encryptor, dbHandle, cfg.DatabaseURL)
	connectionSnapshot, effectiveDatabaseURL, err := connectionCfg.Bootstrap(ctx)
	if err != nil {
		return nil, err
	}
	cfg.DatabaseURL = effectiveDatabaseURL
	db := dbHandle.Current()
	switch {
	case connectionSnapshot.Database.LastError != "":
		logger.WarnContext(ctx, "postgres unavailable; starting in degraded mode with in-memory stores", "error", connectionSnapshot.Database.LastError)
	case !connectionSnapshot.Database.Configured:
		logger.WarnContext(ctx, "postgres is not configured; starting in degraded mode with in-memory stores")
	}
	health.Add("database", observability.CheckFunc(func(ctx context.Context) error {
		if err := dbHandle.Health(ctx); err != nil {
			return err
		}
		if dbHandle.Current() == nil {
			return dbHandle.LastError()
		}
		return nil
	}))

	systemConfig := systemconfigservice.NewService(runtimeStore, systemconfigservice.DefaultsFromConfig(cfg))
	runtimeSnapshot, err := systemConfig.Bootstrap(ctx)
	if err != nil {
		return nil, err
	}
	cfg = runtimeSnapshot.ApplyConfig(cfg)
	if cfg.MemoryAIBackend != "direct" && cfg.MemoryAIBackend != "broker" {
		return nil, errors.New("memory AI backend must be direct or broker")
	}
	if cfg.MemoryAIBackend == "broker" {
		if cfg.MemoryBrokerCredential == cfg.AdminToken || cfg.MemoryBrokerCredential == cfg.DevAPIKey || cfg.MemoryBrokerCredential == cfg.MasterKey {
			return nil, errors.New("AI broker requires a separate service credential")
		}
		cfg.SummaryModel = "openai_compatible:" + cfg.MemoryBrokerSummaryAlias
		cfg.AnalysisModel = "openai_compatible:" + cfg.MemoryBrokerAnalysisAlias
		cfg.EmbeddingModel = "openai_compatible:" + cfg.MemoryBrokerEmbeddingAlias
		cfg.RerankModel = "rerank_compatible:" + cfg.MemoryBrokerRerankAlias
	}

	redisClient, err := bootstrap.OpenRedis(ctx, cfg)
	redisErr := err
	if err != nil {
		logger.WarnContext(ctx, "redis unavailable; starting in degraded mode without distributed cache or queue connectivity", "error", err)
	}
	if redisClient != nil {
		cleanup = append(cleanup, func(context.Context) error { return redisClient.Close() })
		health.Add("redis", observability.CheckFunc(func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }))
	} else if redisErr != nil {
		health.Add("redis", observability.CheckFunc(func(context.Context) error { return redisErr }))
	} else {
		health.Add("redis", observability.CheckFunc(func(context.Context) error { return nil }))
	}

	jsonCache, err := cache.NewJSONStore(ctx, redisClient, cfg.CacheTTL)
	if err != nil {
		return nil, err
	}
	cleanup = append(cleanup, jsonCache.Close)

	models := modelcap.NewRegistry(cfg.DefaultProvider, cfg.ChatModel, cfg.SummaryModel, cfg.AnalysisModel, cfg.EmbeddingModel)
	settings := settingsservice.NewService(dbHandle, encryptor, cfg.ChatModel, cfg.SummaryModel, cfg.AnalysisModel, cfg.EmbeddingModel, cfg.MaxInjectionTokens)
	settings.SetCache(jsonCache)
	providerCfg := providerconfigservice.NewService(runtimeStore, encryptor, providerconfigservice.Defaults{
		providerconfigservice.ScopeGateway: {
			domain.ProviderOpenAICompatible: {APIKey: cfg.OpenAIAPIKey, BaseURL: cfg.OpenAIBaseURL},
			domain.ProviderOpenAIResponses:  {APIKey: cfg.OpenAIAPIKey, BaseURL: cfg.OpenAIBaseURL},
			domain.ProviderAnthropic:        {APIKey: cfg.AnthropicAPIKey, BaseURL: cfg.AnthropicBaseURL},
			domain.ProviderGemini:           {APIKey: cfg.GeminiAPIKey, BaseURL: cfg.GeminiBaseURL},
		},
		providerconfigservice.ScopeInternal: {
			domain.ProviderOpenAICompatible: {APIKey: cfg.OpenAIAPIKey, BaseURL: cfg.OpenAIBaseURL},
			domain.ProviderOpenAIResponses:  {APIKey: cfg.OpenAIAPIKey, BaseURL: cfg.OpenAIBaseURL},
			domain.ProviderAnthropic:        {APIKey: cfg.AnthropicAPIKey, BaseURL: cfg.AnthropicBaseURL},
			domain.ProviderGemini:           {APIKey: cfg.GeminiAPIKey, BaseURL: cfg.GeminiBaseURL},
		},
		providerconfigservice.ScopeEmbedding: {
			domain.ProviderOpenAICompatible: {APIKey: cfg.EmbeddingAPIKey, BaseURL: cfg.EmbeddingBaseURL},
		},
		providerconfigservice.ScopeRerank: {
			domain.ProviderCohere: {APIKey: cfg.CohereAPIKey, BaseURL: cfg.CohereBaseURL},
			domain.ProviderJina:   {APIKey: cfg.JinaAPIKey, BaseURL: cfg.JinaBaseURL},
		},
	})
	if err := providerCfg.Bootstrap(ctx); err != nil {
		return nil, err
	}
	audit := auditsvc.NewService(dbHandle)
	users := userservice.NewService(dbHandle)
	apiKeys := apikeyservice.NewService(dbHandle)
	apiKeys.SetFallbackAudit(audit.Record)

	conversationRepo := sqlstore.NewHybridConversationRepository(dbHandle)
	var mediaRepo ports.MediaRepository = sqlstore.NewHybridMediaRepository(dbHandle)
	hybridMemory := sqlstore.NewHybridMemoryRepository(dbHandle)
	hybridMemory.SetFallbackAudit(audit.Record)
	var memoryRepo ports.MemoryRepository = hybridMemory
	var outboxRepo ports.OutboxRepository = sqlstore.NewHybridOutboxRepository(dbHandle)
	blobStore, err := blobstore.NewStore(cfg.Multimodal)
	if err != nil {
		return nil, err
	}
	vectorStore := vector.NewPGVectorStore(dbHandle)
	if err := vectorStore.SetBackend(cfg.VectorStoreBackend); err != nil {
		return nil, err
	}
	embeddingClient := embeddingservice.NewService(providerCfg.Resolver(providerconfigservice.ScopeEmbedding), settings, cfg.ProviderTimeout)
	vectorStore.SetEmbedder(embeddingClient)
	aiLedger := sqlstore.NewAICallRepository(dbHandle)
	var broker *aibroker.Client
	if cfg.MemoryAIBackend == "broker" {
		broker, err = aibroker.New(aibroker.Config{BaseURL: cfg.MemoryBrokerBaseURL, Credential: cfg.MemoryBrokerCredential, Timeout: cfg.ProviderTimeout, Roles: map[string]string{"summary": cfg.MemoryBrokerSummaryAlias, "analysis": cfg.MemoryBrokerAnalysisAlias, "embedding": cfg.MemoryBrokerEmbeddingAlias, "rerank": cfg.MemoryBrokerRerankAlias}, Embedding: domain.EmbeddingIdentity{ModelVersion: cfg.MemoryBrokerEmbeddingModel, Dimensions: cfg.MemoryBrokerEmbeddingDimensions, Fingerprint: cfg.MemoryBrokerEmbeddingFingerprint}}, aiLedger)
		if err != nil {
			return nil, err
		}
		vectorStore.SetEmbedder(broker)
	}

	generations := memorygenservice.NewService(dbHandle, cfg.EmbeddingModel)
	health.Add("vectorstore", observability.CheckFunc(vectorStore.Health))
	health.Add("outbox", observability.CheckFunc(func(ctx context.Context) error {
		_, err := outboxRepo.Pending(ctx, 1)
		return err
	}))

	memoryEngine := memoryservice.NewEngine(memoryRepo, vectorStore)
	memoryEngine.SetGenerationService(generations)
	memoryEngine.SetRelationRecallConfig(memoryservice.RelationRecallConfig{
		MaxEdgesPerNode: cfg.MemoryRelationMaxEdgesPerNode, MaxEdges: cfg.MemoryRelationMaxEdges, MaxNodes: cfg.MemoryRelationMaxNodes, Timeout: time.Duration(cfg.MemoryRelationTimeoutMS) * time.Millisecond,
		MaxHops:          cfg.MemoryRelationMaxHops,
		HopDecayLambda:   cfg.MemoryRelationHopDecayLambda,
		HopDecayExponent: cfg.MemoryRelationHopDecayExponent,
	})
	reranker := rerankservice.NewService(providerCfg.Resolver(providerconfigservice.ScopeRerank), cfg.RerankModel, cfg.ProviderTimeout)
	memoryEngine.SetReranker(reranker)
	if broker != nil {
		memoryEngine.SetReranker(broker)
	}
	memoryEngine.SetRerankConfig(memoryservice.RerankConfig{
		Model:               cfg.RerankModel,
		CandidateMultiplier: cfg.RerankCandidateMultiplier,
		MinCandidates:       cfg.RerankMinCandidates,
		MaxCandidates:       cfg.RerankMaxCandidates,
		BlendWeight:         cfg.RerankBlendWeight,
	})
	conversations := conversationservice.NewService(conversationRepo, outboxRepo, cfg.MessageThreshold, cfg.TokenThreshold)
	multimodal := multimodalservice.NewService(mediaRepo, blobStore, cfg.Multimodal)
	gatewayOpenAICompatible := openaiprovider.NewCompatibleAdapter(providerCfg.Resolver(providerconfigservice.ScopeGateway), cfg.ProviderTimeout)
	gatewayOpenAIResponses := openaiprovider.NewResponsesAdapter(providerCfg.Resolver(providerconfigservice.ScopeGateway), cfg.ProviderTimeout)
	gatewayAnthropic := anthropicprovider.NewAdapter(providerCfg.Resolver(providerconfigservice.ScopeGateway), cfg.ProviderTimeout)
	gatewayGemini := geminiprovider.NewAdapter(providerCfg.Resolver(providerconfigservice.ScopeGateway), cfg.ProviderTimeout)
	gatewayProviderRegistry := provider.NewRegistry(
		cfg.DefaultProvider,
		gatewayOpenAICompatible,
		gatewayOpenAIResponses,
		gatewayAnthropic,
		gatewayGemini,
	)
	internalOpenAICompatible := openaiprovider.NewCompatibleAdapter(providerCfg.Resolver(providerconfigservice.ScopeInternal), cfg.ProviderTimeout)
	internalOpenAIResponses := openaiprovider.NewResponsesAdapter(providerCfg.Resolver(providerconfigservice.ScopeInternal), cfg.ProviderTimeout)
	internalAnthropic := anthropicprovider.NewAdapter(providerCfg.Resolver(providerconfigservice.ScopeInternal), cfg.ProviderTimeout)
	internalGemini := geminiprovider.NewAdapter(providerCfg.Resolver(providerconfigservice.ScopeInternal), cfg.ProviderTimeout)
	internalProviderRegistry := provider.NewRegistry(
		cfg.InternalDefaultProvider,
		internalOpenAICompatible,
		internalOpenAIResponses,
		internalAnthropic,
		internalGemini,
	)
	gateway := gatewayservice.NewService(gatewayProviderRegistry, memoryEngine, conversations, settings, cfg.DefaultProvider)
	gateway.SetMaxContextTokens(cfg.MaxContextTokens)
	memories := memoryservice.NewService(memoryEngine, memoryRepo)
	memories.SetKeyMemoryCache(jsonCache)
	memories.SetVectorStore(vectorStore)
	memoryLLM := memoryllm.NewClient(internalProviderRegistry, settings, cfg.ProviderTimeout)
	if broker != nil {
		memoryLLM.SetRuntime(broker)
	}
	memoryScorer := memoryservice.NewScorerWithGenerator(memoryLLM)
	memories.SetScorer(memoryScorer)
	memories.SetGenerationService(generations)
	gateway.SetToolRuntime(gatewayservice.NewToolRuntime(memories, audit, keySet(cfg.KeyMemoryWriteKeys), 3))
	analysis := analysisservice.NewService(memoryLLM)
	admin := adminservice.NewService(conversationRepo, mediaRepo, memoryRepo, settings, audit, models)
	admin.SetGenerationService(generations)
	admin.SetMemoryProcessor(analysis, cfg.KeyMemoryAutoKeys)
	usage := usageservice.NewService(dbHandle, conversationRepo)
	admin.AttachUserRuntime(users, apiKeys, usage)
	sessionManager := security.NewAdminSessionManager(cfg.MasterKey, 8*time.Hour)
	auth := security.Authenticator{
		AdminToken:                 cfg.AdminToken,
		DevAPIKey:                  cfg.DevAPIKey,
		SessionManager:             sessionManager,
		ResolveAccess:              apiKeys.ResolveAccess,
		LookupPrincipal:            apiKeys.LookupPrincipalByHash,
		LookupDevelopmentPrincipal: apiKeys.DevelopmentPrincipal,
	}
	rateLimiter := security.NewRateLimiter(float64(cfg.RateLimitRPS), cfg.RateLimitBurst)

	var asynqOpt *asynq.RedisClientOpt
	var asynqClient *asynq.Client
	var inspector *asynq.Inspector
	if cfg.RedisURL != "" {
		opt, err := bootstrap.AsynqRedisOpt(cfg.RedisURL)
		if err != nil {
			return nil, err
		}
		asynqOpt = &opt
		asynqClient = asynq.NewClient(opt)
		inspector = asynq.NewInspector(opt)
		cleanup = append(cleanup, func(context.Context) error { return asynqClient.Close() })
		cleanup = append(cleanup, func(context.Context) error { return inspector.Close() })
		health.Add("queue", observability.CheckFunc(func(context.Context) error {
			_, err := inspector.Queues()
			return err
		}))
	}

	ingestionRepo := sqlstore.NewDurableIngestionRepository(dbHandle)
	memories.SetWatermarks(ingestionRepo)
	lifecycleRepo := sqlstore.NewMemoryLifecycleRepository(dbHandle)
	lifecycleRepo.SetWindowBudget(cfg.IngestionWindowBudget)
	journalKey := cfg.DeletionJournalKey
	if journalKey == "" {
		journalKey = cfg.MasterKey
	}
	deletionJournal, err := deletionjournal.New(cfg.DeletionJournalPath, journalKey)
	if err != nil {
		return nil, err
	}
	lifecycleRepo.SetDeletionJournal(deletionJournal)
	ingestionRepo.SetDeletionJournal(deletionJournal)
	lifecycleRepo.SetCleanup(blobStore, func(ctx context.Context) error { return jsonCache.DeletePrefix(ctx, "memory:") })
	memoryGate := sqlstore.NewMemoryGate(lifecycleRepo)
	health.Add("deletion_history", observability.CheckFunc(memoryGate.Verify))
	lifecycle := lifecycleservice.NewService(lifecycleRepo)
	lifecycle.SetReady(memoryGate.Check)
	memoryRuntime := domain.MemoryRuntime{AIBackend: cfg.MemoryAIBackend, RoleAliases: map[string]string{}, UsageAuthority: "memstation"}
	if broker != nil {
		memoryRuntime.RoleAliases = map[string]string{"summary": cfg.MemoryBrokerSummaryAlias, "analysis": cfg.MemoryBrokerAnalysisAlias, "embedding": cfg.MemoryBrokerEmbeddingAlias, "rerank": cfg.MemoryBrokerRerankAlias}
		memoryRuntime.Embedding = domain.EmbeddingIdentity{ModelVersion: cfg.MemoryBrokerEmbeddingModel, Dimensions: cfg.MemoryBrokerEmbeddingDimensions, Fingerprint: cfg.MemoryBrokerEmbeddingFingerprint}
		memoryRuntime.UsageAuthority = "broker"
	}
	lifecycle.SetRuntime(memoryRuntime)
	memoryControl := memorycontrolservice.NewService(lifecycleRepo, func(queue, id string) error {
		if inspector == nil {
			return errors.New("queue unavailable")
		}
		return inspector.RunTask(queue, id)
	})

	publisher := worker.NewPublisher(logger, asynqClient)
	relay := worker.NewRelay(logger, outboxRepo, publisher)
	scheduler := worker.NewScheduler(logger, relay, conversations, cfg.ConversationTimeout)
	relay.SetTaskAuthority(ingestionRepo)
	scheduler.SetIngestion(ingestionRepo)
	scheduler.SetRetention(lifecycleRepo.ExpireExports)
	systemConfig.SetLiveApplier(systemconfigservice.LiveApplier{
		Validate: func(snapshot systemconfigservice.Snapshot) error {
			if _, err := blobstore.NewStore(snapshot.MultimodalConfig()); err != nil {
				return err
			}
			return nil
		},
		SetDefaultProvider: func(provider domain.Provider) {
			gateway.SetDefaultProvider(provider)
		},
		SetInternalDefaultProvider: func(provider domain.Provider) {
			internalProviderRegistry.SetDefaultProvider(provider)
		},
		SetVectorStoreBackend: func(backend string) {
			if err := vectorStore.SetBackend(backend); err != nil {
				logger.WarnContext(ctx, "ignored unsupported live vector store backend update", "backend", backend, "error", err)
			}
		},
		SetThresholds: func(messageThreshold, tokenThreshold int) {
			conversations.UpdateThresholds(messageThreshold, tokenThreshold)
		},
		SetConversationTimeout: func(timeout time.Duration) {
			scheduler.UpdateTimeoutWindow(timeout)
		},
		SetProviderTimeout: func(timeout time.Duration) {
			gatewayOpenAICompatible.SetTimeout(timeout)
			gatewayOpenAIResponses.SetTimeout(timeout)
			gatewayAnthropic.SetTimeout(timeout)
			gatewayGemini.SetTimeout(timeout)
			internalOpenAICompatible.SetTimeout(timeout)
			internalOpenAIResponses.SetTimeout(timeout)
			internalAnthropic.SetTimeout(timeout)
			internalGemini.SetTimeout(timeout)
			embeddingClient.SetTimeout(timeout)
			reranker.SetTimeout(timeout)
		},
		SetMaxContextTokens: func(tokens int) {
			gateway.SetMaxContextTokens(tokens)
		},
		SetMaxInjectionTokens: func(tokens int) {
			settings.UpdateDefaultMaxInjectionTokens(tokens)
		},
		SetRerankConfig: func(model string, candidateMultiplier, minCandidates, maxCandidates int, blendWeight float64) {
			memoryEngine.SetRerankConfig(memoryservice.RerankConfig{
				Model:               model,
				CandidateMultiplier: candidateMultiplier,
				MinCandidates:       minCandidates,
				MaxCandidates:       maxCandidates,
				BlendWeight:         blendWeight,
			})
		},
		SetMemoryRelationRecall: func(maxHops int, decayLambda, decayExponent float64) {
			memoryEngine.SetRelationRecallConfig(memoryservice.RelationRecallConfig{
				MaxEdgesPerNode: cfg.MemoryRelationMaxEdgesPerNode, MaxEdges: cfg.MemoryRelationMaxEdges, MaxNodes: cfg.MemoryRelationMaxNodes, Timeout: time.Duration(cfg.MemoryRelationTimeoutMS) * time.Millisecond,
				MaxHops:          maxHops,
				HopDecayLambda:   decayLambda,
				HopDecayExponent: decayExponent,
			})
		},
		SetRateLimit: func(rps, burst int) {
			if rateLimiter != nil {
				rateLimiter.Update(float64(rps), burst)
			}
		},
		SetCacheTTL: func(ttl time.Duration) {
			jsonCache.UpdateTTL(ttl)
		},
		SetMultimodalConfig: func(nextCfg bootstrap.MultimodalConfig) {
			nextBlobStore, err := blobstore.NewStore(nextCfg)
			if err != nil {
				logger.WarnContext(ctx, "ignored invalid live multimodal config update", "error", err)
				return
			}
			multimodal.UpdateRuntime(nextCfg, nextBlobStore)
		},
	})
	workerMux := worker.NewServeMux(logger, worker.Services{
		AcknowledgeReceipt:   ingestionRepo.AcknowledgeReceipt,
		DataReady:            memoryGate.Check,
		ProcessOperation:     lifecycleRepo.ProcessOperation,
		TaskAuthority:        ingestionRepo,
		Jobs:                 ingestionRepo,
		Conversations:        conversationRepo,
		MediaStore:           mediaRepo,
		Memories:             memoryRepo,
		MemoryService:        memories,
		Generations:          generations,
		Vectors:              vectorStore,
		Publisher:            publisher,
		Analysis:             analysis,
		Multimodal:           multimodal,
		Scorer:               memoryScorer,
		EmbeddingModel:       cfg.EmbeddingModel,
		AllowedKeyMemoryKeys: keySet(cfg.KeyMemoryAutoKeys),
		RecordAudit:          audit.Record,
		InvalidateKeyMemory:  memories.InvalidateKeyMemoryCache,
	})
	graphQLResolver := &graphresolver.Resolver{
		Generations: generations,
		Identity:    apiKeys.Identities(), Ingestion: ingestionservice.NewService(ingestionRepo, cfg.IngestionWindowBudget), Lifecycle: lifecycle, MemoryControlService: memoryControl, AIUsage: aibroker.NewUsageService(aiLedger, broker),
		UserService:         users,
		APIKeyService:       apiKeys,
		SettingsService:     settings,
		ConversationService: conversations,
		MemoryService:       memories,
		AdminService:        admin,
		UsageService:        usage,
		AuditService:        audit,
		Publisher:           publisher,
	}

	router := httptransport.NewRouter(httptransport.Dependencies{
		AIUsage:   aibroker.NewUsageService(aiLedger, broker),
		Lifecycle: lifecycle, MemoryControl: memoryControl, DataReady: memoryGate.Check,
		Ingestion:        ingestionservice.NewService(ingestionRepo, cfg.IngestionWindowBudget),
		Config:           cfg,
		Logger:           logger,
		Health:           health,
		Auth:             auth,
		RateLimiter:      rateLimiter,
		Session:          sessionManager,
		Models:           models,
		GraphQL:          graphQLResolver,
		Gateway:          gateway,
		Multimodal:       multimodal,
		Memory:           memories,
		Generations:      generations,
		Admin:            admin,
		Audit:            audit,
		Usage:            usage,
		Users:            users,
		APIKeys:          apiKeys,
		Settings:         settings,
		ConnectionConfig: connectionCfg,
		SystemConfig:     systemConfig,
		ProviderConfig:   providerCfg,
		Publisher:        publisher,
		Inspector:        inspector,
	})
	grpcServer := grpctransport.NewServer(grpctransport.Dependencies{
		AIUsage:   aibroker.NewUsageService(aiLedger, broker),
		DataReady: memoryGate.Check, Ingestion: ingestionservice.NewService(ingestionRepo, cfg.IngestionWindowBudget), Lifecycle: lifecycle, MemoryControl: memoryControl, Identity: apiKeys.Identities(),
		Config:      cfg,
		Logger:      logger,
		Health:      health,
		Auth:        auth,
		RateLimiter: rateLimiter,
		Models:      models,
		Gateway:     gateway,
		Memory:      memories,
		Generations: generations,
		Admin:       admin,
		Audit:       audit,
	})

	return &Container{
		Config:        cfg,
		Logger:        logger,
		DB:            db,
		Redis:         redisClient,
		Health:        health,
		Auth:          auth,
		RateLimiter:   rateLimiter,
		Session:       sessionManager,
		Models:        models,
		Users:         users,
		APIKeys:       apiKeys,
		Settings:      settings,
		ConnectionCfg: connectionCfg,
		SystemConfig:  systemConfig,
		ProviderCfg:   providerCfg,
		Audit:         audit,
		Conversations: conversations,
		Multimodal:    multimodal,
		Memory:        memories,
		Lifecycle:     lifecycleRepo,
		Generations:   generations,
		Admin:         admin,
		Usage:         usage,
		Gateway:       gateway,
		GraphQL:       graphQLResolver,
		VectorStore:   vectorStore,
		Router:        router,
		GRPCServer:    grpcServer,
		Publisher:     publisher,
		Inspector:     inspector,
		Relay:         relay,
		Scheduler:     scheduler,
		WorkerMux:     workerMux,
		AsynqOpt:      asynqOpt,
		cleanup:       cleanup,
	}, nil
}

func RunServe(ctx context.Context) error {
	container, err := buildContainer(ctx)
	if err != nil {
		return err
	}
	defer shutdownAll(context.Background(), container.cleanup)

	server := &http.Server{
		Addr:              ":" + itoa(container.Config.Port),
		Handler:           container.Router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		container.Logger.InfoContext(ctx, "starting HTTP server", "addr", server.Addr)
		if uri := managerConnectionURI(container.Config); uri != "" {
			container.Logger.InfoContext(ctx, "manager target uri ready", "uri", uri)
		}
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	var grpcListener net.Listener
	if container.Config.GRPCPort > 0 && container.GRPCServer != nil {
		grpcListener, err = net.Listen("tcp", ":"+itoa(container.Config.GRPCPort))
		if err != nil {
			return err
		}
		go func() {
			container.Logger.InfoContext(ctx, "starting gRPC server", "addr", grpcListener.Addr().String())
			if err := container.GRPCServer.Serve(grpcListener); err != nil {
				errCh <- err
			}
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), container.Config.ShutdownTimeout)
		defer cancel()
		var shutdownErrs []error
		if err := server.Shutdown(shutdownCtx); err != nil {
			shutdownErrs = append(shutdownErrs, err)
		}
		if container.GRPCServer != nil {
			done := make(chan struct{})
			go func() {
				container.GRPCServer.GracefulStop()
				close(done)
			}()
			select {
			case <-done:
			case <-shutdownCtx.Done():
				container.GRPCServer.Stop()
			}
		}
		return errors.Join(shutdownErrs...)
	case err := <-errCh:
		return err
	}
}

func RunWorker(ctx context.Context) error {
	container, err := buildContainer(ctx)
	if err != nil {
		return err
	}
	defer shutdownAll(context.Background(), container.cleanup)
	if container.AsynqOpt == nil {
		container.Logger.InfoContext(ctx, "worker idle; redis is not configured")
		<-ctx.Done()
		return nil
	}
	server := asynq.NewServer(*container.AsynqOpt, asynq.Config{
		RetryDelayFunc: worker.MemoryRetryDelay,
		Concurrency:    10,
		Queues: map[string]int{
			worker.QueueCritical: 4,
			worker.QueueDefault:  6,
			worker.QueueLow:      2,
			worker.QueueBulk:     1,
		},
	})
	go func() {
		<-ctx.Done()
		server.Shutdown()
	}()
	return server.Run(container.WorkerMux)
}

func RunScheduler(ctx context.Context) error {
	container, err := buildContainer(ctx)
	if err != nil {
		return err
	}
	defer shutdownAll(context.Background(), container.cleanup)
	return container.Scheduler.Run(ctx)
}

func RunMigrate(ctx context.Context) error {
	container, err := buildContainer(ctx)
	if err != nil {
		return err
	}
	defer shutdownAll(context.Background(), container.cleanup)
	migrationsDir := filepath.Join("ent", "migrate", "migrations")
	if _, err := exec.LookPath("atlas"); err == nil {
		cmd := exec.CommandContext(ctx, "atlas", "migrate", "apply", "--dir", "file://"+migrationsDir, "--url", container.Config.DatabaseURL)
		cmd.Stdout = logWriter{logger: container.Logger}
		cmd.Stderr = logWriter{logger: container.Logger}
		return cmd.Run()
	}
	container.Logger.WarnContext(ctx, "atlas not found; applying SQL migrations with built-in runner", "dir", migrationsDir)
	return applySQLMigrations(ctx, container.DB, container.Logger, migrationsDir)
}

func RunSeed(ctx context.Context, initializeDeletionJournal ...bool) error {
	container, err := buildContainer(ctx)
	if err != nil {
		return err
	}
	defer shutdownAll(context.Background(), container.cleanup)
	if len(initializeDeletionJournal) > 0 && initializeDeletionJournal[0] {
		if err = container.Lifecycle.InitializeLegacyDeletionJournal(ctx); err != nil {
			return err
		}
	}
	adminUser, err := container.Users.EnsureDefaultAdmin(ctx, "Admin")
	if err != nil {
		return err
	}
	settingsSnapshot, err := container.Settings.UpdateForUser(ctx, adminUser.ID, map[string]any{})
	if err != nil {
		return err
	}

	var seededKey apikeyservice.Record
	var plaintext string
	keys, err := container.APIKeys.List(ctx)
	if err != nil {
		return err
	}
	for _, item := range keys {
		if item.UserID == adminUser.ID && item.Name == "bootstrap" && item.RevokedAt == nil {
			seededKey = item
			break
		}
	}
	if seededKey.ID == "" {
		seededKey, plaintext, err = container.APIKeys.Create(ctx, adminUser.ID, "bootstrap")
		if err != nil {
			return err
		}
	}

	container.Logger.InfoContext(ctx, "seeded model registry", "models", container.Models.List())
	encryptor, err := security.NewAESGCMEncryptor(container.Config.MasterKey)
	if err != nil {
		return err
	}
	encrypted, err := encryptor.Encrypt(ctx, []byte(container.Config.AdminToken))
	if err != nil {
		return err
	}
	container.Logger.InfoContext(ctx, "admin bootstrap prepared",
		"admin_user_id", adminUser.ID,
		"bootstrap_api_key_prefix", seededKey.Prefix,
		"bootstrap_api_key_issued", plaintext != "",
		"default_settings", settingsSnapshot,
		"admin_token_ciphertext", encrypted,
	)
	if plaintext != "" {
		container.Logger.InfoContext(ctx, "bootstrap api key issued", "bootstrap_api_key", plaintext)
	}
	return nil
}

func shutdownAll(ctx context.Context, cleanup []func(context.Context) error) {
	for i := len(cleanup) - 1; i >= 0; i-- {
		_ = cleanup[i](ctx)
	}
}

func keySet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		out[value] = struct{}{}
	}
	return out
}

func itoa(value int) string { return strconv.Itoa(value) }

func managerConnectionURI(cfg bootstrap.Config) string {
	baseURL := strings.TrimSpace(cfg.PublicBaseURL)
	if baseURL == "" {
		baseURL = "http://127.0.0.1:" + itoa(cfg.Port)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	scheme := "memstation"
	if strings.EqualFold(parsed.Scheme, "https") {
		scheme = "memstations"
	}
	target := &url.URL{
		Scheme: scheme,
		Host:   parsed.Host,
		Path:   parsed.EscapedPath(),
	}
	if tokenHash, err := security.HashAdminToken(cfg.AdminToken); err == nil {
		values := target.Query()
		values.Set("key", tokenHash)
		target.RawQuery = values.Encode()
	}
	return target.String()
}

type logWriter struct{ logger *slog.Logger }

func (w logWriter) Write(p []byte) (int, error) {
	w.logger.Info(string(p))
	return len(p), nil
}

// RunRecoveryCheck applies the latest independently persisted deletion history
// without seeding users, credentials or configuration.
func RunRecoveryCheck(ctx context.Context) error {
	container, err := buildContainer(ctx)
	if err != nil {
		return err
	}
	defer shutdownAll(context.Background(), container.cleanup)
	if err = container.Lifecycle.EnsureDeletionJournal(ctx); err != nil {
		return err
	}
	container.Logger.InfoContext(ctx, "memory recovery verified")
	return nil
}
