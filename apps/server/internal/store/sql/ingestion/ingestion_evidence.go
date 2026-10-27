package ingestion

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	scopestore "memstation/apps/server/internal/store/sql/internal/scope"
)

// Locks source revisions until the derived record commits. Retraction locks the
// same rows first, so either the write commits before invalidation or is rejected.
func BindIngestEvidenceTx(ctx context.Context, tx *sqlpkg.Tx, doc *domain.MemoryDocument) error {
	if doc.ConversationID == "" {
		return nil
	}
	var mode, partition, state string
	err := tx.QueryRowContext(ctx, `SELECT context_mode,partition_id,finalize_status FROM conversations WHERE user_id=$1 AND workspace_id=$2 AND id=$3`, doc.UserID, doc.WorkspaceID, doc.ConversationID).Scan(&mode, &partition, &state)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if mode != "events" {
		return nil
	}
	if !security.CanReadPartition(ctx, partition) || partition != doc.PartitionID || state == "invalidated" {
		return domain.ErrAccessConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.revision,w.source_revision,s.tombstoned,s.content_status,s.parts FROM ingestion_window_sources w JOIN source_messages s ON s.id=w.source_message_id WHERE w.user_id=$1 AND w.workspace_id=$2 AND w.conversation_id=$3 ORDER BY s.id FOR SHARE OF s`, doc.UserID, doc.WorkspaceID, doc.ConversationID)
	if err != nil {
		return err
	}
	type source struct {
		revision int64
		parts    []domain.SourcePart
	}
	sources := map[string]source{}
	for rows.Next() {
		var id, status string
		var revision, expected int64
		var deleted bool
		var raw []byte
		if err = rows.Scan(&id, &revision, &expected, &deleted, &status, &raw); err != nil {
			_ = rows.Close()
			return err
		}
		if deleted || status != "active" || revision != expected {
			_ = rows.Close()
			return domain.ErrAccessConflict
		}
		var parts []domain.SourcePart
		if err = json.Unmarshal(raw, &parts); err != nil {
			_ = rows.Close()
			return err
		}
		sources[id] = source{revision, parts}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return domain.ErrAccessConflict
	}
	if len(doc.SourceRefs) == 0 {
		if doc.Type != domain.MemoryTypeSummary && doc.Type != domain.MemoryTypeSegment {
			return errors.New("event-derived memory requires validated source references")
		}
		for id, s := range sources {
			for i := range s.parts {
				doc.SourceRefs = append(doc.SourceRefs, domain.SourceRef{WorkspaceID: doc.WorkspaceID, MessageID: id, Revision: s.revision, PartIndex: i})
			}
		}
	}
	for _, ref := range doc.SourceRefs {
		source, ok := sources[ref.MessageID]
		if !ok || source.revision != ref.Revision {
			return domain.ErrAccessConflict
		}
		if err = ValidateSourceRefTx(ctx, tx, doc.UserID, ref); err != nil {
			return err
		}
	}
	for _, actor := range doc.SubjectActorIDs {
		if err = scopestore.ValidateIngestActor(ctx, tx, doc.UserID, doc.WorkspaceID, actor); err != nil {
			return err
		}
	}
	return scopestore.PrepareDocumentAccess(ctx, doc)
}

func PersistIngestDependenciesTx(ctx context.Context, tx *sqlpkg.Tx, doc domain.MemoryDocument) error {
	for _, ref := range doc.SourceRefs {
		if err := ValidateSourceRefTx(ctx, tx, doc.UserID, ref); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO memory_source_dependencies(id,user_id,workspace_id,node_id,source_message_id,source_revision) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, dbutilstore.IngestionID("dependency", doc.NodeID, ref.MessageID, fmt.Sprint(ref.Revision)), doc.UserID, doc.WorkspaceID, doc.NodeID, ref.MessageID, ref.Revision)
		if err != nil {
			return err
		}
	}
	return nil
}
