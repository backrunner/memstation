package sql

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/runtimeconfig"

	"github.com/google/uuid"
)

// IdentityRepository keeps the development fallback separate from the SQL path.
// A SQL failure never falls back to stale in-memory grants or credentials.
type IdentityRepository struct {
	db            runtimeconfig.DBHandle
	mu            sync.Mutex
	workspaces    map[string]domain.Workspace
	agents        map[string]domain.Agent
	grants        map[string]domain.WorkspaceGrant
	credentials   map[string]domain.Credential
	audits        []domain.AuditEvent
	fallbackAudit func(context.Context, domain.AuditEvent)
}

var _ ports.IdentityRepository = (*IdentityRepository)(nil)

func NewIdentityRepository(db runtimeconfig.DBHandle) *IdentityRepository {
	return &IdentityRepository{db: db, workspaces: map[string]domain.Workspace{}, agents: map[string]domain.Agent{}, grants: map[string]domain.WorkspaceGrant{}, credentials: map[string]domain.Credential{}}
}

func (r *IdentityRepository) transaction(ctx context.Context, fn func(*sqlpkg.Tx) error) error {
	db := currentDB(r.db)
	if db == nil {
		return errors.New("identity database unavailable")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *IdentityRepository) audit(ctx context.Context, tx *sqlpkg.Tx, user, action, kind, id string, details ...map[string]any) error {
	event := domain.AuditEvent{ID: uuid.NewString(), ActorType: "admin", ActorID: "admin", Action: action, TargetType: kind, TargetID: id, Payload: map[string]any{"user_id": user}, CreatedAt: time.Now().UTC()}
	for _, detail := range details {
		for key, value := range detail {
			event.Payload[key] = value
		}
	}
	if tx == nil {
		r.audits = append(r.audits, event)
		if r.fallbackAudit != nil {
			r.fallbackAudit(ctx, event)
		}
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_events (id,actor_type,actor_id,action,target_type,target_id,payload,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)`, event.ID, event.ActorType, event.ActorID, event.Action, event.TargetType, event.TargetID, marshalJSON(event.Payload), event.CreatedAt)
	return err
}

func (r *IdentityRepository) EnsurePersonal(ctx context.Context, user string) (domain.Workspace, error) {
	if user == "" {
		return domain.Workspace{}, domain.ErrAccessDenied
	}
	w, a, g := personalIdentity(user)
	if currentDB(r.db) != nil {
		err := r.transaction(ctx, func(tx *sqlpkg.Tx) error {
			var err error
			w, err = ensurePersonalTx(ctx, tx, user)
			return err
		})
		return w, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.workspaces[w.ID]; ok {
		existing.Capabilities = append([]string{}, existing.Capabilities...)
		return existing, nil
	}
	r.workspaces[w.ID] = w
	r.agents[a.ID] = a
	r.grants[a.ID] = g
	w.Capabilities = append([]string{}, w.Capabilities...)
	return w, nil
}

func (r *IdentityRepository) CreateAgent(ctx context.Context, a domain.Agent, g domain.WorkspaceGrant) error {
	if currentDB(r.db) != nil {
		return r.transaction(ctx, func(tx *sqlpkg.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO actors(id,user_id,kind,display_name) VALUES ($1,$2,'agent',$3)`, a.ActorID, a.UserID, a.Name)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO agents(id,user_id,actor_id,name,status,default_workspace_id,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, a.ID, a.UserID, a.ActorID, a.Name, a.Status, a.DefaultWorkspaceID, a.CreatedAt, a.UpdatedAt)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO workspace_grants(id,user_id,workspace_id,agent_id,capabilities) VALUES ($1,$2,$3,$4,$5)`, uuid.NewString(), g.UserID, g.WorkspaceID, g.AgentID, marshalJSON(g.Capabilities))
			if err != nil {
				return err
			}
			return r.audit(ctx, tx, a.UserID, "create_agent", "agent", a.ID, map[string]any{"workspace_id": a.DefaultWorkspaceID, "name": a.Name, "revision": a.Revision, "capabilities": append([]string{}, g.Capabilities...)})
		})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.workspaces[a.DefaultWorkspaceID]
	if !ok || w.UserID != a.UserID {
		return domain.ErrAccessNotFound
	}
	if _, ok = r.agents[a.ID]; ok {
		return domain.ErrAccessConflict
	}
	r.agents[a.ID] = a
	g.Capabilities = append([]string{}, g.Capabilities...)
	r.grants[a.ID] = g
	return r.audit(ctx, nil, a.UserID, "create_agent", "agent", a.ID, map[string]any{"workspace_id": a.DefaultWorkspaceID, "name": a.Name, "revision": a.Revision, "capabilities": append([]string{}, g.Capabilities...)})
}

func (r *IdentityRepository) ListAgents(ctx context.Context, user, after string, limit int) ([]domain.Agent, error) {
	return r.SearchAgents(ctx, domain.AgentQuery{UserID: user, After: after, Limit: limit})
}

func (r *IdentityRepository) SearchAgents(ctx context.Context, q domain.AgentQuery) ([]domain.Agent, error) {
	out := []domain.Agent{}
	if db := currentDB(r.db); db != nil {
		rows, err := db.QueryContext(ctx, `SELECT id,user_id,actor_id,name,status,default_workspace_id,revision,legacy,created_at,updated_at FROM agents WHERE user_id=$1 AND id>$2 AND ($4='' OR strpos(lower(name),lower($4))>0 OR strpos(lower(id),lower($4))>0) AND ($5='' OR status=$5) ORDER BY id LIMIT $3`, q.UserID, q.After, q.Limit, q.Query, q.Status)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var a domain.Agent
			if err = rows.Scan(&a.ID, &a.UserID, &a.ActorID, &a.Name, &a.Status, &a.DefaultWorkspaceID, &a.Revision, &a.Legacy, &a.CreatedAt, &a.UpdatedAt); err != nil {
				return nil, err
			}
			out = append(out, a)
		}
		return out, rows.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.agents {
		if a.UserID == q.UserID && a.ID > q.After && (q.Status == "" || a.Status == q.Status) && (strings.Contains(strings.ToLower(a.Name), strings.ToLower(q.Query)) || strings.Contains(strings.ToLower(a.ID), strings.ToLower(q.Query))) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (r *IdentityRepository) UpdateAgent(ctx context.Context, a domain.Agent) (domain.Agent, error) {
	if currentDB(r.db) != nil {
		err := r.transaction(ctx, func(tx *sqlpkg.Tx) error {
			err := tx.QueryRowContext(ctx, `UPDATE agents SET name=$3,status=$4,revision=revision+1,updated_at=now() WHERE user_id=$1 AND id=$2 AND revision=$5 RETURNING actor_id,default_workspace_id,revision,legacy,created_at,updated_at`, a.UserID, a.ID, a.Name, a.Status, a.Revision).Scan(&a.ActorID, &a.DefaultWorkspaceID, &a.Revision, &a.Legacy, &a.CreatedAt, &a.UpdatedAt)
			if errors.Is(err, sqlpkg.ErrNoRows) {
				return domain.ErrAccessConflict
			}
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE actors SET display_name=$3,status=$4,revision=revision+1,updated_at=now() WHERE user_id=$1 AND id=$2`, a.UserID, a.ActorID, a.Name, a.Status); err != nil {
				return err
			}
			if a.Status == "disabled" {
				if _, err = tx.ExecContext(ctx, `UPDATE api_keys SET revoked_at=COALESCE(revoked_at,now()),updated_at=now() WHERE user_id=$1 AND agent_id=$2`, a.UserID, a.ID); err != nil {
					return err
				}
			}
			return r.audit(ctx, tx, a.UserID, "update_agent", "agent", a.ID, map[string]any{"status": a.Status, "name": a.Name, "revision": a.Revision})
		})
		return a, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.agents[a.ID]
	if !ok || old.UserID != a.UserID {
		return domain.Agent{}, domain.ErrAccessNotFound
	}
	if old.Revision != a.Revision {
		return domain.Agent{}, domain.ErrAccessConflict
	}
	old.Name = a.Name
	old.Status = a.Status
	old.Revision++
	old.UpdatedAt = time.Now().UTC()
	r.agents[a.ID] = old
	if old.Status == "disabled" {
		for id, k := range r.credentials {
			if k.AgentID == a.ID && k.UserID == a.UserID && k.RevokedAt == nil {
				now := old.UpdatedAt
				k.RevokedAt = &now
				r.credentials[id] = k
			}
		}
	}
	return old, r.audit(ctx, nil, a.UserID, "update_agent", "agent", a.ID, map[string]any{"status": old.Status, "name": old.Name, "revision": old.Revision})
}

