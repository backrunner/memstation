package sql

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"sort"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
)

// Empty scope is accepted only for legacy in-process records. New writes always
// materialize the server-bound workspace; an omitted legacy scope means Personal.
func personalMemoryWorkspace(ctx context.Context, user, workspace string) bool {
	resolved := security.WorkspaceID(ctx, user)
	return resolved != "" && (workspace == resolved || workspace == "" && resolved == domain.PersonalWorkspaceID(user))
}

func prepareDocumentAccess(ctx context.Context, doc *domain.MemoryDocument) error {
	partition := security.WritePartition(ctx)
	if doc.PartitionID != "" && doc.PartitionID != partition {
		return domain.ErrAccessDenied
	}
	if !security.CanReadPartition(ctx, partition) {
		return domain.ErrAccessDenied
	}
	doc.PartitionID = partition
	if doc.SubjectActorIDs == nil {
		doc.SubjectActorIDs = []string{}
	}
	if doc.SourceRefs == nil {
		doc.SourceRefs = []domain.SourceRef{}
	}
	if doc.Status == "" {
		doc.Status = "active"
	}
	if doc.Revision <= 0 {
		doc.Revision = 1
	}
	doc.Metadata = cloneMetadata(doc.Metadata)
	doc.Metadata["_access"] = map[string]any{"partition_id": doc.PartitionID, "subject_actor_ids": doc.SubjectActorIDs, "source_refs": doc.SourceRefs, "revision": doc.Revision, "status": doc.Status}
	return nil
}

func hydrateDocumentAccess(doc *domain.MemoryDocument) {
	if raw, ok := doc.Metadata["_access"]; ok {
		var access struct {
			PartitionID     string             `json:"partition_id"`
			SubjectActorIDs []string           `json:"subject_actor_ids"`
			SourceRefs      []domain.SourceRef `json:"source_refs"`
			Revision        int64              `json:"revision"`
			Status          string             `json:"status"`
		}
		encoded, err := json.Marshal(raw)
		if err == nil && json.Unmarshal(encoded, &access) == nil {
			doc.PartitionID = access.PartitionID
			doc.SubjectActorIDs = access.SubjectActorIDs
			doc.SourceRefs = access.SourceRefs
			doc.Revision = access.Revision
			doc.Status = access.Status
		}
	}
	if doc.Status == "" {
		doc.Status = "active"
	}
	if doc.Revision == 0 {
		doc.Revision = 1
	}
}

// nodeAccessMetadataSQL overrides historical business metadata using the node
// columns in the same SELECT snapshot, without extra round trips or query reordering.
func nodeAccessMetadataSQL() string {
	return `jsonb_build_object('_access',jsonb_build_object('partition_id',n.partition_id,'subject_actor_ids',n.subject_actor_ids,'source_refs',n.source_refs,'revision',n.revision,'status',n.access_status))`
}

func documentVisible(ctx context.Context, doc domain.MemoryDocument) bool {
	return security.MatchesMemoryFilter(ctx, doc.SubjectActorIDs, doc.ActivityAt) && personalMemoryWorkspace(ctx, doc.UserID, doc.WorkspaceID) && security.CanReadPartition(ctx, doc.PartitionID) && (doc.Status == "" || doc.Status == "active")
}

func requireMemoryNodeTx(ctx context.Context, tx *sqlpkg.Tx, user, nodeID string) error {
	workspace, err := security.DataScope(ctx, user, "")
	if err != nil {
		return err
	}
	var found string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM memory_nodes n WHERE user_id=$1 AND workspace_id=$2 AND id=$3 AND `+storeaccess.NodeSQL(ctx, "n")+` FOR SHARE`, user, workspace, nodeID).Scan(&found); err == sqlpkg.ErrNoRows {
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
func (r *InMemoryMemoryRepository) nodeOutsidePersonalLocked(ctx context.Context, user, nodeID string) bool {
	for _, doc := range r.documents {
		if doc.NodeID == nodeID && (doc.UserID != user || !documentVisible(ctx, doc)) {
			return true
		}
	}
	return false
}

func ensureMemoryWorkspaceTx(ctx context.Context, tx *sqlpkg.Tx, user string) error {
	workspace, err := security.DataScope(ctx, user, "")
	if err != nil {
		return err
	}
	if workspace != domain.PersonalWorkspaceID(user) {
		var found string
		err := tx.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE user_id=$1 AND id=$2 AND status='active' FOR SHARE`, user, workspace).Scan(&found)
		if err == sqlpkg.ErrNoRows {
			return domain.ErrAccessDenied
		}
		return err
	}
	w, _, _ := personalIdentity(user)
	_, err = tx.ExecContext(ctx, `INSERT INTO workspaces(id,user_id,name,kind,status,policy_version,capabilities) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, w.ID, user, w.Name, w.Kind, w.Status, w.PolicyVersion, marshalJSON(w.Capabilities))
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
	rows, err := tx.QueryContext(ctx, `SELECT id,user_id,workspace_id,node_type,entity_id,partition_id,access_status FROM memory_nodes WHERE id=$1 OR (node_type=$2 AND entity_id=$3) FOR UPDATE`, nodeID, kind, entityID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var storedID, owner, storedWorkspace, storedKind, storedEntity, partition, status string
		if err = rows.Scan(&storedID, &owner, &storedWorkspace, &storedKind, &storedEntity, &partition, &status); err != nil {
			return err
		}
		if owner != user || storedWorkspace != workspace || storedID != nodeID || storedKind != kind || storedEntity != entityID || partition != security.WritePartition(ctx) {
			return domain.ErrAccessDenied
		}
		if status != "active" {
			return domain.ErrAccessConflict
		}
	}
	return rows.Err()
}
