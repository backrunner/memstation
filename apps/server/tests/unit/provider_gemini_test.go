package unit_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
	geminiprovider "memstation/apps/server/internal/provider/gemini"
)

func TestGeminiAdapterComplete(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/models/gemini-2.5-flash:generateContent" {
			t.Fatalf("unexpected path: %s", got)
		}
		if got := r.URL.Query().Get("key"); got != "sk-gemini" {
			t.Fatalf("unexpected api key query: %s", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if _, ok := payload["contents"]; !ok {
			t.Fatalf("expected contents payload, got %+v", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"role": "model",
					"parts": []map[string]any{{
						"text": "hello gemini",
					}},
				},
			}},
			"usageMetadata": map[string]any{
				"promptTokenCount":     10,
				"candidatesTokenCount": 8,
				"totalTokenCount":      18,
			},
			"modelVersion": "gemini-2.5-flash",
		})
	}))
	defer server.Close()

	adapter := geminiprovider.NewAdapter(provider.StaticConfigResolver{
		domain.ProviderGemini: {
			APIKey:  "sk-gemini",
			BaseURL: server.URL,
		},
	}, 5*time.Second)

	response, err := adapter.Complete(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolOpenAIResponses,
		Model:    "gemini-2.5-flash",
		Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "hello"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if response.Provider != domain.ProviderGemini {
		t.Fatalf("unexpected provider: %s", response.Provider)
	}
	if response.Message.Text != "hello gemini" {
		t.Fatalf("unexpected text: %s", response.Message.Text)
	}
	if response.Usage.TotalTokens != 18 {
		t.Fatalf("unexpected usage: %+v", response.Usage)
	}
}

func TestGeminiAdapterCompleteParsesToolCalls(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		tools, ok := payload["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("expected tools payload, got %+v", payload["tools"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"role": "model",
					"parts": []map[string]any{{
						"functionCall": map[string]any{
							"id":   "call-weather-1",
							"name": "get_weather",
							"args": map[string]any{"city": "London"},
						},
					}},
				},
			}},
			"usageMetadata": map[string]any{
				"promptTokenCount":     12,
				"candidatesTokenCount": 4,
				"totalTokenCount":      16,
			},
			"modelVersion": "gemini-2.5-flash",
		})
	}))
	defer server.Close()

	adapter := geminiprovider.NewAdapter(provider.StaticConfigResolver{
		domain.ProviderGemini: {
			APIKey:  "sk-gemini",
			BaseURL: server.URL,
		},
	}, 5*time.Second)

	response, err := adapter.Complete(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolOpenAIResponses,
		Model:    "gemini-2.5-flash",
		Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "what's the weather?"}},
		Tools: []domain.ToolDefinition{{
			Name:        "get_weather",
			Description: "Get weather for a city",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
				"required": []string{"city"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected tool call, got %+v", response.Message.ToolCalls)
	}
	if response.Message.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("unexpected tool call: %+v", response.Message.ToolCalls[0])
	}
	if response.Message.ToolCalls[0].Arguments["city"] != "London" {
		t.Fatalf("unexpected tool arguments: %+v", response.Message.ToolCalls[0].Arguments)
	}
}

func TestGeminiAdapterCompleteSendsFunctionResponses(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		contents, ok := payload["contents"].([]any)
		if !ok || len(contents) != 3 {
			t.Fatalf("expected three content turns, got %+v", payload["contents"])
		}
		modelTurn, ok := contents[1].(map[string]any)
		if !ok {
			t.Fatalf("unexpected model turn: %+v", contents[1])
		}
		if modelTurn["role"] != "model" {
			t.Fatalf("expected model role, got %+v", modelTurn)
		}
		toolTurn, ok := contents[2].(map[string]any)
		if !ok {
			t.Fatalf("unexpected tool turn: %+v", contents[2])
		}
		parts, ok := toolTurn["parts"].([]any)
		if !ok || len(parts) != 1 {
			t.Fatalf("expected function response part, got %+v", toolTurn["parts"])
		}
		part, ok := parts[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected part: %+v", parts[0])
		}
		functionResponse, ok := part["functionResponse"].(map[string]any)
		if !ok {
			t.Fatalf("expected functionResponse, got %+v", part)
		}
		if functionResponse["name"] != "get_weather" {
			t.Fatalf("unexpected function response name: %+v", functionResponse)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"role": "model",
					"parts": []map[string]any{{
						"text": "It is sunny in London.",
					}},
				},
			}},
			"usageMetadata": map[string]any{
				"promptTokenCount":     14,
				"candidatesTokenCount": 6,
				"totalTokenCount":      20,
			},
			"modelVersion": "gemini-2.5-flash",
		})
	}))
	defer server.Close()

	adapter := geminiprovider.NewAdapter(provider.StaticConfigResolver{
		domain.ProviderGemini: {
			APIKey:  "sk-gemini",
			BaseURL: server.URL,
		},
	}, 5*time.Second)

	response, err := adapter.Complete(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolOpenAIResponses,
		Model:    "gemini-2.5-flash",
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "weather in London"},
			{
				Role: domain.RoleAssistant,
				ToolCalls: []domain.ToolCall{{
					ID:        "call-weather-1",
					Name:      "get_weather",
					Arguments: map[string]any{"city": "London"},
				}},
			},
			{
				Role: domain.RoleTool,
				ToolResults: []domain.ToolResult{{
					ToolCallID: "call-weather-1",
					Name:       "get_weather",
					Output: map[string]any{
						"ok":       true,
						"forecast": "sunny",
					},
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if response.Message.Text != "It is sunny in London." {
		t.Fatalf("unexpected text: %s", response.Message.Text)
	}
}
