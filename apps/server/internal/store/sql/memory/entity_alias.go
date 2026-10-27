package memory

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	scopestore "memstation/apps/server/internal/store/sql/internal/scope"

	"github.com/google/uuid"
)

func prepareEntityAlias(a domain.EntityAlias, existing []domain.EntityAlias) (domain.EntityAlias, error) {
	a.Alias = domain.NormalizeEntityAlias(a.Alias)
	a.Scope = domain.NormalizeEntityAlias(a.Scope)
	if err := a.Validate(); err != nil {
		return a, err
	}
	now := time.Now().UTC()
	a.UpdatedAt = now
	for _, old := range existing {
		if old.Scope == a.Scope && old.Alias == a.Alias && old.TargetNodeID == a.TargetNodeID {
			a.ID = old.ID
			a.CreatedAt = old.CreatedAt
		}
	}
	if a.ID == "" {
		a.ID = uuid.NewString()
		a.CreatedAt = now
	}
	if a.Status != "confirmed" {
		return a, nil
	}
	links := map[string]string{}
	for _, old := range existing {
		if old.ID == a.ID || old.Status != "confirmed" {
			continue
		}
		if old.Scope == a.Scope && old.Alias == a.Alias {
			return a, errors.New("alias already has a confirmed target; revoke it first")
		}
		if old.SourceNodeID != "" {
			if a.SourceNodeID == old.SourceNodeID {
				return a, errors.New("entity already has a confirmed target")
			}
			links[old.SourceNodeID] = old.TargetNodeID
		}
	}
	if a.SourceNodeID != "" {
		links[a.SourceNodeID] = a.TargetNodeID
		seen := map[string]bool{}
		for n := a.SourceNodeID; n != ""; n = links[n] {
			if seen[n] {
				return a, errors.New("entity merge cycle")
			}
			seen[n] = true
			if len(seen) > 32 {
				return a, errors.New("entity merge chain exceeds 32 nodes")
			}
		}
	}
	return a, nil
}

func (r *InMemoryMemoryRepository) ListEntityAliases(ctx context.Context, userID, generationID string) ([]domain.EntityAlias, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := filterAliases(ctx, r.entityAliases, userID, generationID)
	visible := out[:0]
	for _, a := range out {
		if r.nodeOutsidePersonalLocked(ctx, userID, a.TargetNodeID) || (a.SourceNodeID != "" && r.nodeOutsidePersonalLocked(ctx, userID, a.SourceNodeID)) {
			continue
		}
		visible = append(visible, a)
	}
	return visible, nil
}

