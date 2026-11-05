package processing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
)

func TestGenerativeEntityQueryUsesGroundedLocalSpansAndScopedCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/entities" {
			t.Error("query called source routing endpoint")
		}
		budget, err := strconv.Atoi(r.Header.Get("X-Memstation-Timeout-Ms"))
		if err != nil || budget <= 0 || budget > 200 {
			t.Error("missing or unbounded query deadline")
		}
		var request struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Texts) != 1 || request.Texts[0] != "MemStation 的迁移" {
			t.Errorf("unexpected source: %+v", request)
		}
		_ = json.NewEncoder(w).Encode(Response{Version: 1, Model: "encoder", Revision: "pinned", Splitter: "char", Observations: []Observation{{Supported: true, Spans: []Span{{Start: 0, End: 10, Text: "MemStation", Label: "project", Confidence: .9}, {Start: 0, End: 10, Text: "MemStation", Label: "project", Confidence: .9}}}}})
	}))
	defer server.Close()
	config := Config{Strategy: "generative", EntityRecallTimeoutMS: 200, Endpoint: server.URL, Token: "fixture", Model: "encoder", Revision: "pinned", Splitter: "char", Threshold: .75, MaxSemanticBytes: 4096}
	ledger := &queryLedger{}
	service, err := New(config, ledger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := security.WithDataScope(context.Background(), "owner", "workspace", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result, err := service.ExtractQueryEntities(ctx, "owner", "MemStation 的迁移")
		if err != nil || len(result.Names) != 1 || result.Names[0] != "memstation" || result.CacheHit != (i == 1) {
			t.Fatalf("cache/extraction: %+v %v", result, err)
		}
	}
	other, err := security.WithDataScope(context.Background(), "owner", "workspace", "private")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ExtractQueryEntities(other, "owner", "MemStation 的迁移"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("cache crossed audience or repeated inference", calls.Load())
	}
	if _, err = service.ExtractQueryEntities(ctx, "owner", strings.Repeat("中", 513)); err != domain.ErrEntityQueryBudget {
		t.Fatal("oversized query silently truncated", err)
	}
	if calls.Load() != 2 {
		t.Fatal("query budget called encoder")
	}
	if len(ledger.calls) != 2 {
		t.Fatal("cache inflated model call ledger", len(ledger.calls))
	}
	for _, call := range ledger.calls {
		if call.Stage != "entity.query.extract" || call.Strategy != "generative" || call.UsageAuthority != "local" || call.ModelVersion != "pinned" || call.ReportedCost != "" || call.UsageKnown {
			t.Fatalf("incorrect query accounting: %+v", call)
		}
	}
	config.Token = ""
	if _, err = New(config, nil); err == nil {
		t.Fatal("generative entity feature accepted missing encoder credential")
	}
}

// Cache hits are not inference calls and must not inflate the usage ledger.
type queryLedger struct {
	ports.AICallRepository
	calls []domain.AICall
}

func TestEntityQueryRespectsShorterCallerDeadline(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		budget, err := strconv.Atoi(r.Header.Get("X-Memstation-Timeout-Ms"))
		if err != nil || budget < 1 || budget > 80 {
			t.Error("configured timeout replaced the shorter caller deadline")
		}
		_ = json.NewEncoder(w).Encode(Response{Version: 1, Model: "encoder", Revision: "pinned", Splitter: "char", Observations: []Observation{{Supported: true}}})
	}))
	defer server.Close()
	service, err := New(Config{Strategy: "generative", EntityRecallTimeoutMS: 200, Endpoint: server.URL, Token: "fixture", Model: "encoder", Revision: "pinned", Splitter: "char", Threshold: .75, MaxSemanticBytes: 4096}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if _, err := service.ExtractQueryEntities(ctx, "owner", "a synthetic query"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := service.ExtractQueryEntities(ctx, "owner", "another query"); err == nil || calls.Load() != 1 {
		t.Fatal("cancellation triggered another inference", err, calls.Load())
	}
}

func (l *queryLedger) StartAICall(_ context.Context, call domain.AICall) error {
	l.calls = append(l.calls, call)
	return nil
}

func (l *queryLedger) FinishAICall(_ context.Context, call domain.AICall) error {
	for i := range l.calls {
		if l.calls[i].ID == call.ID {
			l.calls[i] = call
			return nil
		}
	}
	panic("finishing unstarted fixture call")
}
