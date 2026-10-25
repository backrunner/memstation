package sql

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"

	"github.com/google/uuid"
)

type MemoryLifecycleRepository struct {
	windowBudget int
	db           runtimeconfig.DBHandle
	journal      ports.DeletionJournal
	blobs        ports.BlobStore
	clearCache   func(context.Context) error
}

func NewMemoryLifecycleRepository(db runtimeconfig.DBHandle) *MemoryLifecycleRepository {
	return &MemoryLifecycleRepository{db: db, windowBudget: 8192}
}

func (r *MemoryLifecycleRepository) SetWindowBudget(budget int) {
	if budget > 0 {
		r.windowBudget = budget
	}
}

func (r *MemoryLifecycleRepository) SetDeletionJournal(journal ports.DeletionJournal) {
	r.journal = journal
}

func (r *MemoryLifecycleRepository) SetCleanup(blobs ports.BlobStore, clear func(context.Context) error) {
	r.blobs = blobs
	r.clearCache = clear
}

func (r *MemoryLifecycleRepository) database() (*sqlpkg.DB, error) {
	db := currentDB(r.db)
	if db == nil {
		return nil, errors.New("durable memory database unavailable")
	}
	return db, nil
}

func lifecycleAccess(ctx context.Context) (domain.AccessContext, error) {
	a, ok := security.AccessFromContext(ctx)
	if !ok || a.UserID == "" || a.WorkspaceID == "" {
		return a, domain.ErrAccessDenied
	}
	if _, err := security.DataScope(ctx, a.UserID, a.WorkspaceID); err != nil {
		return a, err
	}
	return a, nil
}

func (r *MemoryLifecycleRepository) Inspect(ctx context.Context, id string) (domain.MemoryDocument, error) {
	a, err := lifecycleAccess(ctx)
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	db, err := r.database()
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	var generation string
	err = db.QueryRowContext(ctx, `SELECT generation_id FROM memory_nodes n WHERE n.id=$1 AND n.user_id=$2 AND `+storeaccess.NodeSQL(ctx, "n"), id, a.UserID).Scan(&generation)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return domain.MemoryDocument{}, domain.ErrAccessNotFound
	}
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	docs, err := NewPostgresMemoryRepository(db).ListDocumentsByNodeIDsInGeneration(ctx, a.UserID, generation, []string{id})
	if err != nil {
		return domain.MemoryDocument{}, err
	}
	if len(docs) != 1 {
		return domain.MemoryDocument{}, domain.ErrAccessNotFound
	}
	// The document SELECT hydrates these fields from the authoritative node in
	// the same snapshot as its body. Never replace them with an earlier lookup.
	return docs[0], nil
}

const candidateColumns = `id,workspace_id,partition_id,agent_id,kind,CASE WHEN status='invalidated' THEN '' ELSE content END,CASE WHEN status='invalidated' THEN '[]'::jsonb ELSE subject_actor_ids END,CASE WHEN status='invalidated' THEN '[]'::jsonb ELSE source_refs END,origin,status,revision,target_node_id,target_revision,result_node_id,created_at,updated_at`

