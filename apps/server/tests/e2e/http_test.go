package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/modelcap"
	"memstation/apps/server/internal/observability"
	"memstation/apps/server/internal/provider"
	"memstation/apps/server/internal/security"
	adminservice "memstation/apps/server/internal/service/admin"
	apikeyservice "memstation/apps/server/internal/service/apikey"
	auditsvc "memstation/apps/server/internal/service/audit"
	conversationservice "memstation/apps/server/internal/service/conversation"
	gatewayservice "memstation/apps/server/internal/service/gateway"
	memoryservice "memstation/apps/server/internal/service/memory"
	providerconfigservice "memstation/apps/server/internal/service/providerconfig"
	settingsservice "memstation/apps/server/internal/service/settings"
	systemconfigservice "memstation/apps/server/internal/service/systemconfig"
	usageservice "memstation/apps/server/internal/service/usage"
	userservice "memstation/apps/server/internal/service/user"
	"memstation/apps/server/internal/store/sql"
	"memstation/apps/server/internal/store/vector"
	httptransport "memstation/apps/server/internal/transport/http"
	"memstation/apps/server/internal/worker"
	"memstation/apps/server/tests/testutil/mockprovider"
)

const e2eTestAdminToken = "e2e-test-admin-token"

func TestLiveEndpoint(t *testing.T) {
	t.Parallel()
	router := newTestRouter()

	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["status"] != "live" {
		t.Fatalf("unexpected status payload: %v", payload)
	}
}

func TestAdminLoginCreatesSession(t *testing.T) {
	t.Parallel()
	router := newTestRouter()

	request := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewBufferString(`{"token":"`+e2eTestAdminToken+`"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie to be set")
	}
	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	sessionRequest.AddCookie(cookies[0])
	sessionRecorder := httptest.NewRecorder()
	router.ServeHTTP(sessionRecorder, sessionRequest)
	if sessionRecorder.Code != http.StatusOK {
		t.Fatalf("expected session check 200, got %d", sessionRecorder.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(sessionRecorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal session response: %v", err)
	}
	if payload["authenticated"] != true {
		t.Fatalf("expected authenticated session payload, got %v", payload)
	}
}

func TestChatCompletionStreamingEndpoint(t *testing.T) {
	t.Parallel()
	router := newProxyRouter()

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4.1-mini","stream":true,"messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer ms-local-dev")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "chat.completion.chunk") {
		t.Fatalf("expected chat stream chunk, got %s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("expected done marker, got %s", body)
	}
}

func TestGatewayReturnsConversationHeadersAndSupportsMemoryPreview(t *testing.T) {
	t.Parallel()
	router := newProxyRouter()

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"remember that i am validating the preview panel"}]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer ms-local-dev")
	request.Header.Set("X-MemStation-Memory-Persist", "false")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	conversationID := recorder.Header().Get("X-MemStation-Conversation-ID")
	if conversationID == "" {
		t.Fatal("expected conversation header")
	}
	if got := recorder.Header().Get("X-MemStation-Memory-Persist"); got != "false" {
		t.Fatalf("expected memory persist response header false, got %q", got)
	}

	loginRequest := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewBufferString(`{"token":"`+e2eTestAdminToken+`"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRecorder := httptest.NewRecorder()
	router.ServeHTTP(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("expected login 200, got %d", loginRecorder.Code)
	}
	cookies := loginRecorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected admin session cookie")
	}

	previewBody := `{"conversation_id":"` + conversationID + `","messages":[{"role":"user","text":"remember that i am validating the preview panel"},{"role":"assistant","text":"Preview panel validation acknowledged."}]}`
	previewRequest := httptest.NewRequest(http.MethodPost, "/api/admin/playground/memory-preview", bytes.NewBufferString(previewBody))
	previewRequest.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		previewRequest.AddCookie(cookie)
	}
	previewRecorder := httptest.NewRecorder()
	router.ServeHTTP(previewRecorder, previewRequest)

	if previewRecorder.Code != http.StatusOK {
		t.Fatalf("expected preview 200, got %d body=%s", previewRecorder.Code, previewRecorder.Body.String())
	}
	var preview map[string]any
	if err := json.Unmarshal(previewRecorder.Body.Bytes(), &preview); err != nil {
		t.Fatalf("unmarshal preview: %v", err)
	}
	if preview["summary_status"] != "preview" {
		t.Fatalf("expected preview summary status, got %+v", preview)
	}
	if _, ok := preview["fact"]; !ok {
		t.Fatalf("expected fact preview, got %+v", preview)
	}
}

