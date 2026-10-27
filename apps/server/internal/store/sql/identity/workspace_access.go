package identity

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"memstation/apps/server/internal/domain"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	scopestore "memstation/apps/server/internal/store/sql/internal/scope"

	"github.com/google/uuid"
)

func (r *IdentityRepository) CreateWorkspace(ctx context.Context, w domain.Workspace) (domain.Workspace, error) {
	if w.Kind != "group" && w.Kind != "project" || w.UserID == "" {
		return w, domain.ErrAccessDenied
	}
	w.ID = uuid.NewString()
	w.Status = "active"
	w.PolicyVersion = 1
	w.CreatedAt = time.Now().UTC()
	w.UpdatedAt = w.CreatedAt
	err := r.transaction(ctx, func(tx *sqlpkg.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1`, w.UserID).Scan(&owner); err != nil {
			return domain.ErrAccessNotFound
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO workspaces(id,user_id,name,kind,status,policy_version,capabilities) VALUES($1,$2,$3,$4,$5,$6,$7)`, w.ID, w.UserID, w.Name, w.Kind, w.Status, w.PolicyVersion, dbutilstore.MarshalJSON(w.Capabilities))
		if err != nil {
			return err
		}
		return r.audit(ctx, tx, w.UserID, "create_workspace", "workspace", w.ID)
	})
	return w, err
}

