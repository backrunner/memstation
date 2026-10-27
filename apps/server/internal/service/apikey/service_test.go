package apikey_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/apikey"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestAgentCredentialLifecycle(t *testing.T) {
	runIdentitySuite(t, nil)
	runAgentManagementSuite(t, nil)
}

func TestPostgresAgentCredentialLifecycle(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runIdentitySuite(t, db)
	runAgentManagementSuite(t, db)
}

func runIdentitySuite(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	user := "identity-test-" + uuid.NewString()
	keys := apikey.NewService(runtimeconfig.NewStaticDBHandle(db))
	identities := keys.Identities()
	if db != nil {
		defer func() {
			for _, table := range []string{"api_keys", "workspace_grants", "agents", "actors", "workspaces"} {
				if _, err := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id=$1", user); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	a, err := identities.Create(ctx, user, "Coding agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := identities.Create(ctx, user, "Review agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID || a.ActorID == b.ActorID || a.DefaultWorkspaceID != b.DefaultWorkspaceID {
		t.Fatal("agents do not have independent identities in one workspace")
	}
	caps := []string{domain.CapabilityMemoryRead}
	key, raw, err := keys.CreateWithOptions(ctx, user, apikey.CreateOptions{WorkspaceID: domain.PersonalWorkspaceID(user), Name: "read only", AgentID: a.ID, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	caps[0] = domain.CapabilityMemoryEdit
	auth := security.Authenticator{LookupPrincipal: keys.LookupPrincipalByHash}
	principal, err := auth.AuthenticatePrincipal(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if key.Hash != "" || principal.AgentID != a.ID || principal.CredentialID != key.ID || !principal.Allows(domain.CapabilityMemoryRead) || principal.Allows(domain.CapabilityMemoryEdit) {
		t.Fatal("wrong principal or credential ceiling")
	}
	principal.Capabilities[0] = domain.CapabilityMemoryEdit
	principal, err = auth.AuthenticatePrincipal(ctx, raw)
	if err != nil || principal.Allows(domain.CapabilityMemoryEdit) {
		t.Fatal("caller mutated stored capabilities")
	}
	if _, err = auth.AuthenticateContext(ctx, raw, "another-workspace"); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("workspace override accepted: %v", err)
	}
	if _, _, err = keys.CreateWithOptions(ctx, "other-owner", apikey.CreateOptions{WorkspaceID: domain.PersonalWorkspaceID(user), Name: "spoof", AgentID: a.ID}); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatalf("cross-owner issuance: %v", err)
	}
	if err = keys.RevokeForUser(ctx, "other-owner", key.ID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatalf("cross-owner revoke: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	if _, _, err = keys.CreateWithOptions(ctx, user, apikey.CreateOptions{WorkspaceID: domain.PersonalWorkspaceID(user), Name: "expired", AgentID: a.ID, ExpiresAt: &past}); err == nil {
		t.Fatal("past expiration accepted")
	}
	bound, err := auth.AuthenticateContext(ctx, raw, principal.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := identities.Grant(ctx, user, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	grant.Capabilities = []string{}
	// Concurrent editors must not overwrite one another.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := identities.SetGrant(ctx, grant); results <- err })
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, domain.ErrAccessConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS results: %d/%d", successes, conflicts)
	}
	if err = security.RequireCapability(bound, domain.CapabilityMemoryRead); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("tool continuation retained revoked grant")
	}
	if _, err = identities.Personal(ctx, user); err != nil {
		t.Fatal(err)
	}
	principal, err = auth.AuthenticatePrincipal(ctx, raw)
	if err != nil || len(principal.Capabilities) != 0 {
		t.Fatal("bootstrap restored revoked capabilities")
	}
	grant, err = identities.Grant(ctx, user, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	grant.Capabilities = domain.PersonalCapabilities()
	if _, err = identities.SetGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	principal, err = auth.AuthenticatePrincipal(ctx, raw)
	if err != nil || principal.Allows(domain.CapabilityMemoryEdit) {
		t.Fatal("grant expanded immutable credential ceiling")
	}
	a.Status = "disabled"
	disabled, err := identities.Update(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = auth.AuthenticatePrincipal(ctx, raw); err == nil {
		t.Fatal("disabled agent authenticated")
	}
	if _, _, err = keys.CreateWithOptions(ctx, user, apikey.CreateOptions{WorkspaceID: domain.PersonalWorkspaceID(user), Name: "disabled", AgentID: a.ID}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("issued disabled credential: %v", err)
	}
	disabled.Status = "active"
	if _, err = identities.Update(ctx, disabled); err != nil {
		t.Fatal(err)
	}
	if _, err = auth.AuthenticatePrincipal(ctx, raw); err == nil {
		t.Fatal("reactivation resurrected revoked credential")
	}
	// New credentials for another Agent are unaffected.
	active, activeRaw, err := keys.CreateWithOptions(ctx, user, apikey.CreateOptions{WorkspaceID: domain.PersonalWorkspaceID(user), Name: "active", AgentID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if db != nil {
		if _, err = db.ExecContext(ctx, `UPDATE api_keys SET expires_at=now()-interval '1 second' WHERE id=$1`, active.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = auth.AuthenticatePrincipal(ctx, activeRaw); err == nil {
			t.Fatal("expired SQL credential authenticated")
		}
		if _, err = db.ExecContext(ctx, `UPDATE api_keys SET expires_at=NULL,deleted_at=now() WHERE id=$1`, active.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = auth.AuthenticatePrincipal(ctx, activeRaw); err == nil {
			t.Fatal("deleted SQL credential authenticated")
		}
		var count int
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE payload->>'user_id'=$1`, user).Scan(&count); err != nil || count < 6 {
			t.Fatalf("transactional audit missing: %d %v", count, err)
		}
	} else {
		if _, err = auth.AuthenticatePrincipal(ctx, activeRaw); err != nil {
			t.Fatal(err)
		}
	}
	records, err := keys.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range records {
		if k.Hash != "" {
			t.Fatal("credential hash exposed in listing")
		}
	}
}

func runAgentManagementSuite(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	user := "agent-management-" + uuid.NewString()
	keys := apikey.NewService(runtimeconfig.NewStaticDBHandle(db))
	ids := keys.Identities()
	if db != nil {
		defer func() {
			for _, table := range []string{"api_keys", "workspace_grants", "agents", "actors", "workspaces"} {
				if _, err := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id=$1", user); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	var agents []domain.Agent
	for i := range 105 {
		a, err := ids.Create(ctx, user, fmt.Sprintf("Worker %03d", i), nil)
		if err != nil {
			t.Fatal(err)
		}
		agents = append(agents, a)
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		items, err := ids.Search(ctx, domain.AgentQuery{UserID: user, Query: "wOrKeR", After: cursor, Limit: 17})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range items {
			if seen[a.ID] || a.UserID != user {
				t.Fatal("duplicate or cross-owner search")
			}
			seen[a.ID] = true
			cursor = a.ID
		}
		if len(items) < 17 {
			break
		}
	}
	if len(seen) != 105 {
		t.Fatalf("pagination lost agents: %d", len(seen))
	}
	for _, query := range []domain.AgentQuery{{UserID: user, Query: "%"}, {UserID: user, Query: "_"}, {UserID: user, Status: "disabled"}} {
		items, err := ids.Search(ctx, query)
		if err != nil || len(items) != 0 {
			t.Fatalf("literal/status filter: %v %v", items, err)
		}
	}
	if _, err := ids.Search(ctx, domain.AgentQuery{UserID: user, Status: "unknown"}); err == nil {
		t.Fatal("invalid status accepted")
	}
	a := agents[0]
	if _, err := ids.Detail(ctx, "other", a.ID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatalf("cross-owner detail: %v", err)
	}
	if _, err := keys.ListForAgent(ctx, "other", a.ID, "", 10); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatalf("cross-owner credentials: %v", err)
	}
	for i := range 53 {
		_, _, err := keys.CreateWithOptions(ctx, user, apikey.CreateOptions{WorkspaceID: domain.PersonalWorkspaceID(user), Name: fmt.Sprintf("Key %d", i), AgentID: a.ID, Capabilities: []string{domain.CapabilityMemoryRead}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := keys.CreateWithOptions(ctx, user, apikey.CreateOptions{WorkspaceID: domain.PersonalWorkspaceID(user), Name: "Unrelated", AgentID: agents[1].ID}); err != nil {
		t.Fatal(err)
	}
	var credentials []domain.CredentialAccess
	cursor = ""
	for {
		items, err := keys.ListForAgent(ctx, user, a.ID, cursor, 13)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range items {
			if k.Hash != "" || k.AgentID != a.ID || !slices.Equal(k.EffectiveCapabilities, []string{domain.CapabilityMemoryRead}) {
				t.Fatal("credential metadata or permissions incorrect")
			}
			cursor = k.ID
		}
		credentials = append(credentials, items...)
		if len(items) < 13 {
			break
		}
	}
	if len(credentials) != 53 {
		t.Fatalf("credential count: %d", len(credentials))
	}
	d, err := ids.Detail(ctx, user, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Grant.Capabilities = []string{}
	if _, err = ids.SetGrant(ctx, d.Grant); err != nil {
		t.Fatal(err)
	}
	items, err := keys.ListForAgent(ctx, user, a.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range items {
		if len(k.EffectiveCapabilities) != 0 || len(k.Capabilities) != 1 {
			t.Fatal("ceiling and effective permissions conflated")
		}
	}
	a.Status = "disabled"
	if _, err = ids.Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	items, err = keys.ListForAgent(ctx, user, a.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range items {
		if k.RevokedAt == nil || len(k.EffectiveCapabilities) != 0 {
			t.Fatal("disabled agent credential remains effective")
		}
	}
}
