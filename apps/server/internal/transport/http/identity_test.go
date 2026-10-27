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

func TestProxyIdentityCapabilitiesAndWorkspace(t *testing.T) {
	ctx := context.Background()
	keys := apikey.NewService(nil)
	agent, err := keys.Identities().Create(ctx, "owner", "Reader", []string{domain.CapabilityMemoryRead})
	if err != nil {
		t.Fatal(err)
	}
	key, raw, err := keys.CreateWithOptions(ctx, "owner", apikey.CreateOptions{WorkspaceID: domain.PersonalWorkspaceID("owner"), Name: "Reader key", AgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	auth := security.Authenticator{LookupPrincipal: keys.LookupPrincipalByHash}
	hit := false
	handler := proxyAuthMiddleware(auth, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true; identityHandler(w, r) }))
	for _, tc := range []struct {
		method, path, workspace string
		status                  int
	}{
		{"GET", "/api/v1/identity", "", 200},
		{"GET", "/api/v1/workspaces", agent.DefaultWorkspaceID, 200},
		{"POST", "/api/v1/memory/search", "", 200},
		{"PUT", "/api/v1/memory/keys/key", "", 403},
		{"DELETE", "/api/v1/memory/keys/key", "", 403},
		{"POST", "/v1/chat/completions", "", 403},
		{"POST", "/v1/responses", "", 403},
		{"POST", "/v1/messages", "", 403},
		{"GET", "/v1/files/blob", "", 403},
		{"POST", "/v1/files", "", 403},
		{"GET", "/api/v1/identity", "other-workspace", 403},
	} {
		request := httptest.NewRequest(tc.method, tc.path, nil)
		request.Header.Set("Authorization", "Bearer "+raw)
		request.Header.Set("X-MemStation-Workspace", tc.workspace)
		response := httptest.NewRecorder()
		hit = false
		handler.ServeHTTP(response, request)
		if response.Code != tc.status || hit != (tc.status == 200) {
			t.Fatalf("%s %s: %d hit=%v", tc.method, tc.path, response.Code, hit)
		}
		if strings.Contains(response.Body.String(), raw) {
			t.Fatal("raw credential in response")
		}
	}
	if err = keys.RevokeForUser(ctx, "owner", key.ID); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/v1/identity", nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatalf("revoked credential: %d", response.Code)
	}
}

func TestAdminAgentCredentialFlow(t *testing.T) {
	keys := apikey.NewService(nil)
	sessions := security.NewAdminSessionManager("identity-test-master", time.Hour)
	router := NewRouter(Dependencies{Auth: security.Authenticator{AdminToken: "identity-test-admin", SessionManager: sessions, LookupPrincipal: keys.LookupPrincipalByHash}, Session: sessions, APIKeys: keys})
	var cookies []*http.Cookie
	call := func(method, path, body string, admin bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if admin {
			for _, cookie := range cookies {
				req.AddCookie(cookie)
			}
		}
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}
	login := call("POST", "/api/admin/login", `{"token":"identity-test-admin"}`, false)
	if login.Code != 200 {
		t.Fatalf("admin login: %d", login.Code)
	}
	cookies = login.Result().Cookies()
	if response := call("POST", "/api/admin/users/owner/agents", `{"name":"Agent"}`, false); response.Code != 401 {
		t.Fatal("unauthenticated agent creation")
	}
	response := call("POST", "/api/admin/users/owner/agents", `{"name":"Reader","capabilities":["memory.read"]}`, true)
	if response.Code != 201 {
		t.Fatalf("create agent: %d %s", response.Code, response.Body.String())
	}
	var agent domain.Agent
	if err := json.Unmarshal(response.Body.Bytes(), &agent); err != nil {
		t.Fatal(err)
	}
	if response = call("PUT", "/api/admin/users/owner/agents/"+agent.ID, `{"name":"Reader","status":"disabled","expected_revision":99}`, true); response.Code != 409 {
		t.Fatal("stale admin editor did not conflict")
	}
	response = call("POST", "/api/admin/users/owner/api-keys", `{"name":"Reader key","workspace_id":"`+agent.DefaultWorkspaceID+`","agent_id":"`+agent.ID+`","capabilities":["memory.read"]}`, true)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("key issuance: %d", response.Code)
	}
	var issued struct {
		Record domain.Credential `json:"record"`
		Raw    string            `json:"api_key"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if issued.Raw == "" || issued.Record.AgentID != agent.ID {
		t.Fatal("wrong key binding")
	}
	if response = call("DELETE", "/api/admin/users/someone-else/api-keys/"+issued.Record.ID, "", true); response.Code != 404 {
		t.Fatal("wrong owner revoke accepted")
	}

	for _, path := range []string{"/api/admin/users/owner/agents/" + agent.ID, "/api/admin/users/owner/agents/" + agent.ID + "/api-keys"} {
		if result := call("GET", path, "", false); result.Code != 401 {
			t.Fatal("unauthenticated management read")
		}
		result := call("GET", path, "", true)
		if result.Code != 200 || result.Header().Get("Cache-Control") != "no-store" || strings.Contains(result.Body.String(), issued.Raw) || strings.Contains(result.Body.String(), `"hash"`) {
			t.Fatalf("management read: %d %s", result.Code, result.Body.String())
		}
		if result = call("GET", strings.Replace(path, "/owner/", "/other/", 1), "", true); result.Code != 404 {
			t.Fatal("cross-owner management read")
		}
	}
	if result := call("GET", "/api/admin/users/owner/agents?q=READER&status=active&limit=1", "", true); result.Code != 200 || !strings.Contains(result.Body.String(), agent.ID) {
		t.Fatal("filtered list failed")
	}
	if result := call("GET", "/api/admin/users/owner/agents?status=oops", "", true); result.Code != 400 {
		t.Fatal("invalid status accepted")
	}
	req := httptest.NewRequest("GET", "/api/v1/identity", nil)
	req.Header.Set("Authorization", "Bearer "+issued.Raw)
	identity := httptest.NewRecorder()
	router.ServeHTTP(identity, req)
	if identity.Code != 200 || !strings.Contains(identity.Body.String(), agent.ID) || strings.Contains(identity.Body.String(), issued.Raw) {
		t.Fatal("authenticated identity response incorrect")
	}
	if response = call("PUT", "/api/admin/users/owner/agents/"+agent.ID, `{"name":"Reader","status":"disabled","expected_revision":1}`, true); response.Code != 200 {
		t.Fatal("disable failed")
	}
	denied := httptest.NewRecorder()
	router.ServeHTTP(denied, req)
	if denied.Code != 401 {
		t.Fatal("disabled agent retained API access")
	}
}
