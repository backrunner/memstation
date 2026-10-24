package httptransport

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/admin"
	"memstation/apps/server/internal/service/apikey"
	"memstation/apps/server/internal/service/memory"
	sqlstore "memstation/apps/server/internal/store/sql"
)

func TestWorkspaceMemoryEditContract(t *testing.T) {
	ctx := context.Background()
	keys := apikey.NewService(nil)
	repo := sqlstore.NewInMemoryMemoryRepository()
	for _, user := range []string{"alice", "bob"} {
		if _, err := keys.Identities().Personal(ctx, user); err != nil {
			t.Fatal(err)
		}
		if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: user + "-summary", UserID: user, Type: domain.MemoryTypeSummary, Content: user + " content"}); err != nil {
			t.Fatal(err)
		}
	}
	svc := memory.NewService(nil, repo)
	sessions := security.NewAdminSessionManager("scope-test-master", time.Hour)
	router := NewRouter(Dependencies{APIKeys: keys, Admin: admin.NewService(sqlstore.NewInMemoryConversationRepository(), nil, repo, nil, nil, nil), Memory: svc, Session: sessions, Auth: security.Authenticator{AdminToken: "test-admin", SessionManager: sessions}})
	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"token":"test-admin"}`)))
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		for _, c := range login.Result().Cookies() {
			req.AddCookie(c)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	base := "/api/admin/workspaces/" + domain.PersonalWorkspaceID("alice") + "/memory"
	doc, err := svc.GetDocument(ctx, "alice", "alice-summary", domain.MemoryTypeSummary)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"content": "edited", "generation_id": doc.GenerationID, "expected_updated_at": doc.UpdatedAt})
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"PUT", base + "/documents/summary/bob-summary", string(payload), 404},
		{"PUT", base + "/documents/summary/alice-summary?user_id=bob", string(payload), 404},
		{"PUT", base + "/documents/summary/alice-summary", `{"content":"unsafe"}`, 400},
		{"PUT", base + "/documents/summary/alice-summary", string(payload) + `{}`, 400},
		{"POST", base + "/keys", `{"key":"x","value":"v","review_status":"pending","user_id":"bob"}`, 400},
		{"POST", base + "/keys?generation_id=ignored", `{"key":"x","value":"v","review_status":"pending"}`, 400},
		{"POST", base + "/keys", `{"key":"x","value":"v","review_status":"unreviewed"}`, 400},
		{"PUT", base + "/documents/summary/alice-summary", string(payload), 200},
		{"PUT", base + "/documents/summary/alice-summary", string(payload), 409},
		{"POST", base + "/keys", `{"key":"x","value":"v","review_status":"pending"}`, 201},
		{"POST", base + "/keys", `{"key":"x","value":"overwrite","review_status":"pending"}`, 409},
	} {
		res := call(tc.method, tc.path, tc.body)
		if res.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, res.Code, res.Body.String())
		}
		if res.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable write response")
		}
	}
	km, ok, err := svc.GetKeyMemory(ctx, "alice", "x")
	if err != nil || !ok {
		t.Fatal(err)
	}
	if res := call("GET", strings.Replace(base, domain.PersonalWorkspaceID("alice"), domain.PersonalWorkspaceID("bob"), 1)+"/keys/"+km.ID, ""); res.Code != 404 {
		t.Fatal("cross-owner key", res.Code)
	}
	body, _ := json.Marshal(map[string]any{"expected_updated_at": km.UpdatedAt})
	if res := call("DELETE", base+"/keys/"+km.ID, string(body)); res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	if res := call("DELETE", base+"/keys/"+km.ID, string(body)); res.Code != 404 {
		t.Fatal("deleted key", res.Code)
	}
	if res := call("POST", base+"/keys", `{"key":"huge","value":"`+strings.Repeat("x", 256<<10)+`","review_status":"pending"}`); res.Code != 400 {
		t.Fatal("unbounded payload", res.Code)
	}
}