func (r *IdentityRepository) ResolveAccess(ctx context.Context, p domain.AccessContext, workspace string, audience domain.Audience) (domain.AccessContext, error) {
	if workspace == "" {
		workspace = p.WorkspaceID
	}
	if audience.Kind == "" {
		audience.Kind = "workspace"
	}
	if audience.Kind != "workspace" && audience.Kind != "direct" || audience.Kind == "workspace" && len(audience.ActorIDs) != 0 || audience.Kind == "direct" && (len(audience.ActorIDs) == 0 || len(audience.ActorIDs) > 16) {
		return domain.AccessContext{}, domain.ErrAccessDenied
	}
	db := dbutilstore.CurrentDB(r.db)
	if db == nil {
		if workspace != p.WorkspaceID || audience.Kind != "workspace" {
			return domain.AccessContext{}, domain.ErrAccessDenied
		}
		return p, nil
	}
	var ceiling, grant, policy []byte
	err := db.QueryRowContext(ctx, `SELECT k.capability_ceiling,g.capabilities,w.capabilities,w.policy_version,g.revision FROM api_keys k JOIN agents a ON a.id=k.agent_id AND a.user_id=k.user_id JOIN workspace_grants g ON g.agent_id=a.id AND g.user_id=a.user_id JOIN workspaces w ON w.id=g.workspace_id AND w.user_id=g.user_id WHERE k.id=$1 AND k.user_id=$2 AND a.id=$3 AND w.id=$4 AND k.revoked_at IS NULL AND k.deleted_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>now()) AND a.status='active' AND w.status='active'`, p.CredentialID, p.UserID, p.AgentID, workspace).Scan(&ceiling, &grant, &policy, &p.PolicyVersion, &p.GrantRevision)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return domain.AccessContext{}, domain.ErrAccessDenied
	}
	if err != nil {
		return p, err
	}
	var c, g, w []string
	for _, pair := range []struct {
		raw    []byte
		target *[]string
	}{{ceiling, &c}, {grant, &g}, {policy, &w}} {
		if err = json.Unmarshal(pair.raw, pair.target); err != nil {
			return p, err
		}
	}
	p.WorkspaceID = workspace
	p.ScopeVersion = 2
	p.PartitionIDs = []string{""}
	p.Audience = audience
	p.Capabilities = domain.EffectiveCapabilities(c, g, w)
	if audience.Kind == "direct" {
		for _, actor := range audience.ActorIDs {
			var id string
			if err = db.QueryRowContext(ctx, `SELECT actor_id FROM workspace_members WHERE user_id=$1 AND workspace_id=$2 AND actor_id=$3 AND status='active'`, p.UserID, workspace, actor).Scan(&id); err != nil {
				return domain.AccessContext{}, domain.ErrAccessDenied
			}
		}
		rows, err := db.QueryContext(ctx, `SELECT id,audience_actor_ids FROM memory_partitions WHERE user_id=$1 AND workspace_id=$2 AND agent_id=$3 ORDER BY id`, p.UserID, workspace, p.AgentID)
		if err != nil {
			return p, err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var raw []byte
			if err = rows.Scan(&id, &raw); err != nil {
				return p, err
			}
			var allowed []string
			if err = json.Unmarshal(raw, &allowed); err != nil {
				return p, err
			}
			eligible := true
			for _, actor := range audience.ActorIDs {
				if !slices.Contains(allowed, actor) {
					eligible = false
				}
			}
			if eligible {
				p.PartitionIDs = append(p.PartitionIDs, id)
			}
		}
		if err = rows.Err(); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (r *IdentityRepository) PutIntegration(ctx context.Context, input domain.IntegrationConfig) (domain.IntegrationConfig, error) {
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	err := r.transaction(ctx, func(tx *sqlpkg.Tx) error {
		if input.Revision == 0 {
			_, err := tx.ExecContext(ctx, `INSERT INTO integrations(id,user_id,name,kind,identity_namespace_id) VALUES($1,$2,$3,$4,$1)`, input.ID, input.UserID, input.Name, input.Kind)
			if err != nil {
				return err
			}
			input.Revision = 1
		} else {
			err := tx.QueryRowContext(ctx, `UPDATE integrations SET name=$3,revision=revision+1,updated_at=now() WHERE id=$1 AND user_id=$2 AND revision=$4 RETURNING revision`, input.ID, input.UserID, input.Name, input.Revision).Scan(&input.Revision)
			if errors.Is(err, sqlpkg.ErrNoRows) {
				return domain.ErrAccessConflict
			}
			if err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO workspace_sources(id,user_id,workspace_id,integration_id,external_space_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id,workspace_id,integration_id,external_space_id) DO NOTHING`, uuid.NewString(), input.UserID, input.WorkspaceID, input.ID, input.ExternalSpaceID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO integration_agent_grants(id,user_id,workspace_id,integration_id,agent_id,allowed_event_types) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(user_id,workspace_id,integration_id,agent_id) DO UPDATE SET allowed_event_types=EXCLUDED.allowed_event_types,revision=integration_agent_grants.revision+1,updated_at=now()`, uuid.NewString(), input.UserID, input.WorkspaceID, input.ID, input.AgentID, dbutilstore.MarshalJSON(input.AllowedEventTypes))
		if err != nil {
			return err
		}
		return r.audit(ctx, tx, input.UserID, "configure_integration", "integration", input.ID, map[string]any{"workspace_id": input.WorkspaceID, "agent_id": input.AgentID, "revision": input.Revision})
	})
	return input, err
}

func (r *IdentityRepository) ListIntegrations(ctx context.Context, user, workspace string) ([]domain.IntegrationConfig, error) {
	db := dbutilstore.CurrentDB(r.db)
	if db == nil {
		return nil, errors.New("durable identity database unavailable")
	}
	rows, err := db.QueryContext(ctx, `SELECT i.id,i.user_id,s.workspace_id,i.name,i.kind,s.external_space_id,g.agent_id,g.allowed_event_types,i.revision FROM integrations i JOIN workspace_sources s ON s.integration_id=i.id AND s.user_id=i.user_id JOIN integration_agent_grants g ON g.integration_id=i.id AND g.user_id=i.user_id AND g.workspace_id=s.workspace_id WHERE s.user_id=$1 AND s.workspace_id=$2 ORDER BY i.id,g.agent_id`, user, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.IntegrationConfig{}
	for rows.Next() {
		var v domain.IntegrationConfig
		var raw []byte
		if err = rows.Scan(&v.ID, &v.UserID, &v.WorkspaceID, &v.Name, &v.Kind, &v.ExternalSpaceID, &v.AgentID, &raw, &v.Revision); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &v.AllowedEventTypes); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (r *IdentityRepository) PutPartition(ctx context.Context, p domain.MemoryPartition) (domain.MemoryPartition, error) {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	err := r.transaction(ctx, func(tx *sqlpkg.Tx) error {
		for _, actor := range p.AudienceActorIDs {
			if err := scopestore.ValidateIngestActor(ctx, tx, p.UserID, p.WorkspaceID, actor); err != nil {
				return err
			}
		}
		if p.Revision == 0 {
			_, err := tx.ExecContext(ctx, `INSERT INTO memory_partitions(id,user_id,workspace_id,agent_id,audience_actor_ids) VALUES($1,$2,$3,$4,$5)`, p.ID, p.UserID, p.WorkspaceID, p.AgentID, dbutilstore.MarshalJSON(p.AudienceActorIDs))
			if err != nil {
				return err
			}
			p.Revision = 1
		} else {
			err := tx.QueryRowContext(ctx, `UPDATE memory_partitions SET audience_actor_ids=$5,revision=revision+1,updated_at=now() WHERE id=$1 AND user_id=$2 AND workspace_id=$3 AND agent_id=$4 AND revision=$6 RETURNING revision`, p.ID, p.UserID, p.WorkspaceID, p.AgentID, dbutilstore.MarshalJSON(p.AudienceActorIDs), p.Revision).Scan(&p.Revision)
			if errors.Is(err, sqlpkg.ErrNoRows) {
				return domain.ErrAccessConflict
			}
			if err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET policy_version=policy_version+1,updated_at=now() WHERE user_id=$1 AND id=$2`, p.UserID, p.WorkspaceID); err != nil {
			return err
		}
		return r.audit(ctx, tx, p.UserID, "configure_private_audience", "memory_partition", p.ID, map[string]any{"workspace_id": p.WorkspaceID, "revision": p.Revision})
	})
	return p, err
}

func (r *IdentityRepository) ListPartitions(ctx context.Context, user, workspace string) ([]domain.MemoryPartition, error) {
	db := dbutilstore.CurrentDB(r.db)
	if db == nil {
		return nil, errors.New("durable identity database unavailable")
	}
	rows, err := db.QueryContext(ctx, `SELECT id,user_id,workspace_id,agent_id,audience_actor_ids,revision FROM memory_partitions WHERE user_id=$1 AND workspace_id=$2 ORDER BY id`, user, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.MemoryPartition{}
	for rows.Next() {
		var v domain.MemoryPartition
		var raw []byte
		if err = rows.Scan(&v.ID, &v.UserID, &v.WorkspaceID, &v.AgentID, &raw, &v.Revision); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &v.AudienceActorIDs); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
