package aibroker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"memstation/apps/server/internal/aicall"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/memoryllm"
)

type callLedger struct {
	mu         sync.Mutex
	calls      map[string]domain.AICall
	startError error
}

func (l *callLedger) StartAICall(_ context.Context, c domain.AICall) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.startError != nil {
		return l.startError
	}
	if l.calls == nil {
		l.calls = map[string]domain.AICall{}
	}
	l.calls[c.ID] = c
	return nil
}

func (l *callLedger) FinishAICall(_ context.Context, c domain.AICall) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls[c.ID] = c
	return nil
}

func (l *callLedger) AICalls(context.Context, string, int) ([]domain.AICall, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []domain.AICall{}
	for _, c := range l.calls {
		out = append(out, c)
	}
	return out, nil
}

func (*callLedger) EmbeddingIdentity(_ context.Context, _, _ string, _ int, c domain.EmbeddingIdentity) (domain.EmbeddingIdentity, error) {
	return c, nil
}

func brokerConfig(base string) Config {
	return Config{BaseURL: base + "/v1", Credential: "restricted-service-test", Timeout: time.Second, Roles: map[string]string{"summary": "memory.summary", "analysis": "memory.analysis", "embedding": "memory.embedding", "rerank": "memory.rerank"}, Embedding: domain.EmbeddingIdentity{ModelVersion: "fixture-embedding-revision", Dimensions: 3, Fingerprint: "fixture-fingerprint"}}
}

func brokerContext(t *testing.T) context.Context {
	t.Helper()
	ctx, err := security.WithDataScope(context.Background(), "owner", "group")
	if err != nil {
		t.Fatal(err)
	}
	return aicall.WithJob(ctx, "job-id", 3)
}

func usageHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-MemStation-Broker-Version", "1")
	w.Header().Set("X-MemStation-Call-Id", r.Header.Get("X-MemStation-Call-Id"))
	w.Header().Set("X-MemStation-Request-Id", "request-"+r.Header.Get("X-MemStation-Call-Id"))
	w.Header().Set("X-MemStation-Usage-Id", "usage-"+r.Header.Get("X-MemStation-Call-Id"))
	w.Header().Set("X-MemStation-Reported-Cost", "0.001")
	w.Header().Set("X-MemStation-Currency", "USD")
}

