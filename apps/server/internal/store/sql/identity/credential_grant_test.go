package identity

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	testfixturestore "memstation/apps/server/internal/store/sql/internal/testfixture"

	"github.com/google/uuid"
)

func TestCredentialStoreRechecksGrant(t *testing.T) {
	runCredentialGrantCheck(t, nil)
}

func TestPostgresCredentialStoreRechecksGrant(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runCredentialGrantCheck(t, db)
}

func runCredentialGrantCheck(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	user := "credential-race-" + uuid.NewString()
	r := NewIdentityRepository(runtimeconfig.NewStaticDBHandle(db))
	if db != nil {
		defer func() {
			for _, table := range []string{"api_keys", "workspace_grants", "agents", "actors", "workspaces"} {
				if _, err := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id=$1", user); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	w, err := r.EnsurePersonal(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	testfixturestore.ProvisionFixtureAgent(t, r, user)
	g, err := r.GetGrant(ctx, user, w.ID, testfixturestore.FixtureAgentID(user))
	if err != nil {
		t.Fatal(err)
	}
	// A service read has already approved this ceiling. Revoke before the store
	// transaction starts; it must not persist the previously approved request.
	k := domain.Credential{ID: uuid.NewString(), UserID: user, AgentID: g.AgentID, DefaultWorkspaceID: w.ID, Name: "stale ceiling", Prefix: "test", Hash: uuid.NewString(), Capabilities: []string{domain.CapabilityMemoryRead}, CreatedAt: time.Now().UTC()}
	g.Capabilities = []string{}
	if _, err = r.PutGrant(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err = r.CreateCredential(ctx, k); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("stale ceiling stored: %v", err)
	}
	items, err := r.ListAgentCredentials(ctx, user, g.AgentID, "", 50)
	if err != nil || len(items) != 0 {
		t.Fatalf("denied issuance left a credential: %v %v", items, err)
	}
}