func filterAliases(ctx context.Context, all []domain.EntityAlias, userID, generationID string) []domain.EntityAlias {
	out := []domain.EntityAlias{}
	for _, a := range all {
		if a.UserID == userID && a.GenerationID == generationID && scopestore.PersonalMemoryWorkspace(ctx, userID, a.WorkspaceID) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *InMemoryMemoryRepository) PutEntityAlias(ctx context.Context, a domain.EntityAlias) (domain.EntityAlias, error) {
	if _, err := security.DataScope(ctx, a.UserID, a.WorkspaceID); err != nil {
		return domain.EntityAlias{}, err
	}
	a.WorkspaceID = security.WorkspaceID(ctx, a.UserID)
	r.mu.Lock()
	defer r.mu.Unlock()
	a.ID = "" // IDs are server-owned; identity is scoped alias plus target.
	var err error
	a, err = prepareEntityAlias(a, filterAliases(ctx, r.entityAliases, a.UserID, a.GenerationID))
	if err != nil {
		return a, err
	}
	for _, nodeID := range []string{a.SourceNodeID, a.TargetNodeID} {
		if nodeID == "" {
			continue
		}
		found := false
		for _, d := range r.documents {
			if d.UserID == a.UserID && scopestore.PersonalMemoryWorkspace(ctx, a.UserID, d.WorkspaceID) && d.GenerationID == a.GenerationID && d.NodeID == nodeID && d.Type == domain.MemoryTypeEntity {
				found = true
			}
		}
		if !found {
			return a, errors.New("entity endpoint not found in user generation")
		}
	}
	for i, old := range r.entityAliases {
		if old.ID == a.ID {
			r.entityAliases[i] = a
			return a, nil
		}
	}
	r.entityAliases = append(r.entityAliases, a)
	return a, nil
}

type aliasQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listAliases(ctx context.Context, db aliasQueryer, userID, generationID string) ([]domain.EntityAlias, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,user_id,generation_id,scope,alias,COALESCE(source_node_id,''),target_node_id,status,reason,created_at,updated_at FROM entity_aliases WHERE EXISTS (SELECT 1 FROM memory_nodes scope_node WHERE scope_node.user_id=entity_aliases.user_id AND scope_node.id=entity_aliases.target_node_id AND `+storeaccess.NodeSQL(ctx, "scope_node")+`) AND (source_node_id IS NULL OR EXISTS (SELECT 1 FROM memory_nodes scope_src WHERE scope_src.user_id=entity_aliases.user_id AND scope_src.id=entity_aliases.source_node_id AND scope_src.workspace_id = `+storeaccess.WorkspaceSQL(ctx, "scope_src.user_id")+`)) AND user_id=$1 AND generation_id=$2 ORDER BY id`, userID, generationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.EntityAlias{}
	for rows.Next() {
		var a domain.EntityAlias
		if err = rows.Scan(&a.ID, &a.UserID, &a.GenerationID, &a.Scope, &a.Alias, &a.SourceNodeID, &a.TargetNodeID, &a.Status, &a.Reason, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.WorkspaceID = security.WorkspaceID(ctx, a.UserID)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *PostgresMemoryRepository) ListEntityAliases(ctx context.Context, userID, generationID string) ([]domain.EntityAlias, error) {
	if _, err := security.DataScope(ctx, userID, ""); err != nil {
		return nil, err
	}
	return listAliases(ctx, r.db, userID, generationID)
}

func (r *PostgresMemoryRepository) PutEntityAlias(ctx context.Context, a domain.EntityAlias) (domain.EntityAlias, error) {
	if _, err := security.DataScope(ctx, a.UserID, a.WorkspaceID); err != nil {
		return domain.EntityAlias{}, err
	}
	a.WorkspaceID = security.WorkspaceID(ctx, a.UserID)
	a.ID = ""
	if err := a.Validate(); err != nil {
		return a, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return a, err
	}
	defer dbutilstore.Rollback(tx)
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`, a.UserID, "entity-alias:"+a.GenerationID); err != nil {
		return a, err
	}
	old, err := listAliases(ctx, tx, a.UserID, a.GenerationID)
	if err != nil {
		return a, err
	}
	a, err = prepareEntityAlias(a, old)
	if err != nil {
		return a, err
	}
	for _, nodeID := range []string{a.SourceNodeID, a.TargetNodeID} {
		if nodeID == "" {
			continue
		}
		var found string
		if err = tx.QueryRowContext(ctx, `SELECT n.id FROM memory_nodes n JOIN memory_items i ON i.user_id=n.user_id AND i.node_id=n.id AND i.deleted_at IS NULL WHERE `+storeaccess.NodeSQL(ctx, "n")+` AND n.user_id =$1 AND n.generation_id=$2 AND n.id=$3 AND n.node_type='entity' FOR SHARE OF n`, a.UserID, a.GenerationID, nodeID).Scan(&found); err != nil {
			return a, errors.New("entity endpoint not found in user generation")
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO entity_aliases(id,user_id,generation_id,scope,alias,source_node_id,target_node_id,status,reason,created_at,updated_at) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,$10,$11) ON CONFLICT(id) DO UPDATE SET source_node_id=EXCLUDED.source_node_id,status=EXCLUDED.status,reason=EXCLUDED.reason,updated_at=EXCLUDED.updated_at`, a.ID, a.UserID, a.GenerationID, a.Scope, a.Alias, a.SourceNodeID, a.TargetNodeID, a.Status, a.Reason, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return a, err
	}
	// A merge and its audit record commit together.
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_type,actor_id,action,target_type,target_id,payload,created_at,updated_at) VALUES($1,'admin','admin','memory.entity_alias.update','entity_alias',$2,$3,$4,$4)`, uuid.NewString(), a.ID, dbutilstore.MarshalJSON(a), a.UpdatedAt)
	if err != nil {
		return a, err
	}
	return a, tx.Commit()
}

func (r *HybridMemoryRepository) ListEntityAliases(ctx context.Context, u, g string) ([]domain.EntityAlias, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).ListEntityAliases(ctx, u, g)
	}
	return r.fallback.ListEntityAliases(ctx, u, g)
}

func (r *HybridMemoryRepository) PutEntityAlias(ctx context.Context, a domain.EntityAlias) (domain.EntityAlias, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).PutEntityAlias(ctx, a)
	}
	return r.fallback.PutEntityAlias(ctx, a)
}
