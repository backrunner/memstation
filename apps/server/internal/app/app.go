package app

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

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
	"memstation/apps/server/internal/security"
	adminservice "memstation/apps/server/internal/service/admin"
	analysisservice "memstation/apps/server/internal/service/analysis"
	apikeyservice "memstation/apps/server/internal/service/apikey"
	auditsvc "memstation/apps/server/internal/service/audit"
	conversationservice "memstation/apps/server/internal/service/conversation"
	embeddingservice "memstation/apps/server/internal/service/embedding"
	gatewayservice "memstation/apps/server/internal/service/gateway"
	memoryservice "memstation/apps/server/internal/service/memory"
	providerconfigservice "memstation/apps/server/internal/service/providerconfig"
	settingsservice "memstation/apps/server/internal/service/settings"
	systemconfigservice "memstation/apps/server/internal/service/systemconfig"
	usageservice "memstation/apps/server/internal/service/usage"
	userservice "memstation/apps/server/internal/service/user"
	sqlstore "memstation/apps/server/internal/store/sql"
	"memstation/apps/server/internal/store/vector"
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
	SystemConfig  *systemconfigservice.Service
	ProviderCfg   *providerconfigservice.Service
	Audit         *auditsvc.Service
	Conversations *conversationservice.Service
	Memory        *memoryservice.Service
	Admin         *adminservice.Service
	Usage         *usageservice.Service
	Gateway       *gatewayservice.Service
	GraphQL       *graphresolver.Resolver
	VectorStore   *vector.PGVectorStore
	Router        http.Handler
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

	db, err := bootstrap.OpenPostgres(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if db != nil {
		cleanup = append(cleanup, func(context.Context) error { return db.Close() })
		health.Add("database", observability.CheckFunc(func(ctx context.Context) error { return db.PingContext(ctx) }))
	} else {
		health.Add("database", observability.CheckFunc(func(context.Context) error { return nil }))
	}

	systemConfig := systemconfigservice.NewService(db, systemconfigservice.DefaultsFromConfig(cfg))
	runtimeSnapshot, err := systemConfig.Bootstrap(ctx)
	if err != nil {
		return nil, err
	}
	cfg = runtimeSnapshot.ApplyConfig(cfg)

	redisClient, err := bootstrap.OpenRedis(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if redisClient != nil {
		cleanup = append(cleanup, func(context.Context) error { return redisClient.Close() })
		health.Add("redis", observability.CheckFunc(func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }))
	} else {
		health.Add("redis", observability.CheckFunc(func(context.Context) error { return nil }))
	}

	jsonCache, err := cache.NewJSONStore(ctx, redisClient, cfg.CacheTTL)
	if err != nil {
		return nil, err
	}
	cleanup = append(cleanup, jsonCache.Close)

	encryptor, err := security.NewAESGCMEncryptor(cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	_ = encryptor

	models := modelcap.NewRegistry(cfg.DefaultProvider, cfg.ChatModel, cfg.SummaryModel, cfg.AnalysisModel, cfg.EmbeddingModel)
	settings := settingsservice.NewService(db, encryptor, cfg.ChatModel, cfg.SummaryModel, cfg.AnalysisModel, cfg.EmbeddingModel, cfg.MaxInjectionTokens)
	settings.SetCache(jsonCache)
	providerCfg := providerconfigservice.NewService(db, encryptor, map[domain.Provider]provider.RuntimeConfig{
		domain.ProviderOpenAI: {
			APIKey:  cfg.OpenAIAPIKey,
			BaseURL: cfg.OpenAIBaseURL,
		},
		domain.ProviderAnthropic: {
			APIKey:  cfg.AnthropicAPIKey,
			BaseURL: cfg.AnthropicBaseURL,
		},
		domain.ProviderGemini: {
			APIKey:  cfg.GeminiAPIKey,
			BaseURL: cfg.GeminiBaseURL,
		},
	})
	audit := auditsvc.NewService(db)
	users := userservice.NewService(db)
	apiKeys := apikeyservice.NewService(db)

	var conversationRepo ports.ConversationRepository
	var memoryRepo ports.MemoryRepository
	var outboxRepo ports.OutboxRepository
	if db != nil {
		conversationRepo = sqlstore.NewPostgresConversationRepository(db)
		memoryRepo = sqlstore.NewPostgresMemoryRepository(db)
		outboxRepo = sqlstore.NewPostgresOutboxRepository(db)
	} else {
		conversationRepo = sqlstore.NewInMemoryConversationRepository()
		memoryRepo = sqlstore.NewInMemoryMemoryRepository()
		outboxRepo = sqlstore.NewInMemoryOutboxRepository()
	}
	vectorStore := vector.NewPGVectorStore(db)
	vectorStore.SetEmbedder(embeddingservice.NewService(providerCfg, settings, cfg.ProviderTimeout))
	health.Add("vectorstore", observability.CheckFunc(vectorStore.Health))
	health.Add("outbox", observability.CheckFunc(func(ctx context.Context) error {
		_, err := outboxRepo.Pending(ctx, 1)
		return err
	}))

	memoryEngine := memoryservice.NewEngine(memoryRepo, vectorStore)
	conversations := conversationservice.NewService(conversationRepo, outboxRepo, cfg.MessageThreshold, cfg.TokenThreshold)
	providerRegistry := provider.NewRegistry(
		cfg.DefaultProvider,
		openaiprovider.NewAdapter(providerCfg, cfg.ProviderTimeout),
		anthropicprovider.NewAdapter(providerCfg, cfg.ProviderTimeout),
		geminiprovider.NewAdapter(providerCfg, cfg.ProviderTimeout),
	)
	gateway := gatewayservice.NewService(providerRegistry, memoryEngine, conversations, settings, cfg.DefaultProvider)
	memories := memoryservice.NewService(memoryEngine, memoryRepo)
	memories.SetKeyMemoryCache(jsonCache)
	memories.SetVectorStore(vectorStore)
	memories.SetScorer(memoryservice.NewScorer(providerRegistry, settings))
	gateway.SetToolRuntime(gatewayservice.NewToolRuntime(memories, audit, keySet(cfg.KeyMemoryWriteKeys), 3))
	admin := adminservice.NewService(conversationRepo, memoryRepo, settings, audit, models)
	usage := usageservice.NewService(db, conversationRepo)
	admin.AttachUserRuntime(users, apiKeys, usage)
	analysis := analysisservice.NewService()
	sessionManager := security.NewAdminSessionManager(cfg.MasterKey, 8*time.Hour)
	auth := security.Authenticator{
		AdminToken:     cfg.AdminToken,
		DevAPIKey:      cfg.DevAPIKey,
		SessionManager: sessionManager,
		LookupAPIKey: func(ctx context.Context, prefix, hash string) (string, error) {
			return apiKeys.LookupUserByHash(ctx, prefix, hash)
		},
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

	publisher := worker.NewPublisher(logger, asynqClient)
	relay := worker.NewRelay(logger, outboxRepo, publisher)
	scheduler := worker.NewScheduler(logger, relay, conversations, cfg.ConversationTimeout)
	systemConfig.SetLiveApplier(systemconfigservice.LiveApplier{
		SetDefaultProvider: func(provider domain.Provider) {
			gateway.SetDefaultProvider(provider)
		},
		SetThresholds: func(messageThreshold, tokenThreshold int) {
			conversations.UpdateThresholds(messageThreshold, tokenThreshold)
		},
		SetConversationTimeout: func(timeout time.Duration) {
			scheduler.UpdateTimeoutWindow(timeout)
		},
		SetRateLimit: func(rps, burst int) {
			if rateLimiter != nil {
				rateLimiter.Update(float64(rps), burst)
			}
		},
	})
	workerMux := worker.NewServeMux(logger, worker.Services{
		Conversations:        conversationRepo,
		Memories:             memoryRepo,
		Vectors:              vectorStore,
		Publisher:            publisher,
		Analysis:             analysis,
		Scorer:               memoryservice.NewScorer(providerRegistry, settings),
		EmbeddingModel:       cfg.EmbeddingModel,
		AllowedKeyMemoryKeys: keySet(cfg.KeyMemoryWriteKeys),
		RecordAudit:          audit.Record,
		InvalidateKeyMemory:  memories.InvalidateKeyMemoryCache,
	})
	graphQLResolver := &graphresolver.Resolver{
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
		Config:         cfg,
		Logger:         logger,
		Health:         health,
		Auth:           auth,
		RateLimiter:    rateLimiter,
		Session:        sessionManager,
		Models:         models,
		GraphQL:        graphQLResolver,
		Gateway:        gateway,
		Memory:         memories,
		Admin:          admin,
		Audit:          audit,
		Usage:          usage,
		Users:          users,
		APIKeys:        apiKeys,
		Settings:       settings,
		SystemConfig:   systemConfig,
		ProviderConfig: providerCfg,
		Publisher:      publisher,
		Inspector:      inspector,
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
		SystemConfig:  systemConfig,
		ProviderCfg:   providerCfg,
		Audit:         audit,
		Conversations: conversations,
		Memory:        memories,
		Admin:         admin,
		Usage:         usage,
		Gateway:       gateway,
		GraphQL:       graphQLResolver,
		VectorStore:   vectorStore,
		Router:        router,
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

	errCh := make(chan error, 1)
	go func() {
		container.Logger.InfoContext(ctx, "starting HTTP server", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), container.Config.ShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
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
		Concurrency: 10,
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

func RunSeed(ctx context.Context) error {
	container, err := buildContainer(ctx)
	if err != nil {
		return err
	}
	defer shutdownAll(context.Background(), container.cleanup)
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

type logWriter struct{ logger *slog.Logger }

func (w logWriter) Write(p []byte) (int, error) {
	w.logger.Info(string(p))
	return len(p), nil
}