func TestCORSPreflightAllowsConfiguredManagerOrigin(t *testing.T) {
	t.Parallel()

	memoryRepo := sql.NewInMemoryMemoryRepository()
	conversations := sql.NewInMemoryConversationRepository()
	settings := settingsservice.NewService(nil, nil, "", "", "", "", 2048)
	audit := auditsvc.NewService(nil)
	admin := adminservice.NewService(conversations, memoryRepo, settings, audit, modelcap.NewRegistry(domain.ProviderOpenAI, "", "", "", ""))
	engine := memoryservice.NewEngine(memoryRepo, vector.NewPGVectorStore(nil))
	memorySvc := memoryservice.NewService(engine, memoryRepo)
	session := security.NewAdminSessionManager(e2eTestAdminToken, time.Hour)

	router := httptransport.NewRouter(httptransport.Dependencies{
		Config: bootstrap.Config{
			Version:             "0.1.0",
			SessionCookieSecure: false,
			CORSAllowedOrigins:  []string{"http://localhost:3000"},
		},
		Health:   observability.NewRegistry(),
		Auth:     security.Authenticator{AdminToken: e2eTestAdminToken, DevAPIKey: "ms-local-dev", SessionManager: session},
		Session:  session,
		Models:   modelcap.NewRegistry(domain.ProviderOpenAI, "", "", "", ""),
		Memory:   memorySvc,
		Admin:    admin,
		Audit:    audit,
		Settings: settings,
	})

	request := httptest.NewRequest(http.MethodOptions, "/api/admin/session", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	request.Header.Set("Access-Control-Request-Method", "GET")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("expected allowed origin header, got %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("expected credentials header, got %q", got)
	}
}

func TestAdminUserAndCacheEndpoints(t *testing.T) {
	t.Parallel()

	router := newTestRouter()
	cookies := loginCookies(t, router)

	usersRequest := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	for _, cookie := range cookies {
		usersRequest.AddCookie(cookie)
	}
	usersRecorder := httptest.NewRecorder()
	router.ServeHTTP(usersRecorder, usersRequest)
	if usersRecorder.Code != http.StatusOK {
		t.Fatalf("expected users 200, got %d body=%s", usersRecorder.Code, usersRecorder.Body.String())
	}
	if !strings.Contains(usersRecorder.Body.String(), "\"user_id\":\"admin\"") {
		t.Fatalf("expected admin user in response, got %s", usersRecorder.Body.String())
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/admin/users/admin", nil)
	for _, cookie := range cookies {
		detailRequest.AddCookie(cookie)
	}
	detailRecorder := httptest.NewRecorder()
	router.ServeHTTP(detailRecorder, detailRequest)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("expected user detail 200, got %d body=%s", detailRecorder.Code, detailRecorder.Body.String())
	}
	if !strings.Contains(detailRecorder.Body.String(), "\"user\":{\"id\":\"admin\"") {
		t.Fatalf("expected admin detail payload, got %s", detailRecorder.Body.String())
	}

	cacheRequest := httptest.NewRequest(http.MethodGet, "/api/admin/cache", nil)
	for _, cookie := range cookies {
		cacheRequest.AddCookie(cookie)
	}
	cacheRecorder := httptest.NewRecorder()
	router.ServeHTTP(cacheRecorder, cacheRequest)
	if cacheRecorder.Code != http.StatusOK {
		t.Fatalf("expected cache 200, got %d body=%s", cacheRecorder.Code, cacheRecorder.Body.String())
	}
	if !strings.Contains(cacheRecorder.Body.String(), "\"channel\":\"memstation:cache:invalidate\"") {
		t.Fatalf("expected cache invalidation channel, got %s", cacheRecorder.Body.String())
	}
}

func TestAdminConfigRuntimeUpdates(t *testing.T) {
	t.Parallel()

	router := newTestRouter()
	cookies := loginCookies(t, router)

	updateRequest := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewBufferString(`{"runtime":{"default_provider":"anthropic","message_threshold":12,"provider_timeout_seconds":45}}`))
	updateRequest.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		updateRequest.AddCookie(cookie)
	}
	updateRecorder := httptest.NewRecorder()
	router.ServeHTTP(updateRecorder, updateRequest)
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("expected config update 200, got %d body=%s", updateRecorder.Code, updateRecorder.Body.String())
	}
	if !strings.Contains(updateRecorder.Body.String(), `"default_provider":"anthropic"`) {
		t.Fatalf("expected default provider override, got %s", updateRecorder.Body.String())
	}
	if !strings.Contains(updateRecorder.Body.String(), `"message_threshold":12`) {
		t.Fatalf("expected live message threshold update, got %s", updateRecorder.Body.String())
	}
	if !strings.Contains(updateRecorder.Body.String(), `"provider_timeout_seconds":60`) {
		t.Fatalf("expected live runtime to keep restart-only provider timeout, got %s", updateRecorder.Body.String())
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/admin/config", nil)
	for _, cookie := range cookies {
		getRequest.AddCookie(cookie)
	}
	getRecorder := httptest.NewRecorder()
	router.ServeHTTP(getRecorder, getRequest)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("expected config get 200, got %d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	if !strings.Contains(getRecorder.Body.String(), `"apply_mode":"restart"`) {
		t.Fatalf("expected runtime metadata, got %s", getRecorder.Body.String())
	}
	if !strings.Contains(getRecorder.Body.String(), `"runtime_defaults"`) {
		t.Fatalf("expected runtime defaults payload, got %s", getRecorder.Body.String())
	}
}

