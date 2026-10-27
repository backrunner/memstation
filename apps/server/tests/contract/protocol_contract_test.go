package contract_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	mediastore "memstation/apps/server/internal/store/sql/media"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/testutil/authfixture"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/modelcap"
	"memstation/apps/server/internal/observability"
	"memstation/apps/server/internal/provider"
	anthropicprovider "memstation/apps/server/internal/provider/anthropic"
	openaiprovider "memstation/apps/server/internal/provider/openai"
	"memstation/apps/server/internal/security"
	adminservice "memstation/apps/server/internal/service/admin"
	auditsvc "memstation/apps/server/internal/service/audit"
	conversationservice "memstation/apps/server/internal/service/conversation"
	gatewayservice "memstation/apps/server/internal/service/gateway"
	memoryservice "memstation/apps/server/internal/service/memory"
	settingsservice "memstation/apps/server/internal/service/settings"
	"memstation/apps/server/internal/store/vector"
	httptransport "memstation/apps/server/internal/transport/http"
)

const contractTestAdminToken = "contract-test-admin-token"

func TestOpenAIChatCompletionsContractNonStream(t *testing.T) {
	t.Parallel()
	router := newContractRouter(t)

	recorder := performJSONRequest(t, router, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{{
			"role":    "user",
			"content": "hello",
		}},
	}, nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	decodeJSON(t, recorder.Body.Bytes(), &payload)
	if payload["object"] != "chat.completion" {
		t.Fatalf("unexpected object: %v", payload["object"])
	}
	choices := payload["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	if message["content"] != "hello chat" {
		t.Fatalf("unexpected content: %v", message["content"])
	}
}

func TestOpenAIChatCompletionsContractStream(t *testing.T) {
	t.Parallel()
	router := newContractRouter(t)

	recorder := performJSONRequest(t, router, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":  "gpt-4.1-mini",
		"stream": true,
		"messages": []map[string]any{{
			"role":    "user",
			"content": "hello",
		}},
	}, nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("expected event-stream content type, got %s", contentType)
	}
	body := recorder.Body.String()
	for _, expected := range []string{"chat.completion.chunk", "hello ", "world", "[DONE]"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected stream body to contain %q, got %s", expected, body)
		}
	}
}

