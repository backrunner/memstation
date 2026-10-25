package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/apikey"
)

func TestWorkspaceAdminContractAndProxyDiscovery(t *testing.T) {
	ctx := context.Background()
	keys := apikey.NewService(nil)
	w, err := keys.Identities().Personal(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := keys.CreateWithOptions(ctx, "owner", apikey.CreateOptions{Name: "read", Capabilities: []string{domain.CapabilityMemoryRead}})
	if err != nil {
		t.Fatal(err)
	}
	sessions := security.NewAdminSessionManager("workspace-test-master", time.Hour)
	router := NewRouter(Dependencies{APIKeys: keys, Session: sessions, Auth: security.Authenticator{AdminToken: "workspace-test-admin", SessionManager: sessions, LookupPrincipal: keys.LookupPrincipalByHash}})
	var cookies []*http.Cookie
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			if token == "workspace-test-admin" {
				for _, cookie := range cookies {
					req.AddCookie(cookie)
				}
			} else {
				req.Header.Set("Authorization", "Bearer "+token)
			}
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	login := call("POST", "/api/admin/login", "", `{"token":"workspace-test-admin"}`)
	if login.Code != 200 {
		t.Fatal("admin login", login.Body.String())
	}
	cookies = login.Result().Cookies()
	path := "/api/admin/workspaces/" + w.ID
	for _, uri := range []string{"/api/admin/workspaces", path} {
		if res := call("GET", uri, raw, ""); res.Code != 401 {
			t.Fatalf("proxy key got admin access: %d", res.Code)
		}
	}
	body := `{"user_id":"owner","name":"My memory","capabilities":[],"expected_policy_version":1}`
	for _, tc := range []struct {
		method, uri, body string
		code              int
	}{
		{"GET", "/api/admin/workspaces?limit=oops", "", 400},
		{"GET", "/api/admin/workspaces?limit=101", "", 400},
		{"GET", path + "?user_id=other", "", 404},
		{"PUT", path, strings.Replace(body, `"owner"`, `"other"`, 1), 404},
		{"PUT", path, strings.Replace(body, `"capabilities":[]`, `"capabilities":null`, 1), 400},
		{"PUT", path, strings.Replace(body, `"capabilities":[]`, `"capabilities":[],"status":"archived"`, 1), 400},
		{"PUT", path, body, 200},
		{"PUT", path, body, 409},
		{"GET", "/api/admin/workspaces?q=MY%20MEMORY&user_id=owner", "", 200},
	} {
		res := call(tc.method, tc.uri, "workspace-test-admin", tc.body)
		if res.Code != tc.code {
			t.Fatalf("%s %s: %d %s", tc.method, tc.uri, res.Code, res.Body.String())
		}
		if res.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable scope response")
		}
	}
	res := call("GET", "/api/v1/workspaces", raw, "")
	var page domain.WorkspacePage
	if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if res.Code != 200 || len(page.Items) != 1 || page.Items[0].Name != "My memory" || len(page.Items[0].Capabilities) != 0 || page.Items[0].PolicyVersion != 2 {
		t.Fatal("proxy discovery stale or widened", res.Body.String())
	}
	if res = call("GET", "/api/v1/memory/keys", raw, ""); res.Code != 403 {
		t.Fatal("policy removal did not protect memory read")
	}
}