func TestAdminProviderDiagnostics(t *testing.T) {
	t.Parallel()

	router := newTestRouter()
	cookies := loginCookies(t, router)

	initialRequest := httptest.NewRequest(http.MethodGet, "/api/admin/providers/diagnostics", nil)
	for _, cookie := range cookies {
		initialRequest.AddCookie(cookie)
	}
	initialRecorder := httptest.NewRecorder()
	router.ServeHTTP(initialRecorder, initialRequest)
	if initialRecorder.Code != http.StatusOK {
		t.Fatalf("expected provider diagnostics 200, got %d body=%s", initialRecorder.Code, initialRecorder.Body.String())
	}
	if !strings.Contains(initialRecorder.Body.String(), `"provider":"openai"`) {
		t.Fatalf("expected openai diagnostics, got %s", initialRecorder.Body.String())
	}
	if !strings.Contains(initialRecorder.Body.String(), `"status":"degraded"`) {
		t.Fatalf("expected degraded default provider before credentials, got %s", initialRecorder.Body.String())
	}

	updateRequest := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewBufferString(`{"providers":{"openai":{"api_key":"sk-openai-test"}}}`))
	updateRequest.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		updateRequest.AddCookie(cookie)
	}
	updateRecorder := httptest.NewRecorder()
	router.ServeHTTP(updateRecorder, updateRequest)
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("expected provider update 200, got %d body=%s", updateRecorder.Code, updateRecorder.Body.String())
	}

	readyRequest := httptest.NewRequest(http.MethodGet, "/api/admin/providers/diagnostics", nil)
	for _, cookie := range cookies {
		readyRequest.AddCookie(cookie)
	}
	readyRecorder := httptest.NewRecorder()
	router.ServeHTTP(readyRecorder, readyRequest)
	if readyRecorder.Code != http.StatusOK {
		t.Fatalf("expected provider diagnostics 200 after update, got %d body=%s", readyRecorder.Code, readyRecorder.Body.String())
	}
	if !strings.Contains(readyRecorder.Body.String(), `"has_api_key":true`) {
		t.Fatalf("expected configured api key in diagnostics, got %s", readyRecorder.Body.String())
	}
	if !strings.Contains(readyRecorder.Body.String(), `"status":"ready"`) {
		t.Fatalf("expected ready diagnostics after credential update, got %s", readyRecorder.Body.String())
	}
}

