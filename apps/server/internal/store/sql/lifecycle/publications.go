package lifecycle

import (
	"context"
	sqlpkg "database/sql"
	"errors"
	"fmt"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
	ingestionstore "memstation/apps/server/internal/store/sql/ingestion"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	scopestore "memstation/apps/server/internal/store/sql/internal/scope"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"github.com/google/uuid"
)

func (r *MemoryLifecycleRepository) Publish(ctx context.Context, input domain.PublicationInput) (domain.MemoryPublication, error) {
	var out domain.MemoryPublication
	if !security.AdminFromContext(ctx) {
		return out, domain.ErrAccessDenied
	}
	a, err := scopestore.LifecycleAccess(ctx)
	if err != nil {
		return out, err
	}
	db, err := r.database()
	if err != nil {
		return out, err
	}
	if input.TargetWorkspaceID == a.WorkspaceID {
		return out, domain.ErrAccessDenied
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer dbutilstore.Rollback(tx)
	var revision int64
	var isCopy bool
	err = tx.QueryRowContext(ctx, `SELECT n.revision,EXISTS(SELECT 1 FROM memory_publications p WHERE p.target_node_id=n.id) FROM memory_nodes n WHERE n.user_id=$1 AND n.id=$2 AND `+storeaccess.NodeSQL(ctx, "n")+` FOR SHARE`, a.UserID, input.SourceNodeID).Scan(&revision, &isCopy)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return out, domain.ErrAccessNotFound
	}
	if err != nil {
		return out, err
	}
	if isCopy || revision != input.SourceRevision {
		return out, domain.ErrAccessConflict
	}
	source, err := r.Inspect(ctx, input.SourceNodeID)
	if err != nil {
		return out, err
	}
	var target string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE user_id=$1 AND id=$2 AND status='active' FOR SHARE`, a.UserID, input.TargetWorkspaceID).Scan(&target); err != nil {
		return out, domain.ErrAccessNotFound
	}
	if input.TargetPartitionID != "" {
		var id string
		if err = tx.QueryRowContext(ctx, `SELECT id FROM memory_partitions WHERE user_id=$1 AND workspace_id=$2 AND id=$3 FOR SHARE`, a.UserID, target, input.TargetPartitionID).Scan(&id); err != nil {
			return out, domain.ErrAccessNotFound
		}
	}
	actors := []string{}
	for from, to := range input.ActorMapping {
		found := false
		for _, sourceActor := range source.SubjectActorIDs {
			if sourceActor == from {
				found = true
			}
		}
		if !found {
			return out, domain.ErrAccessDenied
		}
		if err = scopestore.ValidateIngestActor(ctx, tx, a.UserID, target, to); err != nil {
			return out, err
		}
		actors = append(actors, to)
	}
	// A partial mapping could misattribute a multi-actor assertion. Unmapped copies
	// belong to the target Workspace, never an implicitly shared person.
	if len(actors) != len(source.SubjectActorIDs) {
		actors = nil
	}
	bound, err := security.BindAdministrativeWorkspace(ctx, a.UserID, target, input.TargetPartitionID)
	if err != nil {
		return out, err
	}
	out = domain.MemoryPublication{ID: uuid.NewString(), SourceWorkspaceID: a.WorkspaceID, SourceNodeID: source.NodeID, SourceRevision: revision, TargetWorkspaceID: target, TargetPartitionID: input.TargetPartitionID, TargetNodeID: uuid.NewString(), Status: "active", Revision: 1}
	kind := source.Type
	if kind == domain.MemoryTypeKeyMemory || kind == domain.MemoryTypeSegment {
		kind = domain.MemoryTypeFact
	}
	doc := domain.MemoryDocument{ID: uuid.NewString(), NodeID: out.TargetNodeID, UserID: a.UserID, WorkspaceID: target, PartitionID: input.TargetPartitionID, Type: kind, Content: source.Content, SubjectActorIDs: actors, Metadata: map[string]any{"source": "publication", "publication_id": out.ID, "subject_scope": "workspace", "fact_key": "publication_" + out.ID, "support_kind": "approved_publication"}}
	if len(actors) > 0 {
		doc.Metadata["subject_scope"] = "actor"
	}
	if err = tx.QueryRowContext(bound, `SELECT generation_id FROM memory_generations WHERE user_id=$1 AND workspace_id=$2 AND is_active FOR SHARE`, a.UserID, target).Scan(&doc.GenerationID); err != nil {
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return out, domain.ErrAccessConflict
		}
		return out, err
	}
	if err = memorystore.NewPostgresMemoryRepository(db).StoreDocumentTx(bound, doc, tx); err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO memory_publications(id,user_id,source_workspace_id,source_node_id,source_revision,target_workspace_id,target_partition_id,target_node_id,actor_mapping) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, out.ID, a.UserID, out.SourceWorkspaceID, out.SourceNodeID, out.SourceRevision, out.TargetWorkspaceID, out.TargetPartitionID, out.TargetNodeID, dbutilstore.MarshalJSON(input.ActorMapping))
	if err != nil {
		return out, err
	}
	if err = dbutilstore.InsertMemoryMutationOutboxTx(bound, tx, domain.OutboxEvent{ID: "publication.embedding:" + out.ID, UserID: a.UserID, EventType: domain.TaskTypeEmbeddingGenerate, AggregateType: "memory_node", AggregateID: out.TargetNodeID, Payload: map[string]any{"node_id": out.TargetNodeID, "node_type": string(kind)}}); err != nil {
		return out, err
	}
	if input.ReplacesPublicationID != "" {
		var priorNode, priorWorkspace, priorPartition string
		var priorRevision int64
		e := tx.QueryRowContext(ctx, `SELECT target_node_id,target_workspace_id,target_partition_id,revision FROM memory_publications WHERE user_id=$1 AND source_workspace_id=$2 AND id=$3 AND status IN('active','paused') FOR UPDATE`, a.UserID, a.WorkspaceID, input.ReplacesPublicationID).Scan(&priorNode, &priorWorkspace, &priorPartition, &priorRevision)
		if e != nil {
			return out, domain.ErrAccessNotFound
		}
		if priorWorkspace != out.TargetWorkspaceID || priorPartition != out.TargetPartitionID || priorRevision != input.ExpectedPublicationRevision {
			return out, domain.ErrAccessConflict
		}
		op, e := r.retractPublicationCopyTx(ctx, tx, a, priorWorkspace, priorPartition, priorNode, input.ReplacesPublicationID, priorRevision)
		if e != nil {
			return out, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE memory_publications SET status='replaced',revision=revision+1,retraction_operation_id=$2,updated_at=now() WHERE id=$1`, input.ReplacesPublicationID, op); e != nil {
			return out, e
		}
	}
	if err = lifecycleAuditTx(ctx, tx, a, "publish_memory_copy", "memory_publication", out.ID); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (r *MemoryLifecycleRepository) Publications(ctx context.Context, after ...string) ([]domain.MemoryPublication, error) {
	if !security.AdminFromContext(ctx) {
		return nil, domain.ErrAccessDenied
	}
	a, err := scopestore.LifecycleAccess(ctx)
	if err != nil {
		return nil, err
	}
	db, err := r.database()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT p.id,p.source_workspace_id,p.source_node_id,p.source_revision,p.target_workspace_id,p.target_partition_id,p.target_node_id,CASE WHEN p.status='active' AND (n.access_status<>'active' OR n.revision<>p.source_revision) THEN 'paused' ELSE p.status END,p.revision,p.retraction_operation_id FROM memory_publications p JOIN memory_nodes n ON n.id=p.source_node_id WHERE p.user_id=$1 AND p.source_workspace_id=$2 AND p.id>$3 AND `+storeaccess.PartitionSQL(ctx, "n.partition_id")+` ORDER BY p.id LIMIT 100`, a.UserID, a.WorkspaceID, listAfter(after))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.MemoryPublication{}
	for rows.Next() {
		var p domain.MemoryPublication
		if err = rows.Scan(&p.ID, &p.SourceWorkspaceID, &p.SourceNodeID, &p.SourceRevision, &p.TargetWorkspaceID, &p.TargetPartitionID, &p.TargetNodeID, &p.Status, &p.Revision, &p.OperationID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *MemoryLifecycleRepository) RetractPublication(ctx context.Context, id string, revision int64) (domain.MemoryPublication, error) {
	var out domain.MemoryPublication
	if !security.AdminFromContext(ctx) {
		return out, domain.ErrAccessDenied
	}
	a, err := scopestore.LifecycleAccess(ctx)
	if err != nil {
		return out, err
	}
	db, err := r.database()
	if err != nil {
		return out, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer dbutilstore.Rollback(tx)
	err = tx.QueryRowContext(ctx, `UPDATE memory_publications p SET status='retracted',revision=p.revision+1,updated_at=now() WHERE p.user_id=$1 AND p.source_workspace_id=$2 AND p.id=$3 AND p.revision=$4 AND EXISTS(SELECT 1 FROM memory_nodes n WHERE n.id=p.source_node_id AND `+storeaccess.PartitionSQL(ctx, "n.partition_id")+`) RETURNING p.id,p.source_workspace_id,p.source_node_id,p.source_revision,p.target_workspace_id,p.target_partition_id,p.target_node_id,p.status,p.revision`, a.UserID, a.WorkspaceID, id, revision).Scan(&out.ID, &out.SourceWorkspaceID, &out.SourceNodeID, &out.SourceRevision, &out.TargetWorkspaceID, &out.TargetPartitionID, &out.TargetNodeID, &out.Status, &out.Revision)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return out, domain.ErrAccessConflict
	}
	if err != nil {
		return out, err
	}
	out.OperationID, err = r.retractPublicationCopyTx(ctx, tx, a, out.TargetWorkspaceID, out.TargetPartitionID, out.TargetNodeID, id, revision)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE memory_publications SET retraction_operation_id=$2 WHERE id=$1`, id, out.OperationID); err != nil {
		return out, err
	}
	if err = lifecycleAuditTx(ctx, tx, a, "retract_memory_publication", "memory_publication", id); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (r *MemoryLifecycleRepository) retractPublicationCopyTx(ctx context.Context, tx *sqlpkg.Tx, a domain.AccessContext, target, partition, node, publication string, revision int64) (string, error) {
	if r.journal == nil {
		return "", errors.New("publication retraction journal unavailable")
	}
	op := dbutilstore.IngestionID("retract-publication", publication, fmt.Sprint(revision))
	intent := domain.DeletionIntent{OperationID: op, UserID: a.UserID, WorkspaceID: target, PartitionID: partition, NodeIDs: []string{node}, SourceIDs: []string{}, BlobIDs: []string{}, ConversationIDs: []string{}, Selector: domain.ForgetSelector{PartitionID: partition, NodeIDs: []string{node}}, RequestHash: ingestionstore.IngestionHash([]any{publication, revision, node}), Cutoff: time.Now().UTC()}
	entry, err := r.journal.Append(ctx, intent)
	if err != nil {
		return "", err
	}
	bound, err := security.BindAdministrativeWorkspace(ctx, a.UserID, target, partition)
	if err != nil {
		return "", err
	}
	if err = ingestionstore.ApplyDeletionIntentTx(bound, tx, entry, "admin", r.windowBudget); err != nil {
		return "", err
	}
	return op, nil
}
