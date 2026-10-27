package scope

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
)

func PersonalWorkspace(user string) domain.Workspace {
	now := time.Now().UTC()
	return domain.Workspace{ID: domain.PersonalWorkspaceID(user), UserID: user, Name: "Personal", Kind: "personal", Status: "active", PolicyVersion: 1, Capabilities: domain.PersonalCapabilities(), CreatedAt: now, UpdatedAt: now}
}

func EnsurePersonalTx(ctx context.Context, tx *sqlpkg.Tx, user string) (domain.Workspace, error) {
	w := PersonalWorkspace(user)
	_, err := tx.ExecContext(ctx, `INSERT INTO workspaces(id,user_id,name,kind,status,policy_version,capabilities) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, w.ID, user, w.Name, w.Kind, w.Status, w.PolicyVersion, dbutilstore.MarshalJSON(w.Capabilities))
	if err != nil {
		return w, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO actors(id,user_id,kind,display_name) VALUES ($1,$2,'human','Owner') ON CONFLICT DO NOTHING`, domain.AccessID("owner", user), user)
	if err != nil {
		return w, err
	}
	var caps []byte
	err = tx.QueryRowContext(ctx, `SELECT name,kind,status,policy_version,capabilities,created_at,updated_at FROM workspaces WHERE user_id=$1 AND id=$2`, user, w.ID).Scan(&w.Name, &w.Kind, &w.Status, &w.PolicyVersion, &caps, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return w, err
	}
	return w, json.Unmarshal(caps, &w.Capabilities)
}

func ValidateIngestActor(ctx context.Context, tx *sqlpkg.Tx, user, workspace, actor string) error {
	var found string
	err := tx.QueryRowContext(ctx, `SELECT actor_id FROM workspace_members WHERE user_id=$1 AND workspace_id=$2 AND actor_id=$3 AND status='active'`, user, workspace, actor).Scan(&found)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return domain.ErrAccessDenied
	}
	return err
}

func LifecycleAccess(ctx context.Context) (domain.AccessContext, error) {
	a, ok := security.AccessFromContext(ctx)
	if !ok || a.UserID == "" || a.WorkspaceID == "" {
		return a, domain.ErrAccessDenied
	}
	if _, err := security.DataScope(ctx, a.UserID, a.WorkspaceID); err != nil {
		return a, err
	}
	return a, nil
}
