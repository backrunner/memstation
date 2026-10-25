package sql

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

const workspaceColumns = `id,user_id,name,kind,status,policy_version,capabilities,created_at,updated_at`

func scanWorkspace(row interface{ Scan(...any) error }) (domain.Workspace, error) {
	var w domain.Workspace
	var caps []byte
	err := row.Scan(&w.ID, &w.UserID, &w.Name, &w.Kind, &w.Status, &w.PolicyVersion, &caps, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return w, domain.ErrAccessNotFound
	}
	if err != nil {
		return w, err
	}
	return w, json.Unmarshal(caps, &w.Capabilities)
}

func (r *IdentityRepository) SearchWorkspaces(ctx context.Context, q domain.WorkspaceQuery) ([]domain.Workspace, error) {
	items := []domain.Workspace{}
	if db := currentDB(r.db); db != nil {
		rows, err := db.QueryContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces WHERE ($1='' OR user_id=$1) AND id>$2 AND (strpos(lower(name),lower($3))>0 OR strpos(lower(user_id),lower($3))>0 OR strpos(lower(id),lower($3))>0) AND kind IN('personal','group','project') ORDER BY id LIMIT $4`, q.UserID, q.After, q.Query, q.Limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			w, err := scanWorkspace(rows)
			if err != nil {
				return nil, err
			}
			items = append(items, w)
		}
		return items, rows.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.workspaces {
		if (q.UserID == "" || w.UserID == q.UserID) && w.ID > q.After && (w.Kind == "personal" || w.Kind == "group" || w.Kind == "project") && (strings.Contains(strings.ToLower(w.Name), strings.ToLower(q.Query)) || strings.Contains(strings.ToLower(w.UserID), strings.ToLower(q.Query)) || strings.Contains(strings.ToLower(w.ID), strings.ToLower(q.Query))) {
			w.Capabilities = append([]string{}, w.Capabilities...)
			items = append(items, w)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if len(items) > q.Limit {
		items = items[:q.Limit]
	}
	return items, nil
}

func (r *IdentityRepository) GetWorkspace(ctx context.Context, user, id string) (domain.Workspace, error) {
	if db := currentDB(r.db); db != nil {
		return scanWorkspace(db.QueryRowContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces WHERE id=$1 AND ($2='' OR user_id=$2)`, id, user))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.workspaces[id]
	if !ok || user != "" && w.UserID != user {
		return domain.Workspace{}, domain.ErrAccessNotFound
	}
	w.Capabilities = append([]string{}, w.Capabilities...)
	return w, nil
}

func (r *IdentityRepository) UpdateWorkspace(ctx context.Context, input domain.Workspace) (domain.Workspace, error) {
	var result domain.Workspace
	apply := func(old domain.Workspace) error {
		if old.UserID != input.UserID || old.ID != input.ID {
			return domain.ErrAccessNotFound
		}
		if old.Status != "active" {
			return domain.ErrAccessDenied
		}
		if old.PolicyVersion != input.PolicyVersion {
			return domain.ErrAccessConflict
		}
		result = old
		result.Name = input.Name
		result.Capabilities = append([]string{}, input.Capabilities...)
		result.PolicyVersion++
		result.UpdatedAt = time.Now().UTC()
		return nil
	}
	audit := func(tx *sqlpkg.Tx, old domain.Workspace) error {
		return r.audit(ctx, tx, input.UserID, "update_workspace", "workspace", input.ID, map[string]any{
			"previous_name": old.Name, "name": result.Name, "previous_capabilities": old.Capabilities,
			"capabilities": result.Capabilities, "previous_policy_version": old.PolicyVersion, "policy_version": result.PolicyVersion,
		})
	}
	if currentDB(r.db) != nil {
		err := r.transaction(ctx, func(tx *sqlpkg.Tx) error {
			old, err := scanWorkspace(tx.QueryRowContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces WHERE id=$1 AND user_id=$2 FOR UPDATE`, input.ID, input.UserID))
			if err != nil {
				return err
			}
			if err = apply(old); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE workspaces SET name=$3,capabilities=$4,policy_version=$5,updated_at=$6 WHERE id=$1 AND user_id=$2`, result.ID, result.UserID, result.Name, marshalJSON(result.Capabilities), result.PolicyVersion, result.UpdatedAt)
			if err != nil {
				return err
			}
			return audit(tx, old)
		})
		return result, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.workspaces[input.ID]
	if !ok {
		return result, domain.ErrAccessNotFound
	}
	if err := apply(old); err != nil {
		return result, err
	}
	r.workspaces[input.ID] = result
	result.Capabilities = append([]string{}, result.Capabilities...)
	return result, audit(nil, old)
}
