package sql

import (
	"context"
	sqlpkg "database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/service/identity"

	"github.com/google/uuid"
)

func TestWorkspacePolicyLifecycle(t *testing.T) { runWorkspacePolicyLifecycle(t, nil) }

func TestPostgresWorkspacePolicyLifecycle(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sqlpkg.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runWorkspacePolicyLifecycle(t, db)
}

func runWorkspacePolicyLifecycle(t *testing.T, db *sqlpkg.DB) {
	t.Helper()
	ctx := context.Background()
	r := NewIdentityRepository(runtimeconfig.NewStaticDBHandle(db))
	s := identity.NewService(r)
	owner := "workspace-policy-" + uuid.NewString()
	other := owner + "-other"
	if db != nil {
		defer func() {
			for _, table := range []string{"api_keys", "workspace_grants", "agents", "actors", "workspaces"} {
				if _, err := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id IN ($1,$2)", owner, other); err != nil {
					t.Error(err)
				}
			}
			if _, err := db.ExecContext(ctx, `DELETE FROM audit_events WHERE payload->>'user_id' IN ($1,$2)`, owner, other); err != nil {
				t.Error(err)
			}
		}()
	}
	page, err := s.Workspaces(ctx, domain.WorkspaceQuery{UserID: owner})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("directory provisioned a phantom workspace", err)
	}
	w, err := s.Personal(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Personal(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Workspace(ctx, other, w.ID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatal("cross-owner read", err)
	}
	page, err = s.Workspaces(ctx, domain.WorkspaceQuery{Query: owner, Limit: 1})
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal("first page", page, err)
	}
	next, err := s.Workspaces(ctx, domain.WorkspaceQuery{Query: owner, Limit: 1, After: page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.NextCursor != "" || page.Items[0].ID == next.Items[0].ID {
		t.Fatal("cursor page", next, err)
	}
	k := domain.Credential{ID: uuid.NewString(), UserID: owner, AgentID: domain.LegacyAgentID(owner), DefaultWorkspaceID: w.ID, Name: "policy test", Prefix: uuid.NewString(), Hash: uuid.NewString(), Capabilities: []string{domain.CapabilityMemoryRead}, CreatedAt: time.Now().UTC()}
	if err = r.CreateCredential(ctx, k); err != nil {
		t.Fatal(err)
	}
	stale := w
	w.Name = "Project notes"
	w.Capabilities = []string{}
	var won atomic.Int32
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			_, err := s.UpdateWorkspace(ctx, w)
			if err == nil {
				won.Add(1)
			} else if !errors.Is(err, domain.ErrAccessConflict) {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if won.Load() != 1 {
		t.Fatalf("concurrent policy writers: %d", won.Load())
	}
	current, err := s.Workspace(ctx, owner, w.ID)
	if err != nil || current.Name != w.Name || current.PolicyVersion != 2 || current.CreatedAt.IsZero() || current.UpdatedAt.Before(current.CreatedAt) {
		t.Fatal("saved workspace", current, err)
	}
	access, err := r.Authenticate(ctx, k.Prefix, k.Hash)
	if err != nil || len(access.Capabilities) != 0 || access.PolicyVersion != 2 {
		t.Fatal("old key retained revoked capability", access, err)
	}
	newKey := k
	newKey.ID = uuid.NewString()
	if err = r.CreateCredential(ctx, newKey); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("stale issue bypassed policy", err)
	}
	if _, err = s.UpdateWorkspace(ctx, stale); !errors.Is(err, domain.ErrAccessConflict) {
		t.Fatal("stale writer", err)
	}
	for _, caps := range [][]string{nil, {"workspace.admin"}, {domain.CapabilityMemoryRead, domain.CapabilityMemoryRead}} {
		input := current
		input.Capabilities = caps
		if _, err = s.UpdateWorkspace(ctx, input); !errors.Is(err, identity.ErrInvalidInput) {
			t.Fatal("invalid policy", caps, err)
		}
	}
	current.Capabilities = []string{domain.CapabilityMemoryRead, domain.CapabilityFilesWrite}
	current, err = s.UpdateWorkspace(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	access, err = r.Authenticate(ctx, k.Prefix, k.Hash)
	if err != nil || len(access.Capabilities) != 1 || access.Capabilities[0] != domain.CapabilityMemoryRead {
		t.Fatal("credential ceiling widened", access, err)
	}
	current.Capabilities[0] = "mutated"
	current, err = r.GetWorkspace(ctx, owner, w.ID)
	if err != nil || current.Capabilities[0] == "mutated" {
		t.Fatal("read alias escaped", err)
	}
	var audits int
	if db != nil {
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='update_workspace' AND payload->>'previous_policy_version' IN ('1','2')`, w.ID).Scan(&audits); err != nil {
			t.Fatal(err)
		}
	} else {
		for _, event := range r.audits {
			if event.Action == "update_workspace" && event.TargetID == w.ID {
				audits++
			}
		}
	}
	if audits != 2 {
		t.Fatalf("audit must match successful updates: %d", audits)
	}
	if db != nil {
		// A failing audit must roll back the policy, not leave a successful but
		// unaudited permission change. The trigger affects only this test owner.
		name := "workspace_audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		_, err = db.ExecContext(ctx, `CREATE FUNCTION `+name+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.payload->>'user_id'='`+owner+`' AND NEW.action='update_workspace' THEN RAISE EXCEPTION 'test audit unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER `+name+` BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION `+name+`()`) // owner is generated above, never external input.
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := db.ExecContext(ctx, `DROP TRIGGER `+name+` ON audit_events; DROP FUNCTION `+name+`() `); err != nil {
				t.Error(err)
			}
		}()
		attempt := current
		attempt.Name = "must roll back"
		if _, err = s.UpdateWorkspace(ctx, attempt); err == nil {
			t.Fatal("audit failure accepted")
		}
		after, err := s.Workspace(ctx, owner, w.ID)
		if err != nil || after.PolicyVersion != current.PolicyVersion || after.Name != current.Name {
			t.Fatal("audit failure left a partial write", after, err)
		}
	}
	unsupported := domain.Workspace{ID: "project-" + uuid.NewString(), UserID: owner, Name: "Hidden project", Kind: "project", Status: "active", PolicyVersion: 1, Capabilities: domain.PersonalCapabilities()}
	if db == nil {
		r.workspaces[unsupported.ID] = unsupported
	} else if _, err = db.ExecContext(ctx, `INSERT INTO workspaces (id,user_id,name,kind,status,policy_version,capabilities) VALUES ($1,$2,$3,$4,$5,$6,$7)`, unsupported.ID, owner, unsupported.Name, unsupported.Kind, unsupported.Status, unsupported.PolicyVersion, marshalJSON(unsupported.Capabilities)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Workspace(ctx, owner, unsupported.ID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatal("unsupported scope opened", err)
	}
	page, err = s.Workspaces(ctx, domain.WorkspaceQuery{UserID: owner})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != w.ID {
		t.Fatal("unsupported scope listed", page, err)
	}
}