func scanCandidate(row interface{ Scan(...any) error }) (domain.MemoryCandidate, error) {
	var c domain.MemoryCandidate
	var actors, refs []byte
	err := row.Scan(&c.ID, &c.WorkspaceID, &c.PartitionID, &c.AgentID, &c.Kind, &c.Content, &actors, &refs, &c.Origin, &c.Status, &c.Revision, &c.TargetNodeID, &c.TargetRevision, &c.ResultNodeID, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return c, domain.ErrAccessNotFound
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(actors, &c.SubjectActorIDs); err != nil {
		return c, err
	}
	return c, json.Unmarshal(refs, &c.SourceRefs)
}

func (r *MemoryLifecycleRepository) Propose(ctx context.Context, c domain.MemoryCandidate) (domain.MemoryCandidate, error) {
	a, err := lifecycleAccess(ctx)
	if err != nil {
		return c, err
	}
	db, err := r.database()
	if err != nil {
		return c, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer rollback(tx)
	if c.WorkspaceID != "" && c.WorkspaceID != a.WorkspaceID || !security.CanReadPartition(ctx, c.PartitionID) {
		return c, domain.ErrAccessDenied
	}
	bound, err := security.WithDataScope(ctx, a.UserID, a.WorkspaceID, c.PartitionID)
	if err != nil {
		return c, err
	}
	for _, actor := range c.SubjectActorIDs {
		if err = validateIngestActor(bound, tx, a.UserID, a.WorkspaceID, actor); err != nil {
			return c, err
		}
	}
	for _, ref := range c.SourceRefs {
		if err = validateSourceRefTx(bound, tx, a.UserID, ref); err != nil {
			return c, err
		}
	}
	if c.TargetNodeID != "" {
		var revision int64
		err = tx.QueryRowContext(ctx, `SELECT revision FROM memory_nodes n WHERE n.id=$1 AND n.user_id=$2 AND `+storeaccess.NodeSQL(bound, "n")+` FOR SHARE`, c.TargetNodeID, a.UserID).Scan(&revision)
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return c, domain.ErrAccessNotFound
		}
		if err != nil {
			return c, err
		}
		if revision != c.TargetRevision {
			return c, domain.ErrAccessConflict
		}
	}
	if c.SourceRefs == nil {
		c.SourceRefs = []domain.SourceRef{}
	}
	if c.SubjectActorIDs == nil {
		c.SubjectActorIDs = []string{}
	}
	c.ID = uuid.NewString()
	c.WorkspaceID = a.WorkspaceID
	c.AgentID = a.AgentID
	c.Status = "pending"
	c.Revision = 1
	c.ResultNodeID = ""
	c.CreatedAt = time.Now().UTC()
	c.UpdatedAt = c.CreatedAt
	_, err = tx.ExecContext(ctx, `INSERT INTO memory_candidates(id,user_id,workspace_id,partition_id,agent_id,kind,content,subject_actor_ids,source_refs,origin,target_node_id,target_revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, c.ID, a.UserID, c.WorkspaceID, c.PartitionID, c.AgentID, c.Kind, c.Content, marshalJSON(c.SubjectActorIDs), marshalJSON(c.SourceRefs), c.Origin, c.TargetNodeID, c.TargetRevision)
	if err != nil {
		return c, err
	}
	if err = lifecycleAuditTx(ctx, tx, a, "propose_memory", "memory_candidate", c.ID); err != nil {
		return c, err
	}
	return c, tx.Commit()
}

func (r *MemoryLifecycleRepository) Candidates(ctx context.Context, id string, after ...string) ([]domain.MemoryCandidate, error) {
	a, err := lifecycleAccess(ctx)
	if err != nil {
		return nil, err
	}
	db, err := r.database()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+candidateColumns+` FROM memory_candidates WHERE user_id=$1 AND workspace_id=$2 AND ($3='' OR id=$3) AND ($3<>'' OR id>$4) AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` ORDER BY id LIMIT 100`, a.UserID, a.WorkspaceID, id, listAfter(after))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.MemoryCandidate{}
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if id != "" && len(result) == 0 {
		return nil, domain.ErrAccessNotFound
	}
	return result, nil
}

func candidateMemoryType(kind string) domain.MemoryType {
	switch kind {
	case "summary":
		return domain.MemoryTypeSummary
	case "event":
		return domain.MemoryTypeEvent
	case "relationship", "preference", "group_habit", "fact":
		return domain.MemoryTypeFact
	default:
		return ""
	}
}

func (r *MemoryLifecycleRepository) Decide(ctx context.Context, d domain.CandidateDecision) (domain.MemoryCandidate, error) {
	if !security.AdminFromContext(ctx) {
		return domain.MemoryCandidate{}, domain.ErrAccessDenied
	}
	a, err := lifecycleAccess(ctx)
	if err != nil {
		return domain.MemoryCandidate{}, err
	}
	db, err := r.database()
	if err != nil {
		return domain.MemoryCandidate{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MemoryCandidate{}, err
	}
	defer rollback(tx)
	c, err := scanCandidate(tx.QueryRowContext(ctx, `SELECT `+candidateColumns+` FROM memory_candidates WHERE user_id=$1 AND workspace_id=$2 AND id=$3 AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` FOR UPDATE`, a.UserID, a.WorkspaceID, d.ID))
	if err != nil {
		return c, err
	}
	if c.Revision != d.ExpectedRevision || c.Status != "pending" {
		return c, domain.ErrAccessConflict
	}
	switch d.Decision {
	case "reject":
		c.Status = "rejected"
	case "approve":
		confirmed := c.Kind
		if c.Kind == "inference" {
			confirmed = d.ConfirmedKind
		} else if d.ConfirmedKind != "" && d.ConfirmedKind != c.Kind {
			return c, domain.ErrAccessConflict
		}
		kind := candidateMemoryType(confirmed)
		if kind == "" {
			return c, domain.ErrAccessConflict
		}
		bound, err := security.WithDataScope(ctx, a.UserID, a.WorkspaceID, c.PartitionID)
		if err != nil {
			return c, err
		}
		doc := domain.MemoryDocument{ID: uuid.NewString(), NodeID: uuid.NewString(), UserID: a.UserID, WorkspaceID: a.WorkspaceID, PartitionID: c.PartitionID, Content: c.Content, Type: kind, SubjectActorIDs: c.SubjectActorIDs, SourceRefs: c.SourceRefs, Metadata: map[string]any{"kind": confirmed, "proposed_kind": c.Kind, "source": "human_review", "candidate_id": c.ID, "support_kind": "human_attestation", "subject_scope": "workspace", "fact_key": "candidate_" + c.ID}}
		if len(doc.SubjectActorIDs) > 0 {
			doc.Metadata["subject_scope"] = "actor"
		}
		for _, actor := range c.SubjectActorIDs {
			if err = validateIngestActor(bound, tx, a.UserID, a.WorkspaceID, actor); err != nil {
				return c, err
			}
		}
		if c.TargetNodeID != "" {
			var revision int64
			var targetType string
			err = tx.QueryRowContext(ctx, `SELECT revision,node_type FROM memory_nodes n WHERE n.user_id=$1 AND n.id=$2 AND `+storeaccess.NodeSQL(bound, "n")+` FOR UPDATE`, a.UserID, c.TargetNodeID).Scan(&revision, &targetType)
			if errors.Is(err, sqlpkg.ErrNoRows) {
				return c, domain.ErrAccessNotFound
			}
			if err != nil {
				return c, err
			}
			if revision != c.TargetRevision || targetType != "fact" || kind != domain.MemoryTypeFact {
				return c, domain.ErrAccessConflict
			}
			prior, err := r.Inspect(bound, c.TargetNodeID)
			if err != nil {
				return c, err
			}
			doc.GenerationID = prior.GenerationID
			doc.Metadata["fact_key"] = prior.Metadata["fact_key"]
			doc.Metadata["subject"] = prior.Metadata["subject"]
			doc.Metadata["lineage_group_id"] = prior.Metadata["lineage_group_id"]
			doc.Metadata["supersedes_fact_ids"] = []string{prior.ID}
			if _, err = tx.ExecContext(ctx, `UPDATE memory_nodes SET revision=revision+1 WHERE id=$1`, c.TargetNodeID); err != nil {
				return c, err
			}
		}
		if err = NewPostgresMemoryRepository(db).storeDocumentTx(bound, doc, tx); err != nil {
			return c, err
		}
		if err = insertMemoryMutationOutboxTx(bound, tx, domain.OutboxEvent{ID: "candidate.embedding:" + doc.NodeID, UserID: a.UserID, EventType: domain.TaskTypeEmbeddingGenerate, AggregateType: "memory_node", AggregateID: doc.NodeID, Payload: map[string]any{"node_id": doc.NodeID, "node_type": string(doc.Type)}}); err != nil {
			return c, err
		}
		c.Status = "approved"
		c.ResultNodeID = doc.NodeID
	default:
		return c, domain.ErrAccessDenied
	}
	c.Revision++
	c.UpdatedAt = time.Now().UTC()
	_, err = tx.ExecContext(ctx, `UPDATE memory_candidates SET status=$2,result_node_id=$3,revision=$4,updated_at=$5 WHERE id=$1`, c.ID, c.Status, c.ResultNodeID, c.Revision, c.UpdatedAt)
	if err != nil {
		return c, err
	}
	if err = lifecycleAuditTx(ctx, tx, a, "review_memory_candidate", "memory_candidate", c.ID); err != nil {
		return c, err
	}
	return c, tx.Commit()
}

func lifecycleAuditTx(ctx context.Context, tx *sqlpkg.Tx, a domain.AccessContext, action, kind, id string) error {
	actorType := "agent"
	actorID := a.AgentID
	if security.AdminFromContext(ctx) {
		actorType = "admin"
		actorID = "admin"
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_type,actor_id,action,target_type,target_id,payload,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,now(),now())`, uuid.NewString(), actorType, actorID, action, kind, id, marshalJSON(map[string]any{"user_id": a.UserID, "workspace_id": a.WorkspaceID}))
	return err
}

func listAfter(values []string) string {
	if len(values) > 0 {
		return values[0]
	}
	return ""
}