func TestOpenAIResponsesContractNonStream(t *testing.T) {
	t.Parallel()
	router := newContractRouter(t)

	recorder := performJSONRequest(t, router, http.MethodPost, "/v1/responses", map[string]any{
		"model": "gpt-4.1-mini",
		"input": "hello",
	}, nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	decodeJSON(t, recorder.Body.Bytes(), &payload)
	if payload["object"] != "response" {
		t.Fatalf("unexpected object: %v", payload["object"])
	}
	output := payload["output"].([]any)
	content := output[0].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["text"] != "hello response" {
		t.Fatalf("unexpected response text: %v", content[0])
	}
}

func TestOpenAIResponsesContractStream(t *testing.T) {
	t.Parallel()
	router := newContractRouter(t)

	recorder := performJSONRequest(t, router, http.MethodPost, "/v1/responses", map[string]any{
		"model":  "gpt-4.1-mini",
		"stream": true,
		"input":  "hello",
	}, nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, expected := range []string{"response.created", "response.output_text.delta", "hello response stream", "response.completed"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected stream body to contain %q, got %s", expected, body)
		}
	}
}

func TestAnthropicMessagesContractNonStream(t *testing.T) {
	t.Parallel()
	router := newContractRouter(t)

	recorder := performJSONRequest(t, router, http.MethodPost, "/v1/messages", map[string]any{
		"model": "anthropic:claude-3-7-sonnet",
		"messages": []map[string]any{{
			"role":    "user",
			"content": "hello",
		}},
	}, nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	decodeJSON(t, recorder.Body.Bytes(), &payload)
	if payload["type"] != "message" {
		t.Fatalf("unexpected type: %v", payload["type"])
	}
	content := payload["content"].([]any)
	if content[0].(map[string]any)["text"] != "hello anthropic" {
		t.Fatalf("unexpected anthropic text: %v", content[0])
	}
}

func TestAnthropicMessagesContractStream(t *testing.T) {
	t.Parallel()
	router := newContractRouter(t)

	recorder := performJSONRequest(t, router, http.MethodPost, "/v1/messages", map[string]any{
		"model":  "anthropic:claude-3-7-sonnet",
		"stream": true,
		"messages": []map[string]any{{
			"role":    "user",
			"content": "hello",
		}},
	}, nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, expected := range []string{"event: message_start", "event: content_block_delta", "hello anthropic stream", "event: message_stop"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected anthropic stream to contain %q, got %s", expected, body)
		}
	}
}

func newContractRouter(t *testing.T) http.Handler {
	t.Helper()

	openaiUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-openai" {
			t.Fatalf("unexpected openai auth header: %s", got)
		}
		var payload map[string]any
		decodeJSON(t, mustReadAll(t, r), &payload)

		switch r.URL.Path {
		case "/chat/completions":
			if payload["stream"] == true {
				writeSSE(t, w, []string{
					fmt.Sprintf("data: %s\n\n", `{"id":"chatcmpl_contract","created":1710000000,"model":"gpt-4.1-mini","choices":[{"delta":{"role":"assistant","content":"hello "}}]}`),
					fmt.Sprintf("data: %s\n\n", `{"id":"chatcmpl_contract","created":1710000000,"model":"gpt-4.1-mini","choices":[{"delta":{"content":"world"}}]}`),
					fmt.Sprintf("data: %s\n\n", `{"id":"chatcmpl_contract","created":1710000000,"model":"gpt-4.1-mini","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":9,"total_tokens":21}}`),
					"data: [DONE]\n\n",
				})
				return
			}
			writeJSON(t, w, map[string]any{
				"id":      "chatcmpl_contract",
				"created": time.Now().Unix(),
				"model":   "gpt-4.1-mini",
				"choices": []map[string]any{{
					"message": map[string]any{
						"role":    "assistant",
						"content": "hello chat",
					},
				}},
				"usage": map[string]any{
					"prompt_tokens":     11,
					"completion_tokens": 7,
					"total_tokens":      18,
				},
			})
		case "/responses":
			if payload["stream"] == true {
				writeSSE(t, w, []string{
					fmt.Sprintf("data: %s\n\n", `{"type":"response.created","response":{"id":"resp_contract","model":"gpt-4.1-mini"}}`),
					fmt.Sprintf("data: %s\n\n", `{"type":"response.output_text.delta","delta":"hello response stream"}`),
					fmt.Sprintf("data: %s\n\n", `{"type":"response.completed","response":{"id":"resp_contract","model":"gpt-4.1-mini","usage":{"input_tokens":10,"output_tokens":11,"total_tokens":21}}}`),
				})
				return
			}
			writeJSON(t, w, map[string]any{
				"id":    "resp_contract",
				"model": "gpt-4.1-mini",
				"output": []map[string]any{{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{{
						"type": "output_text",
						"text": "hello response",
					}},
				}},
				"usage": map[string]any{
					"input_tokens":  8,
					"output_tokens": 6,
					"total_tokens":  14,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(openaiUpstream.Close)

	anthropicUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "sk-anthropic" {
			t.Fatalf("unexpected anthropic api key header: %s", got)
		}
		var payload map[string]any
		decodeJSON(t, mustReadAll(t, r), &payload)
		if r.URL.Path != "/messages" {
			http.NotFound(w, r)
			return
		}
		if payload["stream"] == true {
			writeSSE(t, w, []string{
				fmt.Sprintf("event: message_start\ndata: %s\n\n", `{"type":"message_start","message":{"id":"msg_contract","model":"claude-3-7-sonnet","role":"assistant","usage":{"input_tokens":13,"output_tokens":0}}}`),
				fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello anthropic stream"}}`),
				fmt.Sprintf("event: message_delta\ndata: %s\n\n", `{"type":"message_delta","usage":{"output_tokens":9}}`),
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			})
			return
		}
		writeJSON(t, w, map[string]any{
			"id":    "msg_contract",
			"model": "claude-3-7-sonnet",
			"role":  "assistant",
			"content": []map[string]any{{
				"type": "text",
				"text": "hello anthropic",
			}},
			"usage": map[string]any{
				"input_tokens":  9,
				"output_tokens": 7,
			},
		})
	}))
	t.Cleanup(anthropicUpstream.Close)

	memoryRepo := memorystore.NewInMemoryMemoryRepository()
	conversations := conversationstore.NewInMemoryConversationRepository()
	mediaRepo := mediastore.NewInMemoryMediaRepository()
	outbox := conversationstore.NewInMemoryOutboxRepository()
	settings := settingsservice.NewService(nil, nil, "", "", "", "", 2048)
	audit := auditsvc.NewService(nil)
	engine := memoryservice.NewEngine(memoryRepo, vector.NewPGVectorStore(nil))
	admin := adminservice.NewService(conversations, mediaRepo, memoryRepo, settings, audit, modelcap.NewRegistry(domain.ProviderOpenAICompatible, "", "", "", ""))
	session := security.NewAdminSessionManager(contractTestAdminToken, time.Hour)
	gateway := gatewayservice.NewService(
		provider.NewRegistry(
			domain.ProviderOpenAICompatible,
			openaiprovider.NewCompatibleAdapter(provider.StaticConfigResolver{
				domain.ProviderOpenAICompatible: {
					APIKey:  "sk-openai",
					BaseURL: openaiUpstream.URL,
				},
			}, 5*time.Second),
			openaiprovider.NewResponsesAdapter(provider.StaticConfigResolver{
				domain.ProviderOpenAIResponses: {
					APIKey:  "sk-openai",
					BaseURL: openaiUpstream.URL,
				},
			}, 5*time.Second),
			anthropicprovider.NewAdapter(provider.StaticConfigResolver{
				domain.ProviderAnthropic: {
					APIKey:  "sk-anthropic",
					BaseURL: anthropicUpstream.URL,
				},
			}, 5*time.Second),
		),
		engine,
		conversationservice.NewService(conversations, outbox, 100, 100000),
		settings,
		domain.ProviderOpenAICompatible,
	)

	return httptransport.NewRouter(httptransport.Dependencies{
		Config:      bootstrap.Config{Version: "0.1.0", SessionCookieSecure: false},
		Health:      observability.NewRegistry(),
		Auth:        security.Authenticator{AdminToken: contractTestAdminToken, LookupPrincipal: authfixture.LookupPrincipal, SessionManager: session},
		RateLimiter: security.NewRateLimiter(1000, 1000),
		Session:     session,
		Models:      modelcap.NewRegistry(domain.ProviderOpenAICompatible, "", "", "", ""),
		Gateway:     gateway,
		Memory:      memoryservice.NewService(engine, memoryRepo),
		Admin:       admin,
		Audit:       audit,
		Settings:    settings,
	})
}

func performJSONRequest(t *testing.T, router http.Handler, method, path string, payload any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+authfixture.Token)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeJSON(t *testing.T, raw []byte, value any) {
	t.Helper()
	if err := json.Unmarshal(raw, value); err != nil {
		t.Fatalf("unmarshal json: %v body=%s", err, string(raw))
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}

func writeSSE(t *testing.T, w http.ResponseWriter, events []string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	for _, event := range events {
		if _, err := w.Write([]byte(event)); err != nil {
			t.Fatalf("write sse: %v", err)
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func mustReadAll(t *testing.T, r *http.Request) []byte {
	t.Helper()
	raw := new(bytes.Buffer)
	if _, err := raw.ReadFrom(r.Body); err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return raw.Bytes()
}
