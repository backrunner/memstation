package sql

import (
	"context"
	sqlpkg "database/sql"
	"sort"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

// Empty scope is accepted only for legacy in-process records. New writes always
// materialize Personal scope; additional workspaces remain disabled.
func personalMemoryWorkspace(user, workspace string) bool {
	return workspace == "" || workspace == domain.PersonalWorkspaceID(user)
}

func requireMemoryNodeTx(ctx context.Context, tx *sqlpkg.Tx, user, nodeID string) error {
	workspace, err := security.PersonalScope(ctx, user, "")
	if err != nil {
		return err
	}
	var found string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM memory_nodes WHERE user_id=$1 AND workspace_id=$2 AND id=$3 FOR SHARE`, user, workspace, nodeID).Scan(&found); err == sqlpkg.ErrNoRows {
		return domain.ErrAccessDenied
	}
	return err
}

func validateEvidenceIdentity(user, nodeID string, evidences []domain.MemoryEvidence) error {
	for _, e := range evidences {
		if e.UserID != "" && e.UserID != user || e.NodeID != "" && e.NodeID != nodeID {
			return domain.ErrAccessDenied
		}
	}
	return nil
}

// In-memory fixtures can hold unmaterialized legacy endpoints. A known foreign
// endpoint must always be rejected, including evidence and graph operations.
func (r *InMemoryMemoryRepository) nodeOutsidePersonalLocked(user, nodeID string) bool {
	for _, doc := range r.documents {
		if doc.NodeID == nodeID && (doc.UserID != user || !personalMemoryWorkspace(user, doc.WorkspaceID)) {
			return true
		}
	}
	return false
}

func ensureMemoryWorkspaceTx(ctx context.Context, tx *sqlpkg.Tx, user string) error {
	w, _, _ := personalIdentity(user)
	_, err := tx.ExecContext(ctx, `INSERT INTO workspaces(id,user_id,name,kind,status,policy_version,capabilities) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, w.ID, user, w.Name, w.Kind, w.Status, w.PolicyVersion, marshalJSON(w.Capabilities))
	return err
}

func lockMemoryIdentityTx(ctx context.Context, tx *sqlpkg.Tx, user, workspace, nodeID, entityID, kind string) error {
	locks := []string{"memory-node:" + nodeID, "memory-entity:" + kind + ":" + entityID}
	sort.Strings(locks)
	for _, lock := range locks {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lock); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,user_id,workspace_id,node_type,entity_id FROM memory_nodes WHERE id=$1 OR (node_type=$2 AND entity_id=$3) FOR UPDATE`, nodeID, kind, entityID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var storedID, owner, storedWorkspace, storedKind, storedEntity string
		if err = rows.Scan(&storedID, &owner, &storedWorkspace, &storedKind, &storedEntity); err != nil {
			return err
		}
		if owner != user || storedWorkspace != workspace || storedID != nodeID || storedKind != kind || storedEntity != entityID {
			return domain.ErrAccessDenied
		}
	}
	return rows.Err()
}