func TestAdminMemoryEndpoints(t *testing.T) {
	t.Parallel()

	router := newTestRouter()
	cookies := loginCookies(t, router)

	createKeyRequest := httptest.NewRequest(http.MethodPost, "/api/admin/memory/keys", bytes.NewBufferString(`{"key":"favorite_food","value":"dumplings","category":"profile"}`))
	createKeyRequest.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		createKeyRequest.AddCookie(cookie)
	}
	createKeyRecorder := httptest.NewRecorder()
	router.ServeHTTP(createKeyRecorder, createKeyRequest)
	if createKeyRecorder.Code != http.StatusOK {
		t.Fatalf("expected key create 200, got %d body=%s", createKeyRecorder.Code, createKeyRecorder.Body.String())
	}
	var createdKey map[string]any
	if err := json.Unmarshal(createKeyRecorder.Body.Bytes(), &createdKey); err != nil {
		t.Fatalf("unmarshal key create: %v", err)
	}
	keyID := createdKey["id"].(string)

	updateKeyRequest := httptest.NewRequest(http.MethodPut, "/api/admin/memory/keys/"+keyID, bytes.NewBufferString(`{"value":"noodles","category":"preferences","review_status":"approved"}`))
	updateKeyRequest.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		updateKeyRequest.AddCookie(cookie)
	}
	updateKeyRecorder := httptest.NewRecorder()
	router.ServeHTTP(updateKeyRecorder, updateKeyRequest)
	if updateKeyRecorder.Code != http.StatusOK {
		t.Fatalf("expected key update 200, got %d body=%s", updateKeyRecorder.Code, updateKeyRecorder.Body.String())
	}
	if !strings.Contains(updateKeyRecorder.Body.String(), `"value":"noodles"`) {
		t.Fatalf("expected updated key value, got %s", updateKeyRecorder.Body.String())
	}

	getKeyRequest := httptest.NewRequest(http.MethodGet, "/api/admin/memory/keys/"+keyID, nil)
	for _, cookie := range cookies {
		getKeyRequest.AddCookie(cookie)
	}
	getKeyRecorder := httptest.NewRecorder()
	router.ServeHTTP(getKeyRecorder, getKeyRequest)
	if getKeyRecorder.Code != http.StatusOK {
		t.Fatalf("expected key detail 200, got %d body=%s", getKeyRecorder.Code, getKeyRecorder.Body.String())
	}
	if !strings.Contains(getKeyRecorder.Body.String(), `"review_status":"approved"`) {
		t.Fatalf("expected key detail to reflect approved state, got %s", getKeyRecorder.Body.String())
	}

	keysRequest := httptest.NewRequest(http.MethodGet, "/api/admin/memory/keys", nil)
	for _, cookie := range cookies {
		keysRequest.AddCookie(cookie)
	}
	keysRecorder := httptest.NewRecorder()
	router.ServeHTTP(keysRecorder, keysRequest)
	if keysRecorder.Code != http.StatusOK {
		t.Fatalf("expected key list 200, got %d body=%s", keysRecorder.Code, keysRecorder.Body.String())
	}
	if !strings.Contains(keysRecorder.Body.String(), `"favorite_food"`) {
		t.Fatalf("expected key listing, got %s", keysRecorder.Body.String())
	}

	searchRequest := httptest.NewRequest(http.MethodPost, "/api/admin/memory/search", bytes.NewBufferString(`{"query":"favorite food","top_n":5}`))
	searchRequest.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		searchRequest.AddCookie(cookie)
	}
	searchRecorder := httptest.NewRecorder()
	router.ServeHTTP(searchRecorder, searchRequest)
	if searchRecorder.Code != http.StatusOK {
		t.Fatalf("expected memory search 200, got %d body=%s", searchRecorder.Code, searchRecorder.Body.String())
	}

	documentRequest := httptest.NewRequest(http.MethodGet, "/api/admin/memory/documents/summary/summary-seeded", nil)
	for _, cookie := range cookies {
		documentRequest.AddCookie(cookie)
	}
	documentRecorder := httptest.NewRecorder()
	router.ServeHTTP(documentRecorder, documentRequest)
	if documentRecorder.Code != http.StatusOK {
		t.Fatalf("expected document detail 200, got %d body=%s", documentRecorder.Code, documentRecorder.Body.String())
	}
	if !strings.Contains(documentRecorder.Body.String(), `"topic_label":"Project Atlas"`) {
		t.Fatalf("expected summary metadata, got %s", documentRecorder.Body.String())
	}
	if !strings.Contains(documentRecorder.Body.String(), `"related":[{"id":"fact-seeded","type":"fact","weight":0.88}]`) {
		t.Fatalf("expected hydrated related fact, got %s", documentRecorder.Body.String())
	}

	auditRequest := httptest.NewRequest(http.MethodGet, "/api/admin/audit?action=update_key_memory&target_type=key_memory&target_id="+keyID, nil)
	for _, cookie := range cookies {
		auditRequest.AddCookie(cookie)
	}
	auditRecorder := httptest.NewRecorder()
	router.ServeHTTP(auditRecorder, auditRequest)
	if auditRecorder.Code != http.StatusOK {
		t.Fatalf("expected filtered audit 200, got %d body=%s", auditRecorder.Code, auditRecorder.Body.String())
	}
	if !strings.Contains(auditRecorder.Body.String(), `"action":"update_key_memory"`) {
		t.Fatalf("expected filtered audit action, got %s", auditRecorder.Body.String())
	}

	deleteKeyRequest := httptest.NewRequest(http.MethodDelete, "/api/admin/memory/keys/"+keyID, nil)
	for _, cookie := range cookies {
		deleteKeyRequest.AddCookie(cookie)
	}
	deleteKeyRecorder := httptest.NewRecorder()
	router.ServeHTTP(deleteKeyRecorder, deleteKeyRequest)
	if deleteKeyRecorder.Code != http.StatusOK {
		t.Fatalf("expected key delete 200, got %d body=%s", deleteKeyRecorder.Code, deleteKeyRecorder.Body.String())
	}
}