func (r *IdentityRepository) GetGrant(ctx context.Context, user, workspace, agent string) (domain.WorkspaceGrant, error) {
	g := domain.WorkspaceGrant{UserID: user, WorkspaceID: workspace, AgentID: agent}
	if db := currentDB(r.db); db != nil {
		var caps []byte
		err := db.QueryRowContext(ctx, `SELECT capabilities,revision FROM workspace_grants WHERE user_id=$1 AND workspace_id=$2 AND agent_id=$3`, user, workspace, agent).Scan(&caps, &g.Revision)
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return g, domain.ErrAccessNotFound
		}
		if err != nil {
			return g, err
		}
		return g, json.Unmarshal(caps, &g.Capabilities)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.grants[agent]
	if !ok || g.UserID != user || g.WorkspaceID != workspace {
		return domain.WorkspaceGrant{}, domain.ErrAccessNotFound
	}
	g.Capabilities = append([]string{}, g.Capabilities...)
	return g, nil
}

func (r *IdentityRepository) PutGrant(ctx context.Context, g domain.WorkspaceGrant) (domain.WorkspaceGrant, error) {
	if currentDB(r.db) != nil {
		err := r.transaction(ctx, func(tx *sqlpkg.Tx) error {
			err := tx.QueryRowContext(ctx, `UPDATE workspace_grants SET capabilities=$4,revision=revision+1,updated_at=now() WHERE user_id=$1 AND workspace_id=$2 AND agent_id=$3 AND revision=$5 RETURNING revision`, g.UserID, g.WorkspaceID, g.AgentID, marshalJSON(g.Capabilities), g.Revision).Scan(&g.Revision)
			if errors.Is(err, sqlpkg.ErrNoRows) {
				return domain.ErrAccessConflict
			}
			if err != nil {
				return err
			}
			return r.audit(ctx, tx, g.UserID, "update_workspace_grant", "agent", g.AgentID, map[string]any{"workspace_id": g.WorkspaceID, "capabilities": append([]string{}, g.Capabilities...), "revision": g.Revision})
		})
		return g, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.grants[g.AgentID]
	if !ok || old.UserID != g.UserID || old.WorkspaceID != g.WorkspaceID {
		return g, domain.ErrAccessNotFound
	}
	if old.Revision != g.Revision {
		return g, domain.ErrAccessConflict
	}
	g.Revision++
	g.Capabilities = append([]string{}, g.Capabilities...)
	r.grants[g.AgentID] = g
	g.Capabilities = append([]string{}, g.Capabilities...)
	return g, r.audit(ctx, nil, g.UserID, "update_workspace_grant", "agent", g.AgentID, map[string]any{"workspace_id": g.WorkspaceID, "capabilities": append([]string{}, g.Capabilities...), "revision": g.Revision})
}

func (r *IdentityRepository) CreateCredential(ctx context.Context, k domain.Credential) error {
	if currentDB(r.db) != nil {
		return r.transaction(ctx, func(tx *sqlpkg.Tx) error {
			// The row lock serializes issue vs disable. Disabling after issue revokes it;
			// disabling before issue makes this transaction fail.
			var active bool
			err := tx.QueryRowContext(ctx, `SELECT status='active' FROM agents WHERE user_id=$1 AND id=$2 FOR UPDATE`, k.UserID, k.AgentID).Scan(&active)
			if errors.Is(err, sqlpkg.ErrNoRows) || err == nil && !active {
				return domain.ErrAccessDenied
			}
			if err != nil {
				return err
			}
			var policy, grant []byte
			var workspaceActive bool
			if err = tx.QueryRowContext(ctx, `SELECT status='active',capabilities FROM workspaces WHERE user_id=$1 AND id=$2 FOR SHARE`, k.UserID, k.DefaultWorkspaceID).Scan(&workspaceActive, &policy); err != nil {
				return err
			}
			if err = tx.QueryRowContext(ctx, `SELECT capabilities FROM workspace_grants WHERE user_id=$1 AND workspace_id=$2 AND agent_id=$3 FOR SHARE`, k.UserID, k.DefaultWorkspaceID, k.AgentID).Scan(&grant); err != nil {
				return err
			}
			var p, g []string
			if err = json.Unmarshal(policy, &p); err != nil {
				return err
			}
			if err = json.Unmarshal(grant, &g); err != nil {
				return err
			}
			if !workspaceActive || len(domain.EffectiveCapabilities(k.Capabilities, g, p)) != len(k.Capabilities) {
				return domain.ErrAccessDenied
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO api_keys(id,user_id,agent_id,default_workspace_id,name,prefix,hash,capability_ceiling,expires_at,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`, k.ID, k.UserID, k.AgentID, k.DefaultWorkspaceID, k.Name, k.Prefix, k.Hash, marshalJSON(k.Capabilities), k.ExpiresAt, k.CreatedAt)
			if err != nil {
				return err
			}
			return r.audit(ctx, tx, k.UserID, "issue_agent_credential", "api_key", k.ID, map[string]any{"agent_id": k.AgentID, "workspace_id": k.DefaultWorkspaceID, "capabilities": append([]string{}, k.Capabilities...), "expires_at": k.ExpiresAt})
		})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.agents[k.AgentID]
	if !ok || a.UserID != k.UserID || a.Status != "active" || a.DefaultWorkspaceID != k.DefaultWorkspaceID {
		return domain.ErrAccessDenied
	}
	w, wok := r.workspaces[k.DefaultWorkspaceID]
	g, gok := r.grants[k.AgentID]
	if !wok || !gok || w.Status != "active" || g.UserID != k.UserID || g.WorkspaceID != w.ID || len(domain.EffectiveCapabilities(k.Capabilities, g.Capabilities, w.Capabilities)) != len(k.Capabilities) {
		return domain.ErrAccessDenied
	}
	k.Capabilities = append([]string{}, k.Capabilities...)
	r.credentials[k.ID] = cloneCredential(k)
	return r.audit(ctx, nil, k.UserID, "issue_agent_credential", "api_key", k.ID, map[string]any{"agent_id": k.AgentID, "workspace_id": k.DefaultWorkspaceID, "capabilities": append([]string{}, k.Capabilities...), "expires_at": k.ExpiresAt})
}

func (r *IdentityRepository) RevokeCredential(ctx context.Context, user, id string) error {
	if currentDB(r.db) != nil {
		return r.transaction(ctx, func(tx *sqlpkg.Tx) error {
			err := tx.QueryRowContext(ctx, `UPDATE api_keys SET revoked_at=COALESCE(revoked_at,now()),updated_at=now() WHERE id=$1 AND ($2='' OR user_id=$2) RETURNING user_id`, id, user).Scan(&user)
			if errors.Is(err, sqlpkg.ErrNoRows) {
				return domain.ErrAccessNotFound
			}
			if err != nil {
				return err
			}
			return r.audit(ctx, tx, user, "revoke_agent_credential", "api_key", id)
		})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.credentials[id]
	if !ok || user != "" && k.UserID != user {
		return domain.ErrAccessNotFound
	}
	if k.RevokedAt == nil {
		now := time.Now().UTC()
		k.RevokedAt = &now
		r.credentials[id] = k
	}
	return r.audit(ctx, nil, k.UserID, "revoke_agent_credential", "api_key", id)
}

func (r *IdentityRepository) ListCredentials(ctx context.Context) ([]domain.Credential, error) {
	out := []domain.Credential{}
	if db := currentDB(r.db); db != nil {
		rows, err := db.QueryContext(ctx, `SELECT k.id,k.user_id,k.agent_id,k.default_workspace_id,k.name,k.prefix,k.capability_ceiling,k.expires_at,k.revoked_at,k.created_at,a.name FROM api_keys k JOIN agents a ON a.user_id=k.user_id AND a.id=k.agent_id WHERE k.deleted_at IS NULL ORDER BY k.created_at DESC,k.id`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var k domain.Credential
			var caps []byte
			if err = rows.Scan(&k.ID, &k.UserID, &k.AgentID, &k.DefaultWorkspaceID, &k.Name, &k.Prefix, &caps, &k.ExpiresAt, &k.RevokedAt, &k.CreatedAt, &k.AgentName); err != nil {
				return nil, err
			}
			if err = json.Unmarshal(caps, &k.Capabilities); err != nil {
				return nil, err
			}
			out = append(out, k)
		}
		return out, rows.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range r.credentials {
		k.AgentName = r.agents[k.AgentID].Name
		k = cloneCredential(k)
		k.Hash = ""
		k.Capabilities = append([]string{}, k.Capabilities...)
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (r *IdentityRepository) Authenticate(ctx context.Context, prefix, hash string) (domain.AccessContext, error) {
	var result domain.AccessContext
	if db := currentDB(r.db); db != nil {
		var ceiling, grant, policy []byte
		err := db.QueryRowContext(ctx, `SELECT k.user_id,a.id,k.id,w.id,k.capability_ceiling,g.capabilities,w.capabilities,w.policy_version,g.revision,a.legacy
   FROM api_keys k JOIN agents a ON a.user_id=k.user_id AND a.id=k.agent_id
   JOIN workspaces w ON w.user_id=k.user_id AND w.id=k.default_workspace_id
   JOIN workspace_grants g ON g.user_id=k.user_id AND g.agent_id=a.id AND g.workspace_id=w.id
   WHERE k.prefix=$1 AND k.hash=$2 AND k.revoked_at IS NULL AND k.deleted_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>now()) AND a.status='active' AND w.status='active'
   ORDER BY k.created_at DESC LIMIT 1`, prefix, hash).Scan(&result.UserID, &result.AgentID, &result.CredentialID, &result.WorkspaceID, &ceiling, &grant, &policy, &result.PolicyVersion, &result.GrantRevision, &result.Legacy)
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return result, domain.ErrAccessDenied
		}
		if err != nil {
			return result, err
		}
		var c, g, p []string
		for _, pair := range []struct {
			raw []byte
			dst *[]string
		}{{ceiling, &c}, {grant, &g}, {policy, &p}} {
			if err = json.Unmarshal(pair.raw, pair.dst); err != nil {
				return domain.AccessContext{}, err
			}
		}
		result.Capabilities = domain.EffectiveCapabilities(c, g, p)
	} else {
		r.mu.Lock()
		defer r.mu.Unlock()
		found := false
		for _, k := range r.credentials {
			if k.Prefix != prefix || k.Hash != hash || k.RevokedAt != nil || k.ExpiresAt != nil && !k.ExpiresAt.After(time.Now()) {
				continue
			}
			a := r.agents[k.AgentID]
			w := r.workspaces[k.DefaultWorkspaceID]
			g, ok := r.grants[k.AgentID]
			if a.UserID != k.UserID || a.Status != "active" || w.UserID != k.UserID || w.Status != "active" || !ok || g.UserID != k.UserID || g.WorkspaceID != w.ID {
				continue
			}
			result = domain.AccessContext{UserID: k.UserID, AgentID: a.ID, CredentialID: k.ID, WorkspaceID: w.ID, Capabilities: domain.EffectiveCapabilities(k.Capabilities, g.Capabilities, w.Capabilities), PolicyVersion: w.PolicyVersion, GrantRevision: g.Revision, Legacy: a.Legacy}
			found = true
			break
		}
		if !found {
			return result, domain.ErrAccessDenied
		}
	}
	// Do not authenticate new scopes against the still user-scoped data plane.
	if result.WorkspaceID != domain.PersonalWorkspaceID(result.UserID) {
		return domain.AccessContext{}, domain.ErrAccessDenied
	}
	return result, nil
}

func cloneCredential(k domain.Credential) domain.Credential {
	k.Capabilities = append([]string{}, k.Capabilities...)
	if k.ExpiresAt != nil {
		value := *k.ExpiresAt
		k.ExpiresAt = &value
	}
	if k.RevokedAt != nil {
		value := *k.RevokedAt
		k.RevokedAt = &value
	}
	return k
}

// SetFallbackAudit connects development mutations to the same audit feed as SQL.
// Configure before serving requests.
func (r *IdentityRepository) SetFallbackAudit(record func(context.Context, domain.AuditEvent)) {
	r.fallbackAudit = record
}

func (r *IdentityRepository) GetAgent(ctx context.Context, user, id string) (domain.Agent, error) {
	if db := currentDB(r.db); db != nil {
		var a domain.Agent
		err := db.QueryRowContext(ctx, `SELECT id,user_id,actor_id,name,status,default_workspace_id,revision,legacy,created_at,updated_at FROM agents WHERE user_id=$1 AND id=$2`, user, id).Scan(&a.ID, &a.UserID, &a.ActorID, &a.Name, &a.Status, &a.DefaultWorkspaceID, &a.Revision, &a.Legacy, &a.CreatedAt, &a.UpdatedAt)
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return a, domain.ErrAccessNotFound
		}
		return a, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.agents[id]
	if !ok || a.UserID != user {
		return domain.Agent{}, domain.ErrAccessNotFound
	}
	return a, nil
}

func (r *IdentityRepository) ListAgentCredentials(ctx context.Context, user, agent, after string, limit int) ([]domain.Credential, error) {
	out := []domain.Credential{}
	if db := currentDB(r.db); db != nil {
		rows, err := db.QueryContext(ctx, `SELECT k.id,k.user_id,k.agent_id,k.default_workspace_id,k.name,k.prefix,k.capability_ceiling,k.expires_at,k.revoked_at,k.created_at,a.name FROM api_keys k JOIN agents a ON a.user_id=k.user_id AND a.id=k.agent_id WHERE k.user_id=$1 AND k.agent_id=$2 AND k.id>$3 AND k.deleted_at IS NULL ORDER BY k.id LIMIT $4`, user, agent, after, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var k domain.Credential
			var caps []byte
			if err = rows.Scan(&k.ID, &k.UserID, &k.AgentID, &k.DefaultWorkspaceID, &k.Name, &k.Prefix, &caps, &k.ExpiresAt, &k.RevokedAt, &k.CreatedAt, &k.AgentName); err != nil {
				return nil, err
			}
			if err = json.Unmarshal(caps, &k.Capabilities); err != nil {
				return nil, err
			}
			out = append(out, k)
		}
		return out, rows.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range r.credentials {
		if k.UserID != user || k.AgentID != agent || k.ID <= after {
			continue
		}
		k = cloneCredential(k)
		k.Hash = ""
		k.AgentName = r.agents[k.AgentID].Name
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func personalIdentity(user string) (domain.Workspace, domain.Agent, domain.WorkspaceGrant) {
	w := domain.Workspace{ID: domain.PersonalWorkspaceID(user), UserID: user, Name: "Personal", Kind: "personal", Status: "active", PolicyVersion: 1, Capabilities: domain.PersonalCapabilities()}
	now := time.Now().UTC()
	w.CreatedAt, w.UpdatedAt = now, now
	a := domain.Agent{ID: domain.LegacyAgentID(user), UserID: user, ActorID: domain.AccessID("legacy-actor", user), Name: "Legacy agent", Status: "active", DefaultWorkspaceID: w.ID, Revision: 1, Legacy: true, CreatedAt: now, UpdatedAt: now}
	g := domain.WorkspaceGrant{UserID: user, WorkspaceID: w.ID, AgentID: a.ID, Capabilities: domain.PersonalCapabilities(), Revision: 1}
	return w, a, g
}

func ensurePersonalTx(ctx context.Context, tx *sqlpkg.Tx, user string) (domain.Workspace, error) {
	w, a, g := personalIdentity(user)
	_, err := tx.ExecContext(ctx, `INSERT INTO workspaces(id,user_id,name,kind,status,policy_version,capabilities) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, w.ID, user, w.Name, w.Kind, w.Status, w.PolicyVersion, marshalJSON(w.Capabilities))
	if err != nil {
		return w, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO actors(id,user_id,kind,display_name) VALUES ($1,$2,'human','Owner'),($3,$2,'agent','Legacy agent') ON CONFLICT DO NOTHING`, domain.AccessID("owner", user), user, a.ActorID)
	if err != nil {
		return w, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agents(id,user_id,actor_id,name,default_workspace_id,legacy) VALUES ($1,$2,$3,$4,$5,true) ON CONFLICT DO NOTHING`, a.ID, user, a.ActorID, a.Name, w.ID)
	if err != nil {
		return w, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_grants(id,user_id,workspace_id,agent_id,capabilities) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, a.ID+":personal", user, w.ID, a.ID, marshalJSON(g.Capabilities))
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
