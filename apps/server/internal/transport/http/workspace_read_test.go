package httptransport

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/admin"
	"memstation/apps/server/internal/service/apikey"
	"memstation/apps/server/internal/service/memory"
	"memstation/apps/server/internal/service/memorygen"
)

func TestWorkspaceReadContract(t *testing.T) {
	ctx := context.Background()
	keys := apikey.NewService(nil)
	conversations := conversationstore.NewInMemoryConversationRepository()
	memories := memorystore.NewInMemoryMemoryRepository()
	for _, user := range []string{"alice", "bob"} {
		if _, err := keys.Identities().Personal(ctx, user); err != nil {
			t.Fatal(err)
		}
		if err := conversations.PersistExchange(ctx, domain.ConversationRecord{UserID: user, ConversationID: user + "-thread", CreatedAt: time.Now(), UpdatedAt: time.Now(), AgentID: "fixture-agent"}); err != nil {
			t.Fatal(err)
		}
		if err := memories.StoreDocument(ctx, domain.MemoryDocument{ID: user + "-fact", NodeID: user + "-node", UserID: user, Type: domain.MemoryTypeFact, Content: user + " private content", ConversationID: user + "-thread", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	sessions := security.NewAdminSessionManager("scope-test-master", time.Hour)
	router := NewRouter(Dependencies{APIKeys: keys, Admin: admin.NewService(conversations, nil, memories, nil, nil, nil), Memory: memory.NewService(memory.NewEngine(memories, nil), memories), Generations: memorygen.NewService(nil, "test"), Session: sessions, Auth: security.Authenticator{AdminToken: "test-admin", SessionManager: sessions}})
	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"token":"test-admin"}`)))
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	base := "/api/admin/workspaces/" + domain.PersonalWorkspaceID("alice")
	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{
		{base + "/conversations", 200, "alice-thread"},
		{base + "/conversations/page?q=alice&limit=1", 200, "alice-thread"},
		{base + "/conversations/page?q=bob", 200, `"items":[]`},
		{base + "/conversations/page?after=broken", 400, ""},
		{base + "/conversations/page?q=a&q=b", 400, ""},
		{base + "/conversations/page?generation_id=ignored", 400, ""},
		{base + "/conversations/page?kind=ignored", 400, ""},
		{base + "/conversations/page?user_id=bob", 404, ""},
		{base + "/memory/keys/page", 200, `"items":[]`},
		{base + "/memory/keys/page?after=broken", 400, ""},
		{base + "/memory/keys/page?limit=101", 400, ""},
		{base + "/memory/keys/page?generation_id=ignored", 400, ""},
		{base + "/memory/keys/page?user_id=bob", 404, ""},

		{base + "/conversations/alice-thread", 200, "alice-thread"},
		{base + "/conversations/alice-thread/memory", 200, "alice private content"},
		{base + "/conversations/alice-thread/sources", 200, "alice-thread"},
		{base + "/memory/documents", 200, "alice private content"},
		{base + "/memory/documents/page?q=private&limit=1", 200, "alice private content"},
		{base + "/memory/documents/page?q=missing", 200, `"items":[]`},
		{base + "/memory/documents/page?after=broken", 400, ""},
		{base + "/memory/documents/page?q=a&q=b", 400, ""},
		{base + "/memory/documents/page?kind=invalid", 400, ""},
		{base + "/memory/documents/page?user_id=bob", 404, ""},
		{base + "/memory/documents/fact/alice-fact", 200, "alice private content"},
		{base + "/memory/documents/fact/bob-fact", 404, ""},
		{base + "/conversations/bob-thread", 404, ""},
		{base + "/conversations/bob-thread/memory", 404, ""},
		{base + "/conversations/bob-thread/sources", 404, ""},
		{base + "/conversations?user_id=bob", 404, ""},
		{base + "/memory/documents?workspace_id=group", 404, ""},
		{base + "/conversations?user_id=alice&user_id=bob", 400, ""},
		{base + "/conversations?limit=101", 400, ""},
		{base + "/conversations?limit=-1", 400, ""},
		{base + "/memory/documents?actor_id=ignored", 400, ""},
		{"/api/admin/workspaces/" + domain.PersonalWorkspaceID("missing") + "/conversations", 404, ""},
		{"/api/admin/workspaces/group/memory/documents", 404, ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			for _, cookie := range login.Result().Cookies() {
				req.AddCookie(cookie)
			}
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != tc.status || !strings.Contains(res.Body.String(), tc.contains) || strings.Contains(res.Body.String(), "bob private content") {
				t.Fatalf("%d %s", res.Code, res.Body.String())
			}
			if res.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("cacheable workspace response")
			}
		})
	}
	for _, method := range []string{"GET", "PUT"} {
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequest(method, base+"/memory/documents/fact/alice-fact", nil))
		expected := 401
		if res.Code != expected {
			t.Fatalf("anonymous %s: %d", method, res.Code)
		}
	}
	for _, tc := range []struct {
		body     string
		status   int
		snapshot bool
	}{
		{`{"query":"private","top_n":10}`, 200, false},
		{`{"query":"private","top_n":10,"as_of":"2099-01-01T00:00:00Z"}`, 200, true},
		{`{"query":"private","top_n":10,"known_at":"2099-01-01T00:00:00Z","memory_types":["fact"]}`, 200, true},
		{`{"query":"private","top_n":10,"user_id":"bob"}`, 400, false},
		{`{"query":"private","top_n":10,"workspace_id":"group"}`, 400, false},
		{`{"query":"private","top_n":10,"generation_id":"foreign"}`, 400, false},
		{`{"query":"private","top_n":101}`, 400, false},
		{`{"query":"private","top_n":0}`, 400, false},
		{`{"query":"private","top_n":10,"as_of":"yesterday"}`, 400, false},
		{`{"query":"private","top_n":10,"as_of":"2099-01-01T00:00:00Z","memory_types":["summary"]}`, 400, false},
		{`{"query":"private","top_n":10,"date_from":"2026-12-01T00:00:00Z","date_to":"2026-01-01T00:00:00Z"}`, 400, false},
		{`{"query":"private","top_n":10} {}`, 400, false},
	} {
		req := httptest.NewRequest("POST", base+"/memory/search", strings.NewReader(tc.body))
		for _, cookie := range login.Result().Cookies() {
			req.AddCookie(cookie)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != tc.status || strings.Contains(res.Body.String(), "bob private content") {
			t.Fatalf("search %s: %d %s", tc.body, res.Code, res.Body.String())
		}
		if tc.status == 200 {
			var result domain.SearchResult
			if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || len(result.Memories) != 1 || result.Memories[0].Content != "alice private content" {
				t.Fatal(res.Body.String(), err)
			}
			if tc.snapshot && result.Memories[0].Metadata["historical_snapshot"] != true {
				t.Fatal("unmarked historical snapshot")
			}
		}
	}

	page, err := keys.Identities().Workspaces(ctx, domain.WorkspaceQuery{})
	if err != nil || len(page.Items) != 2 {
		t.Fatal("read provisioned a missing workspace", err)
	}
}