func TestAdminTaskDetailUnavailableWithoutInspector(t *testing.T) {
	t.Parallel()

	router := newTestRouter()
	cookies := loginCookies(t, router)

	request := httptest.NewRequest(http.MethodGet, "/api/admin/tasks/detail?queue=default&id=missing", nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected task detail 503 without inspector, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func newTestRouter() http.Handler {
	memoryRepo := sql.NewInMemoryMemoryRepository()
	conversations := sql.NewInMemoryConversationRepository()
	_ = memoryRepo.StoreDocument(context.Background(), domain.MemoryDocument{
		ID:             "summary-seeded",
		NodeID:         "node-summary-seeded",
		UserID:         "admin",
		ConversationID: "conv-seeded",
		Type:           domain.MemoryTypeSummary,
		Content:        "Project Atlas summary for seeded admin memory coverage.",
		Importance:     0.9,
		DecayFactor:    0.95,
		Metadata: map[string]any{
			"topic_label": "Project Atlas",
		},
	})
	_ = memoryRepo.StoreDocument(context.Background(), domain.MemoryDocument{
		ID:             "fact-seeded",
		NodeID:         "node-fact-seeded",
		UserID:         "admin",
		ConversationID: "conv-seeded",
		Type:           domain.MemoryTypeFact,
		Content:        "Project Atlas ships from the Shanghai workspace.",
		Importance:     0.8,
		DecayFactor:    0.92,
		Metadata: map[string]any{
			"category": "project",
		},
	})
	_ = memoryRepo.LinkDocuments(context.Background(), domain.MemoryRelation{
		UserID:       "admin",
		SrcNodeID:    "node-summary-seeded",
		DstNodeID:    "node-fact-seeded",
		RelationType: "supports",
		Weight:       0.88,
		Source:       "seed",
	})
	cfg := bootstrap.Config{
		Version:                "0.1.0",
		SessionCookieSecure:    false,
		DefaultProvider:        domain.ProviderOpenAI,
		VectorStoreBackend:     "pgvector",
		OpenAIBaseURL:          "https://api.openai.com/v1",
		AnthropicBaseURL:       "https://api.anthropic.com/v1",
		GeminiBaseURL:          "https://generativelanguage.googleapis.com/v1beta",
		ConversationTimeout:    15 * time.Minute,
		ProviderTimeout:        60 * time.Second,
		MessageThreshold:       32,
		TokenThreshold:         64000,
		MaxContextTokens:       8192,
		MaxInjectionTokens:     2048,
		RateLimitRPS:           20,
		RateLimitBurst:         40,
		CacheTTL:               120 * time.Second,
		SessionCookieCrossSite: false,
	}
	systemConfig := systemconfigservice.NewService(nil, systemconfigservice.DefaultsFromConfig(cfg))
	if _, err := systemConfig.Bootstrap(context.Background()); err != nil {
		panic(err)
	}
	encryptor, err := security.NewAESGCMEncryptor("test-master-key")
	if err != nil {
		panic(err)
	}
	providerConfig := providerconfigservice.NewService(nil, encryptor, map[domain.Provider]provider.RuntimeConfig{
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
	settings := settingsservice.NewService(nil, nil, "", "", "", "", 2048)
	audit := auditsvc.NewService(nil)
	admin := adminservice.NewService(conversations, memoryRepo, settings, audit, modelcap.NewRegistry(domain.ProviderOpenAI, "", "", "", ""))
	users := userservice.NewService(nil)
	apiKeys := apikeyservice.NewService(nil)
	usage := usageservice.NewService(nil, conversations)
	admin.AttachUserRuntime(users, apiKeys, usage)
	engine := memoryservice.NewEngine(memoryRepo, vector.NewPGVectorStore(nil))
	memorySvc := memoryservice.NewService(engine, memoryRepo)
	session := security.NewAdminSessionManager(e2eTestAdminToken, time.Hour)
	rateLimiter := security.NewRateLimiter(float64(cfg.RateLimitRPS), cfg.RateLimitBurst)
	scheduler := worker.NewScheduler(nil, nil, nil, cfg.ConversationTimeout)
	gateway := gatewayservice.NewService(
		provider.NewRegistry(
			cfg.DefaultProvider,
			mockprovider.New(cfg.DefaultProvider),
		),
		engine,
		conversationservice.NewService(conversations, sql.NewInMemoryOutboxRepository(), cfg.MessageThreshold, cfg.TokenThreshold),
		settings,
		cfg.DefaultProvider,
	)
	systemConfig.SetLiveApplier(systemconfigservice.LiveApplier{
		SetDefaultProvider: func(providerName domain.Provider) {
			gateway.SetDefaultProvider(providerName)
		},
		SetThresholds: func(messageThreshold, tokenThreshold int) {},
		SetConversationTimeout: func(timeout time.Duration) {
			scheduler.UpdateTimeoutWindow(timeout)
		},
		SetRateLimit: func(rps, burst int) {
			rateLimiter.Update(float64(rps), burst)
		},
	})

	return httptransport.NewRouter(httptransport.Dependencies{
		Config:         cfg,
		Health:         observability.NewRegistry(),
		Auth:           security.Authenticator{AdminToken: e2eTestAdminToken, DevAPIKey: "ms-local-dev", SessionManager: session},
		RateLimiter:    rateLimiter,
		Session:        session,
		Models:         modelcap.NewRegistry(domain.ProviderOpenAI, "", "", "", ""),
		Gateway:        gateway,
		Memory:         memorySvc,
		Admin:          admin,
		Audit:          audit,
		Usage:          usage,
		Users:          users,
		APIKeys:        apiKeys,
		Settings:       settings,
		SystemConfig:   systemConfig,
		ProviderConfig: providerConfig,
	})
}

func newProxyRouter() http.Handler {
	memoryRepo := sql.NewInMemoryMemoryRepository()
	conversations := sql.NewInMemoryConversationRepository()
	outbox := sql.NewInMemoryOutboxRepository()
	settings := settingsservice.NewService(nil, nil, "", "", "", "", 2048)
	audit := auditsvc.NewService(nil)
	admin := adminservice.NewService(conversations, memoryRepo, settings, audit, modelcap.NewRegistry(domain.ProviderOpenAI, "", "", "", ""))
	users := userservice.NewService(nil)
	apiKeys := apikeyservice.NewService(nil)
	usage := usageservice.NewService(nil, conversations)
	admin.AttachUserRuntime(users, apiKeys, usage)
	engine := memoryservice.NewEngine(memoryRepo, vector.NewPGVectorStore(nil))
	memorySvc := memoryservice.NewService(engine, memoryRepo)
	session := security.NewAdminSessionManager(e2eTestAdminToken, time.Hour)
	gateway := gatewayservice.NewService(
		provider.NewRegistry(
			domain.ProviderOpenAI,
			mockprovider.New(domain.ProviderOpenAI),
		),
		engine,
		conversationservice.NewService(conversations, outbox, 100, 100000),
		settings,
		domain.ProviderOpenAI,
	)

	return httptransport.NewRouter(httptransport.Dependencies{
		Config:   bootstrap.Config{Version: "0.1.0", SessionCookieSecure: false},
		Health:   observability.NewRegistry(),
		Auth:     security.Authenticator{AdminToken: e2eTestAdminToken, DevAPIKey: "ms-local-dev", SessionManager: session},
		Session:  session,
		Models:   modelcap.NewRegistry(domain.ProviderOpenAI, "", "", "", ""),
		Gateway:  gateway,
		Memory:   memorySvc,
		Admin:    admin,
		Audit:    audit,
		Usage:    usage,
		Users:    users,
		APIKeys:  apiKeys,
		Settings: settings,
	})
}

func loginCookies(t *testing.T, router http.Handler) []*http.Cookie {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewBufferString(`{"token":"`+e2eTestAdminToken+`"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected login 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	return recorder.Result().Cookies()
}