func TestBrokerBudgetRetryHasSeparateAuthoritativeUsage(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer restricted-service-test" || r.Header.Get("X-MemStation-Role") != "summary" || r.Header.Get("X-MemStation-Workspace") != "group" || r.Header.Get("X-MemStation-Job") != "job-id" || r.Header.Get("X-MemStation-Attempt") != "3" || r.Header.Get("X-MemStation-Internal-Call") != "memory-broker-v1" {
			t.Error("broker scope or role lost")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != "memory.summary" {
			t.Error("logical alias lost")
		}
		usageHeaders(w, r)
		w.Header().Set("X-MemStation-Model-Version", "summary-fixture")
		finish := "length"
		content := ""
		if attempts == 2 {
			finish = "stop"
			content = `{"summary":"validated fixture"}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "summary-fixture", "choices": []any{map[string]any{"finish_reason": finish, "message": map[string]any{"role": "assistant", "content": content}}}, "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 30, "total_tokens": 50}})
	}))
	defer server.Close()
	ledger := &callLedger{}
	broker, err := New(brokerConfig(server.URL), ledger)
	if err != nil {
		t.Fatal(err)
	}
	client := memoryllm.NewClient(nil, nil, time.Second)
	client.SetRuntime(broker)
	raw, err := client.Generate(brokerContext(t), memoryllm.Request{UserID: "owner", Role: "summary_model", Task: "summary", Input: map[string]string{"source": "fixture"}, MaxOutputTokens: 4096})
	if err != nil || string(raw) != `{"summary":"validated fixture"}` {
		t.Fatalf("generation %s %v", raw, err)
	}
	calls, _ := ledger.AICalls(context.Background(), "", 100)
	if attempts != 2 || len(calls) != 2 {
		t.Fatalf("attempt accounting %d %+v", attempts, calls)
	}
	for _, c := range calls {
		if c.Status != "complete" || c.BrokerUsageID == "" || c.UsageAuthority != "broker" || c.ReportedCost != "0.001" || c.InputTokens != 20 {
			t.Fatalf("usage lost %+v", c)
		}
	}
	if _, _, err = broker.Adapter("gateway"); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("gateway role allowed")
	}
}

func TestBrokerEmbeddingFingerprintAndRerankContract(t *testing.T) {
	var mismatch atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usageHeaders(w, r)
		switch r.URL.Path {
		case "/v1/embeddings":
			if r.Header.Get("X-MemStation-Embedding-Fingerprint") != "fixture-fingerprint" {
				t.Error("missing index identity")
			}
			fingerprint := "fixture-fingerprint"
			if mismatch.Load() {
				fingerprint = "changed-model-same-alias"
			}
			w.Header().Set("X-MemStation-Embedding-Fingerprint", fingerprint)
			w.Header().Set("X-MemStation-Model-Version", "fixture-embedding-revision")
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "fixture-embedding-revision", "data": []any{map[string]any{"index": 0, "embedding": []float32{1, 0, 0}}}, "usage": map[string]int{"prompt_tokens": 2}})
		case "/v1/memory/rerank/v1":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": 1, "results": []any{map[string]any{"document_id": "invented-id", "score": 0.9}}})
		default:
			t.Error("unexpected broker route")
		}
	}))
	defer server.Close()
	broker, err := New(brokerConfig(server.URL), &callLedger{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := brokerContext(t)
	if _, err = broker.Embed(ctx, domain.EmbeddingInput{UserID: "owner", Text: "fixture", GenerationID: "generation"}); err != nil {
		t.Fatal(err)
	}
	mismatch.Store(true)
	if _, err = broker.Embed(ctx, domain.EmbeddingInput{UserID: "owner", Text: "fixture", GenerationID: "generation"}); err == nil {
		t.Fatal("changed embedding model mixed into existing index")
	}
	rerankCtx, calls := aicall.WithTrace(ctx)
	if _, err = broker.Rerank(rerankCtx, domain.RerankInput{UserID: "owner", Query: "fixture", Documents: []domain.RerankDocument{{ID: "real-id", Text: "fixture"}}}); err == nil {
		t.Fatal("invented rerank identity accepted")
	}
	if calls.FailureCode() != "broker_rerank_failed" || len(calls.Calls()) != 1 || calls.Calls()[0].Status != "complete" {
		t.Fatalf("invalid inference must be degraded while retaining actual usage: %+v %q", calls.Calls(), calls.FailureCode())
	}
}

func TestBrokerNetworkUnknownAndRedirectDoNotRetry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	defer server.Close()
	ledger := &callLedger{}
	broker, err := New(brokerConfig(server.URL), ledger)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = broker.post(brokerContext(t), "owner", "summary", "/chat/completions", map[string]any{}, nil)
	calls, _ := ledger.AICalls(context.Background(), "", 100)
	if err == nil || requests.Load() != 1 || len(calls) != 1 || calls[0].Status != "unknown" {
		t.Fatalf("unknown result incorrectly retried %+v %v", calls, err)
	}
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetRequests.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	broker, err = New(brokerConfig(redirect.URL), &callLedger{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = broker.post(brokerContext(t), "owner", "summary", "/chat/completions", map[string]any{}, nil); err == nil || targetRequests.Load() != 0 {
		t.Fatal("broker credential followed redirect")
	}
}

func (l *callLedger) AICall(ctx context.Context, id string) (domain.AICall, error) {
	calls, _ := l.AICalls(ctx, "", 100)
	for _, c := range calls {
		if c.ID == id {
			return c, nil
		}
	}
	return domain.AICall{}, domain.ErrAccessNotFound
}
