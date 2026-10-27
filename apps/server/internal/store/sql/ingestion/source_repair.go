package ingestion

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"fmt"

	"memstation/apps/server/internal/domain"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"

	"github.com/google/uuid"
)

func SourceDescendantsTx(ctx context.Context, tx *sqlpkg.Tx, user, workspace string, roots []string) ([]string, error) {
	if len(roots) == 0 {
		return []string{}, nil
	}
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE affected(id) AS (SELECT unnest($3::text[]) UNION SELECT l.source_message_id FROM source_links l JOIN affected a ON a.id=l.parent_message_id JOIN source_messages s ON s.id=l.source_message_id AND s.revision=l.source_revision WHERE l.user_id=$1 AND l.workspace_id=$2) SELECT id FROM affected ORDER BY id`, user, workspace, roots)
	if err != nil {
		return nil, err
	}
	return dbutilstore.ReadIDs(rows)
}

// Rebuild only surviving sources. A repaired window uses the same source identity
// and revision, and old invalidated artifacts never become readable in place.
func RepairInvalidatedWindowsTx(ctx context.Context, tx *sqlpkg.Tx, user, workspace, partition string, budget int) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM conversations WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 AND context_mode='events' AND finalize_status='invalidated' AND NOT COALESCE((metadata->>'repair_scheduled')::boolean,false) ORDER BY id FOR UPDATE`, user, workspace, partition)
	if err != nil {
		return err
	}
	oldWindows, err := dbutilstore.ReadIDs(rows)
	if err != nil {
		return err
	}
	for _, old := range oldWindows {
		rows, err = tx.QueryContext(ctx, `SELECT s.id,s.revision,s.speaker_actor_id,s.parts,s.reply_to,s.occurred_at,COALESCE(a.kind,'unknown') FROM ingestion_window_sources w JOIN source_messages s ON s.id=w.source_message_id LEFT JOIN actors a ON a.id=s.speaker_actor_id AND a.user_id=s.user_id WHERE w.conversation_id=$1 AND s.content_status='active' AND NOT s.tombstoned ORDER BY w.sequence_num FOR SHARE OF s`, old)
		if err != nil {
			return err
		}
		type source struct {
			id string
			e  domain.SourceEvent
		}
		sources := []source{}
		for rows.Next() {
			var s source
			var parts, reply []byte
			if err = rows.Scan(&s.id, &s.e.SourceRevision, &s.e.Speaker.ActorID, &parts, &reply, &s.e.OccurredAt, &s.e.Speaker.Kind); err != nil {
				_ = rows.Close()
				return err
			}
			if err = json.Unmarshal(parts, &s.e.Parts); err != nil {
				_ = rows.Close()
				return err
			}
			if err = json.Unmarshal(reply, &s.e.ReplyTo); err != nil {
				_ = rows.Close()
				return err
			}
			sources = append(sources, s)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		current := ""
		count, tokens := 0, 0
		for _, source := range sources {
			var existing string
			err = tx.QueryRowContext(ctx, `SELECT c.id FROM ingestion_window_sources w JOIN conversations c ON c.id=w.conversation_id WHERE w.source_message_id=$1 AND w.source_revision=$2 AND c.finalize_status<>'invalidated' ORDER BY c.created_at DESC LIMIT 1`, source.id, source.e.SourceRevision).Scan(&existing)
			if err != nil && err != sqlpkg.ErrNoRows {
				return err
			}
			if existing == "" {
				if current != "" && (count >= 100 || tokens+eventInputBudget(source.e) > budget) {
					if err = sealIngestWindowTx(ctx, tx, user, workspace, current, -1, "source_repair"); err != nil {
						return err
					}
					current = ""
				}
				if current == "" {
					current = uuid.NewString()
					count = 0
					tokens = 0
					_, err = tx.ExecContext(ctx, `INSERT INTO conversations(id,user_id,workspace_id,agent_id,partition_id,context_mode,request_format,mode,finalize_status,summary_status,analysis_status,metadata) SELECT $2,user_id,workspace_id,agent_id,partition_id,'events','events','memory','active','pending','pending',jsonb_build_object('memory_persist_enabled',true,'source_retain_enabled',true,'repair_of',$1::text,'message_count',0,'input_budget_units',0) FROM conversations WHERE id=$1`, old, current)
					if err != nil {
						return err
					}
				}
				if err = appendIngestMessageTx(ctx, tx, user, workspace, current, source.id, source.e); err != nil {
					return err
				}
				count++
				tokens += eventInputBudget(source.e)
				existing = current
			}
			if _, err = tx.ExecContext(ctx, `UPDATE source_events SET processing_conversation_id=$1 WHERE user_id=$2 AND workspace_id=$3 AND processing_conversation_id=$4 AND source_message_id=$5`, existing, user, workspace, old, source.id); err != nil {
				return err
			}
		}
		if current != "" {
			if err = sealIngestWindowTx(ctx, tx, user, workspace, current, -1, "source_repair"); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE source_events SET processing_conversation_id='' WHERE user_id=$1 AND workspace_id=$2 AND processing_conversation_id=$3`, user, workspace, old); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE conversations SET metadata=metadata||jsonb_build_object('repair_scheduled',true),updated_at=now() WHERE id=$1`, old); err != nil {
			return fmt.Errorf("record source repair: %w", err)
		}
	}
	return nil
}
