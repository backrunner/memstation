package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
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
	analysisservice "memstation/apps/server/internal/service/analysis"
	apikeyservice "memstation/apps/server/internal/service/apikey"
	auditsvc "memstation/apps/server/internal/service/audit"
	conversationservice "memstation/apps/server/internal/service/conversation"
	gatewayservice "memstation/apps/server/internal/service/gateway"
	memoryservice "memstation/apps/server/internal/service/memory"
	multimodalservice "memstation/apps/server/internal/service/multimodal"
	providerconfigservice "memstation/apps/server/internal/service/providerconfig"
	settingsservice "memstation/apps/server/internal/service/settings"
	systemconfigservice "memstation/apps/server/internal/service/systemconfig"
	usageservice "memstation/apps/server/internal/service/usage"
	userservice "memstation/apps/server/internal/service/user"
	blobstore "memstation/apps/server/internal/store/blob"
	"memstation/apps/server/internal/store/sql"
	"memstation/apps/server/internal/store/vector"
	"memstation/apps/server/internal/testutil/memoryllmfixture"
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

func TestAdminHTTPRoutesRequireSessionCookieNotBearerToken(t *testing.T) {
	t.Parallel()
	router := newTestRouter()

	bearerRequest := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil)
	bearerRequest.Header.Set("Authorization", "Bearer "+e2eTestAdminToken)
	bearerRecorder := httptest.NewRecorder()
	router.ServeHTTP(bearerRecorder, bearerRequest)
	if bearerRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected bearer admin token to be rejected, got %d body=%s", bearerRecorder.Code, bearerRecorder.Body.String())
	}

	headerRequest := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil)
	headerRequest.Header.Set("X-MemStation-Admin-Token", e2eTestAdminToken)
	headerRecorder := httptest.NewRecorder()
	router.ServeHTTP(headerRecorder, headerRequest)
	if headerRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected header admin token to be rejected, got %d body=%s", headerRecorder.Code, headerRecorder.Body.String())
	}

	cookies := loginCookies(t, router)
	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil)
	for _, cookie := range cookies {
		sessionRequest.AddCookie(cookie)
	}
	sessionRecorder := httptest.NewRecorder()
	router.ServeHTTP(sessionRecorder, sessionRequest)
	if sessionRecorder.Code != http.StatusOK {
		t.Fatalf("expected session cookie to be accepted, got %d body=%s", sessionRecorder.Code, sessionRecorder.Body.String())
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

func TestProxyFileUploadDownloadDeleteLifecycle(t *testing.T) {
	t.Parallel()

	router := newMultimodalProxyRouter(t, t.TempDir())

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="note.txt"`)
	header.Set("Content-Type", "text/plain")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := io.WriteString(part, "hello multimodal upload"); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.WriteField("metadata", `{"kind":"note"}`); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	uploadRequest := httptest.NewRequest(http.MethodPost, "/v1/files", &body)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	uploadRequest.Header.Set("Authorization", "Bearer ms-local-dev")
	uploadRecorder := httptest.NewRecorder()
	router.ServeHTTP(uploadRecorder, uploadRequest)
	if uploadRecorder.Code != http.StatusCreated {
		t.Fatalf("expected upload 201, got %d body=%s", uploadRecorder.Code, uploadRecorder.Body.String())
	}

	var uploadPayload map[string]any
	if err := json.Unmarshal(uploadRecorder.Body.Bytes(), &uploadPayload); err != nil {
		t.Fatalf("unmarshal upload payload: %v body=%s", err, uploadRecorder.Body.String())
	}
	blobID, _ := uploadPayload["blob_id"].(string)
	sourceURL, _ := uploadPayload["source_url"].(string)
	if blobID == "" || sourceURL == "" {
		t.Fatalf("expected blob metadata, got %+v", uploadPayload)
	}

	getRequest := httptest.NewRequest(http.MethodGet, sourceURL, nil)
	getRequest.Header.Set("Authorization", "Bearer ms-local-dev")
	getRecorder := httptest.NewRecorder()
	router.ServeHTTP(getRecorder, getRequest)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("expected get 200, got %d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	if getRecorder.Body.String() != "hello multimodal upload" {
		t.Fatalf("unexpected blob body: %q", getRecorder.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/v1/files/"+blobID, nil)
	deleteRequest.Header.Set("Authorization", "Bearer ms-local-dev")
	deleteRecorder := httptest.NewRecorder()
	router.ServeHTTP(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("expected delete 204, got %d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}

	missingRequest := httptest.NewRequest(http.MethodGet, sourceURL, nil)
	missingRequest.Header.Set("Authorization", "Bearer ms-local-dev")
	missingRecorder := httptest.NewRecorder()
	router.ServeHTTP(missingRecorder, missingRequest)
	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("expected deleted blob to return 404, got %d body=%s", missingRecorder.Code, missingRecorder.Body.String())
	}
}

func TestCORSPreflightAllowsConfiguredManagerOrigin(t *testing.T) {
	t.Parallel()

	memoryRepo := sql.NewInMemoryMemoryRepository()
	conversations := sql.NewInMemoryConversationRepository()
	mediaRepo := sql.NewInMemoryMediaRepository()
	settings := settingsservice.NewService(nil, nil, "", "", "", "", 2048)
	audit := auditsvc.NewService(nil)
	admin := adminservice.NewService(conversations, mediaRepo, memoryRepo, settings, audit, modelcap.NewRegistry(domain.ProviderOpenAI, "", "", "", ""))
	admin.SetMemoryProcessor(analysisservice.NewService(memoryllmfixture.Generator{}), []string{"preferences"})
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

func TestCORSDefaultsReflectIncomingOrigins(t *testing.T) {
	t.Parallel()

	router := newTestRouter()

	adminPreflight := httptest.NewRequest(http.MethodOptions, "/api/admin/session", nil)
	adminPreflight.Header.Set("Origin", "https://manager.example")
	adminPreflight.Header.Set("Access-Control-Request-Method", "GET")
	adminPreflight.Header.Set("Access-Control-Request-Headers", "X-MemStation-Admin-Token, Content-Type")
	adminRecorder := httptest.NewRecorder()
	router.ServeHTTP(adminRecorder, adminPreflight)

	if adminRecorder.Code != http.StatusNoContent {
		t.Fatalf("expected admin preflight 204, got %d", adminRecorder.Code)
	}
	if got := adminRecorder.Header().Get("Access-Control-Allow-Origin"); got != "https://manager.example" {
		t.Fatalf("expected reflected admin origin, got %q", got)
	}
	if got := adminRecorder.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("expected credentials header, got %q", got)
	}
	if got := adminRecorder.Header().Get("Access-Control-Allow-Headers"); got != "X-MemStation-Admin-Token, Content-Type" {
		t.Fatalf("expected requested headers to be reflected, got %q", got)
	}

	proxyPreflight := httptest.NewRequest(http.MethodOptions, "/v1/models", nil)
	proxyPreflight.Header.Set("Origin", "https://client.example")
	proxyPreflight.Header.Set("Access-Control-Request-Method", "GET")
	proxyRecorder := httptest.NewRecorder()
	router.ServeHTTP(proxyRecorder, proxyPreflight)

	if proxyRecorder.Code != http.StatusNoContent {
		t.Fatalf("expected proxy preflight 204, got %d", proxyRecorder.Code)
	}
	if got := proxyRecorder.Header().Get("Access-Control-Allow-Origin"); got != "https://client.example" {
		t.Fatalf("expected reflected proxy origin, got %q", got)
	}
	if got := proxyRecorder.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("expected credentials header, got %q", got)
	}
}

func TestAdminOriginPolicyRejectsOriginsOutsideConfiguredWhitelist(t *testing.T) {
	t.Parallel()

	session := security.NewAdminSessionManager(e2eTestAdminToken, time.Hour)
	router := httptransport.NewRouter(httptransport.Dependencies{
		Config: bootstrap.Config{
			Version:             "0.1.0",
			SessionCookieSecure: false,
			CORSAllowedOrigins:  []string{"https://manager.example"},
		},
		Health:  observability.NewRegistry(),
		Auth:    security.Authenticator{AdminToken: e2eTestAdminToken, SessionManager: session},
		Session: session,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewBufferString(`{"token":"`+e2eTestAdminToken+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden origin, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(recorder.Result().Cookies()) != 0 {
		t.Fatal("expected rejected login not to set cookies")
	}
}

func TestCORSWildcardDoesNotEnableCredentialedOrigins(t *testing.T) {
	t.Parallel()

	session := security.NewAdminSessionManager(e2eTestAdminToken, time.Hour)
	router := httptransport.NewRouter(httptransport.Dependencies{
		Config: bootstrap.Config{
			Version:             "0.1.0",
			SessionCookieSecure: false,
			CORSAllowedOrigins:  []string{"*"},
		},
		Health:  observability.NewRegistry(),
		Auth:    security.Authenticator{AdminToken: e2eTestAdminToken, SessionManager: session},
		Session: session,
	})

	request := httptest.NewRequest(http.MethodOptions, "/api/admin/session", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	request.Header.Set("Access-Control-Request-Method", "GET")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected wildcard not to grant credentialed origin, got %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("expected wildcard not to grant credentials, got %q", got)
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

	updateRequest := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewBufferString(`{"runtime":{"default_provider":"anthropic","message_threshold":12,"provider_timeout_seconds":45,"rerank_model":"jina:jina-reranker-v2-base-multilingual","rerank_candidate_multiplier":8,"rerank_min_candidates":24,"rerank_max_candidates":80,"rerank_blend_weight":0.5,"memory_relation_max_hops":3,"memory_relation_hop_decay_lambda":0.5,"memory_relation_hop_decay_exponent":1.6,"multimodal_enabled":false,"multimodal_storage_backend":"local","multimodal_storage_local_root":"./data/runtime-test-blobs","multimodal_extract_image_model":"vision-v2","multimodal_audio_transcription_enabled":false,"multimodal_max_asset_bytes":2048,"multimodal_allowed_mime_types":["image/png","audio/mpeg"]}}`))
	updateRequest.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		updateRequest.AddCookie(cookie)
	}
	updateRecorder := httptest.NewRecorder()
	router.ServeHTTP(updateRecorder, updateRequest)
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("expected config update 200, got %d body=%s", updateRecorder.Code, updateRecorder.Body.String())
	}
	var updatePayload map[string]any
	if err := json.Unmarshal(updateRecorder.Body.Bytes(), &updatePayload); err != nil {
		t.Fatalf("unmarshal update payload: %v body=%s", err, updateRecorder.Body.String())
	}
	runtimePayload := updatePayload["runtime"].(map[string]any)
	runtimeLive := updatePayload["runtime_live"].(map[string]any)
	if !strings.Contains(updateRecorder.Body.String(), `"default_provider":"anthropic"`) {
		t.Fatalf("expected default provider override, got %s", updateRecorder.Body.String())
	}
	if !strings.Contains(updateRecorder.Body.String(), `"message_threshold":12`) {
		t.Fatalf("expected live message threshold update, got %s", updateRecorder.Body.String())
	}
	if !strings.Contains(updateRecorder.Body.String(), `"provider_timeout_seconds":45`) {
		t.Fatalf("expected live provider timeout update, got %s", updateRecorder.Body.String())
	}
	if runtimePayload["memory_relation_max_hops"] != float64(3) {
		t.Fatalf("expected relation max hops override, got %#v", runtimePayload["memory_relation_max_hops"])
	}
	if runtimePayload["rerank_model"] != "jina:jina-reranker-v2-base-multilingual" {
		t.Fatalf("expected rerank model override, got %#v", runtimePayload["rerank_model"])
	}
	if runtimeLive["rerank_model"] != "jina:jina-reranker-v2-base-multilingual" {
		t.Fatalf("expected live rerank model update, got %#v", runtimeLive["rerank_model"])
	}
	if runtimeLive["rerank_candidate_multiplier"] != float64(8) {
		t.Fatalf("expected live rerank candidate multiplier update, got %#v", runtimeLive["rerank_candidate_multiplier"])
	}
	if runtimeLive["rerank_min_candidates"] != float64(24) {
		t.Fatalf("expected live rerank min candidates update, got %#v", runtimeLive["rerank_min_candidates"])
	}
	if runtimeLive["rerank_max_candidates"] != float64(80) {
		t.Fatalf("expected live rerank max candidates update, got %#v", runtimeLive["rerank_max_candidates"])
	}
	if runtimeLive["rerank_blend_weight"] != 0.5 {
		t.Fatalf("expected live rerank blend weight update, got %#v", runtimeLive["rerank_blend_weight"])
	}
	if runtimeLive["memory_relation_max_hops"] != float64(3) {
		t.Fatalf("expected live relation max hops update, got %#v", runtimeLive["memory_relation_max_hops"])
	}
	if runtimeLive["memory_relation_hop_decay_lambda"] != 0.5 {
		t.Fatalf("expected live relation decay lambda update, got %#v", runtimeLive["memory_relation_hop_decay_lambda"])
	}
	if runtimeLive["memory_relation_hop_decay_exponent"] != 1.6 {
		t.Fatalf("expected live relation decay exponent update, got %#v", runtimeLive["memory_relation_hop_decay_exponent"])
	}
	if runtimePayload["multimodal_storage_backend"] != "local" {
		t.Fatalf("expected multimodal storage backend override, got %#v", runtimePayload["multimodal_storage_backend"])
	}
	if runtimePayload["multimodal_extract_image_model"] != "vision-v2" {
		t.Fatalf("expected image extractor override, got %#v", runtimePayload["multimodal_extract_image_model"])
	}
	if runtimeLive["multimodal_storage_backend"] != "local" {
		t.Fatalf("expected live multimodal storage backend update, got %#v", runtimeLive["multimodal_storage_backend"])
	}
	if runtimeLive["multimodal_audio_transcription_enabled"] != false {
		t.Fatalf("expected live multimodal transcription flag update, got %#v", runtimeLive["multimodal_audio_transcription_enabled"])
	}
	if runtimeLive["multimodal_storage_local_root"] != "./data/runtime-test-blobs" {
		t.Fatalf("expected live multimodal local root update, got %#v", runtimeLive["multimodal_storage_local_root"])
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
	if !strings.Contains(getRecorder.Body.String(), `"apply_mode":"live"`) {
		t.Fatalf("expected runtime metadata, got %s", getRecorder.Body.String())
	}
	if !strings.Contains(getRecorder.Body.String(), `"runtime_defaults"`) {
		t.Fatalf("expected runtime defaults payload, got %s", getRecorder.Body.String())
	}
	if !strings.Contains(getRecorder.Body.String(), `"rerank_model"`) {
		t.Fatalf("expected rerank runtime field in config payload, got %s", getRecorder.Body.String())
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
	var initialPayload struct {
		Items []providerconfigservice.Diagnostic `json:"items"`
	}
	if err := json.Unmarshal(initialRecorder.Body.Bytes(), &initialPayload); err != nil {
		t.Fatalf("unmarshal diagnostics: %v body=%s", err, initialRecorder.Body.String())
	}
	gatewayOpenAI := findProviderDiagnostic(initialPayload.Items, providerconfigservice.ScopeGateway, domain.ProviderOpenAICompatible)
	if gatewayOpenAI == nil {
		t.Fatalf("expected gateway openai_compatible diagnostics, got %+v", initialPayload.Items)
	}
	if gatewayOpenAI.Status != "degraded" {
		t.Fatalf("expected degraded gateway default provider before credentials, got %+v", gatewayOpenAI)
	}

	updateRequest := httptest.NewRequest(http.MethodPut, "/api/admin/config", bytes.NewBufferString(`{"provider_sets":{"gateway":{"openai_compatible":{"api_key":"sk-openai-test"}}}}`))
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
	var readyPayload struct {
		Items []providerconfigservice.Diagnostic `json:"items"`
	}
	if err := json.Unmarshal(readyRecorder.Body.Bytes(), &readyPayload); err != nil {
		t.Fatalf("unmarshal diagnostics after update: %v body=%s", err, readyRecorder.Body.String())
	}
	gatewayOpenAI = findProviderDiagnostic(readyPayload.Items, providerconfigservice.ScopeGateway, domain.ProviderOpenAICompatible)
	if gatewayOpenAI == nil {
		t.Fatalf("expected gateway openai_compatible diagnostics after update, got %+v", readyPayload.Items)
	}
	if !gatewayOpenAI.HasAPIKey {
		t.Fatalf("expected configured api key in diagnostics, got %+v", gatewayOpenAI)
	}
	if gatewayOpenAI.Status != "ready" {
		t.Fatalf("expected ready diagnostics after credential update, got %+v", gatewayOpenAI)
	}
	rerankJina := findProviderDiagnostic(readyPayload.Items, providerconfigservice.ScopeRerank, domain.ProviderJina)
	if rerankJina == nil {
		t.Fatalf("expected rerank jina diagnostics, got %+v", readyPayload.Items)
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
	for _, expected := range []string{
		`"node_id":"node-fact-seeded"`,
		`"document_id":"fact-seeded"`,
		`"document_type":"fact"`,
		`"relation_type":"supports"`,
		`"weight":0.88`,
	} {
		if !strings.Contains(documentRecorder.Body.String(), expected) {
			t.Fatalf("expected hydrated related fact field %s, got %s", expected, documentRecorder.Body.String())
		}
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
		Version:                        "0.1.0",
		SessionCookieSecure:            false,
		DefaultProvider:                domain.ProviderOpenAICompatible,
		InternalDefaultProvider:        domain.ProviderOpenAICompatible,
		VectorStoreBackend:             "pgvector",
		OpenAIBaseURL:                  "https://api.openai.com/v1",
		AnthropicBaseURL:               "https://api.anthropic.com/v1",
		GeminiBaseURL:                  "https://generativelanguage.googleapis.com/v1beta",
		EmbeddingBaseURL:               "https://api.openai.com/v1",
		CohereBaseURL:                  "https://api.cohere.com/v2",
		JinaBaseURL:                    "https://api.jina.ai/v1",
		ConversationTimeout:            15 * time.Minute,
		ProviderTimeout:                60 * time.Second,
		MessageThreshold:               32,
		TokenThreshold:                 64000,
		MaxContextTokens:               8192,
		MaxInjectionTokens:             2048,
		RerankModel:                    "cohere:rerank-v3.5",
		RerankCandidateMultiplier:      6,
		RerankMinCandidates:            20,
		RerankMaxCandidates:            50,
		RerankBlendWeight:              0.35,
		MemoryRelationMaxHops:          2,
		MemoryRelationHopDecayLambda:   0.35,
		MemoryRelationHopDecayExponent: 1.25,
		RateLimitRPS:                   20,
		RateLimitBurst:                 40,
		CacheTTL:                       120 * time.Second,
		SessionCookieCrossSite:         false,
		Multimodal: bootstrap.MultimodalConfig{
			Enabled:                   true,
			StorageBackend:            "local",
			StorageLocalRoot:          "./data/blobs",
			AudioTranscriptionEnabled: true,
			VideoFrameSamplingEnabled: true,
			MaxAssetBytes:             104857600,
			AllowedMIMETypes:          []string{"image/png", "application/pdf", "audio/mpeg"},
		},
	}
	systemConfig := systemconfigservice.NewService(nil, systemconfigservice.DefaultsFromConfig(cfg))
	if _, err := systemConfig.Bootstrap(context.Background()); err != nil {
		panic(err)
	}
	encryptor, err := security.NewAESGCMEncryptor("test-master-key")
	if err != nil {
		panic(err)
	}
	providerConfig := providerconfigservice.NewService(nil, encryptor, providerconfigservice.Defaults{
		providerconfigservice.ScopeGateway: {
			domain.ProviderOpenAICompatible: {
				APIKey:  cfg.OpenAIAPIKey,
				BaseURL: cfg.OpenAIBaseURL,
			},
			domain.ProviderOpenAIResponses: {
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
		},
		providerconfigservice.ScopeInternal: {
			domain.ProviderOpenAICompatible: {
				APIKey:  cfg.OpenAIAPIKey,
				BaseURL: cfg.OpenAIBaseURL,
			},
			domain.ProviderOpenAIResponses: {
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
		},
		providerconfigservice.ScopeEmbedding: {
			domain.ProviderOpenAICompatible: {
				APIKey:  cfg.EmbeddingAPIKey,
				BaseURL: cfg.EmbeddingBaseURL,
			},
		},
		providerconfigservice.ScopeRerank: {
			domain.ProviderCohere: {
				APIKey:  cfg.CohereAPIKey,
				BaseURL: cfg.CohereBaseURL,
			},
			domain.ProviderJina: {
				APIKey:  cfg.JinaAPIKey,
				BaseURL: cfg.JinaBaseURL,
			},
		},
	})
	settings := settingsservice.NewService(nil, nil, "", "", "", "", 2048)
	audit := auditsvc.NewService(nil)
	admin := adminservice.NewService(conversations, sql.NewInMemoryMediaRepository(), memoryRepo, settings, audit, modelcap.NewRegistry(domain.ProviderOpenAI, "", "", "", ""))
	admin.SetMemoryProcessor(analysisservice.NewService(memoryllmfixture.Generator{}), []string{"preferences"})
	users := userservice.NewService(nil)
	apiKeys := apikeyservice.NewService(nil)
	usage := usageservice.NewService(nil, conversations)
	admin.AttachUserRuntime(users, apiKeys, usage)
	engine := memoryservice.NewEngine(memoryRepo, vector.NewPGVectorStore(nil))
	engine.SetRerankConfig(memoryservice.RerankConfig{
		Model:               cfg.RerankModel,
		CandidateMultiplier: cfg.RerankCandidateMultiplier,
		MinCandidates:       cfg.RerankMinCandidates,
		MaxCandidates:       cfg.RerankMaxCandidates,
		BlendWeight:         cfg.RerankBlendWeight,
	})
	engine.SetRelationRecallConfig(memoryservice.RelationRecallConfig{
		MaxHops:          cfg.MemoryRelationMaxHops,
		HopDecayLambda:   cfg.MemoryRelationHopDecayLambda,
		HopDecayExponent: cfg.MemoryRelationHopDecayExponent,
	})
	memorySvc := memoryservice.NewService(engine, memoryRepo)
	session := security.NewAdminSessionManager(e2eTestAdminToken, time.Hour)
	rateLimiter := security.NewRateLimiter(float64(cfg.RateLimitRPS), cfg.RateLimitBurst)
	scheduler := worker.NewScheduler(nil, nil, nil, cfg.ConversationTimeout)
	gateway := gatewayservice.NewService(
		provider.NewRegistry(
			cfg.DefaultProvider,
			mockprovider.New(domain.ProviderOpenAICompatible),
			mockprovider.New(domain.ProviderOpenAIResponses),
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
		SetInternalDefaultProvider: func(providerName domain.Provider) {},
		SetVectorStoreBackend:      func(string) {},
		SetThresholds:              func(messageThreshold, tokenThreshold int) {},
		SetConversationTimeout: func(timeout time.Duration) {
			scheduler.UpdateTimeoutWindow(timeout)
		},
		SetProviderTimeout:    func(time.Duration) {},
		SetMaxContextTokens:   func(int) {},
		SetMaxInjectionTokens: func(int) {},
		SetRerankConfig: func(model string, candidateMultiplier, minCandidates, maxCandidates int, blendWeight float64) {
			engine.SetRerankConfig(memoryservice.RerankConfig{
				Model:               model,
				CandidateMultiplier: candidateMultiplier,
				MinCandidates:       minCandidates,
				MaxCandidates:       maxCandidates,
				BlendWeight:         blendWeight,
			})
		},
		SetMemoryRelationRecall: func(maxHops int, decayLambda, decayExponent float64) {
			engine.SetRelationRecallConfig(memoryservice.RelationRecallConfig{
				MaxHops:          maxHops,
				HopDecayLambda:   decayLambda,
				HopDecayExponent: decayExponent,
			})
		},
		SetRateLimit: func(rps, burst int) {
			rateLimiter.Update(float64(rps), burst)
		},
		SetCacheTTL:         func(time.Duration) {},
		SetMultimodalConfig: func(bootstrap.MultimodalConfig) {},
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
	admin := adminservice.NewService(conversations, sql.NewInMemoryMediaRepository(), memoryRepo, settings, audit, modelcap.NewRegistry(domain.ProviderOpenAI, "", "", "", ""))
	admin.SetMemoryProcessor(analysisservice.NewService(memoryllmfixture.Generator{}), []string{"preferences"})
	users := userservice.NewService(nil)
	apiKeys := apikeyservice.NewService(nil)
	usage := usageservice.NewService(nil, conversations)
	admin.AttachUserRuntime(users, apiKeys, usage)
	engine := memoryservice.NewEngine(memoryRepo, vector.NewPGVectorStore(nil))
	memorySvc := memoryservice.NewService(engine, memoryRepo)
	session := security.NewAdminSessionManager(e2eTestAdminToken, time.Hour)
	gateway := gatewayservice.NewService(
		provider.NewRegistry(
			domain.ProviderOpenAICompatible,
			mockprovider.New(domain.ProviderOpenAICompatible),
			mockprovider.New(domain.ProviderOpenAIResponses),
		),
		engine,
		conversationservice.NewService(conversations, outbox, 100, 100000),
		settings,
		domain.ProviderOpenAICompatible,
	)

	return httptransport.NewRouter(httptransport.Dependencies{
		Config: bootstrap.Config{
			Version:             "0.1.0",
			SessionCookieSecure: false,
			Multimodal: bootstrap.MultimodalConfig{
				Enabled:                   true,
				StorageBackend:            "local",
				StorageLocalRoot:          "./data/blobs",
				AudioTranscriptionEnabled: true,
				VideoFrameSamplingEnabled: true,
				MaxAssetBytes:             104857600,
				AllowedMIMETypes:          []string{"text/plain", "image/png", "application/pdf", "audio/mpeg"},
			},
		},
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

func newMultimodalProxyRouter(t *testing.T, root string) http.Handler {
	t.Helper()

	mediaRepo := sql.NewInMemoryMediaRepository()
	blobStore, err := blobstore.NewStore(bootstrap.MultimodalConfig{
		Enabled:                   true,
		StorageBackend:            "local",
		StorageLocalRoot:          root,
		AudioTranscriptionEnabled: true,
		VideoFrameSamplingEnabled: true,
		MaxAssetBytes:             1 << 20,
		AllowedMIMETypes:          []string{"text/plain"},
	})
	if err != nil {
		t.Fatalf("create blob store: %v", err)
	}

	return httptransport.NewRouter(httptransport.Dependencies{
		Config: bootstrap.Config{
			Version:             "0.1.0",
			SessionCookieSecure: false,
			Multimodal: bootstrap.MultimodalConfig{
				Enabled:                   true,
				StorageBackend:            "local",
				StorageLocalRoot:          root,
				AudioTranscriptionEnabled: true,
				VideoFrameSamplingEnabled: true,
				MaxAssetBytes:             1 << 20,
				AllowedMIMETypes:          []string{"text/plain"},
			},
		},
		Health: observability.NewRegistry(),
		Auth:   security.Authenticator{AdminToken: e2eTestAdminToken, DevAPIKey: "ms-local-dev"},
		Multimodal: multimodalservice.NewService(mediaRepo, blobStore, bootstrap.MultimodalConfig{
			Enabled:                   true,
			StorageBackend:            "local",
			StorageLocalRoot:          root,
			AudioTranscriptionEnabled: true,
			VideoFrameSamplingEnabled: true,
			MaxAssetBytes:             1 << 20,
			AllowedMIMETypes:          []string{"text/plain"},
		}),
	})
}

func findProviderDiagnostic(items []providerconfigservice.Diagnostic, scope providerconfigservice.Scope, providerName domain.Provider) *providerconfigservice.Diagnostic {
	for i := range items {
		if items[i].Scope == scope && items[i].Provider == providerName {
			return &items[i]
		}
	}
	return nil
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
