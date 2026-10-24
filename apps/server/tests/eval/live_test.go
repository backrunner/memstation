package eval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLiveReaderAnswersDistinguishAbstentionAndMalformedOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw      string
		expected any
		valid    bool
	}{
		{`{"answer":null}`, nil, true},
		{`{"answer":false}`, false, true},
		{`{"answer":"SQLite"}`, "sqlite", true},
		{`{"answer":0}`, false, false},
		{`{"answer":"false"}`, false, false},
		{`{}`, nil, false},
		{`not JSON`, nil, false},
	}
	for _, test := range tests {
		actual, err := parseLiveAnswer(test.raw)
		correct := err == nil && equalLiveAnswer(actual, test.expected)
		if correct != test.valid {
			t.Errorf("%s: got correct=%v, parse error=%v", test.raw, correct, err)
		}
	}
}

func TestLiveEvaluationUsesFlatConversationContractAndRevokesCredential(t *testing.T) {
	t.Parallel()
	var revoked atomic.Bool
	var derivedSeen atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/admin/login":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == "DELETE" && strings.HasSuffix(r.URL.Path, "/api-keys/credential"):
			revoked.Store(true)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/api-keys"):
			_, _ = w.Write([]byte(`{"api_key":"fixture-secret-not-for-reports","record":{"id":"credential"}}`))
		case strings.HasPrefix(r.URL.Path, "/api/admin/conversations/") && strings.HasSuffix(r.URL.Path, "/memory"):
			_, _ = w.Write([]byte(`{"documents":[]}`))
		case strings.HasPrefix(r.URL.Path, "/api/admin/conversations/"):
			_, _ = w.Write([]byte(`{"conversation_id":"conversation","summary_status":"completed","analysis_status":"completed"}`))
		case r.URL.Path == "/api/v1/memory/search":
			_, _ = w.Write([]byte(`{"memories":[{"user_id":"eval-contract-owner","type":"fact","content":"Preferred drink is tea","metadata":{"source_quote":"RAW_SOURCE_MUST_NOT_REACH_DERIVED_READER"}}],"total":1}`))
		case r.URL.Path == "/v1/chat/completions":
			var body struct {
				Messages []LiveMessage `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			w.Header().Set("X-MemStation-Conversation-ID", "conversation")
			content := `{"answer":null}`
			if r.Header.Get("X-MemStation-Mode") == "inject" {
				content = `{"answer":"tea"}`
			}
			if len(body.Messages) == 2 && body.Messages[0].Role == "system" {
				if strings.Contains(body.Messages[0].Content, "RAW_SOURCE_MUST_NOT_REACH") {
					t.Error("derived-only ablation included source evidence")
				}
				derivedSeen.Store(true)
				content = `{"answer":"tea"}`
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	dataset := LiveDataset{Name: "protocol-contract", Cases: []LiveCase{{ID: "owner", Conversations: [][]LiveMessage{{{Role: "user", Content: "synthetic tea preference"}}}, Questions: []LiveQuestion{{ID: "drink", Query: "Preferred drink?", Answer: "tea"}}}}}
	report, err := RunLiveEvaluation(context.Background(), dataset, LiveOptions{BaseURL: server.URL, AdminToken: "fixture", Model: "fixture", Prefix: "eval-contract", OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if report.Coverage != 1 || report.Correct != 1 || report.DerivedCorrect != 1 || !derivedSeen.Load() || report.BaselineCorrect != 0 || !revoked.Load() {
		t.Fatalf("incorrect contract result or credential lifecycle: %+v revoked=%v", report, revoked.Load())
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "fixture-secret-not-for-reports") {
		t.Fatal("report contains credential")
	}
}

func TestEvaluationDatabaseRejectsProductionAndConnectionOverrides(t *testing.T) {
	t.Parallel()
	for _, dsn := range []string{"postgres://127.0.0.1/memstation", "postgres://remote.example/memstation_eval_test", "postgres://127.0.0.1/memstation_eval_test?dbname=production", "postgres://127.0.0.1/memstation_quality_test?host=remote.example"} {
		if db, err := OpenEvaluationDatabase(context.Background(), dsn); err == nil {
			if db != nil {
				_ = db.Close()
			}
			t.Errorf("unsafe evaluation database accepted: %s", dsn)
		}
	}
}

func TestLiveEvaluationRejectsUnsafeScopeBeforeRequests(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	dataset := LiveDataset{Cases: []LiveCase{{ID: "case", Conversations: [][]LiveMessage{{{Role: "user", Content: "synthetic"}}}, Questions: []LiveQuestion{{ID: "q", Query: "question", Answer: nil}}}}}
	for _, options := range []LiveOptions{
		{BaseURL: server.URL, AdminToken: "fixture", Model: "fixture", Prefix: "existing-owner", OutputDir: t.TempDir()},
		{BaseURL: "https://example.com", AdminToken: "fixture", Model: "fixture", Prefix: "eval-safe", OutputDir: t.TempDir()},
	} {
		if _, err := RunLiveEvaluation(context.Background(), dataset, options); err == nil {
			t.Fatal("unsafe live scope was accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("unsafe scope made %d requests", calls.Load())
	}
}
