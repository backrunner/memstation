package ingestion

import (
	"context"
	"crypto/sha256"
	sqlpkg "database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
	ingestionservice "memstation/apps/server/internal/service/ingestion"
	storeaccess "memstation/apps/server/internal/store/access"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	scopestore "memstation/apps/server/internal/store/sql/internal/scope"

	"github.com/google/uuid"
)

type PostgresIngestionRepository struct {
	db      *sqlpkg.DB
	journal ports.DeletionJournal
}

var _ ports.IngestionRepository = (*PostgresIngestionRepository)(nil)

func NewPostgresIngestionRepository(db *sqlpkg.DB) *PostgresIngestionRepository {
	return &PostgresIngestionRepository{db: db}
}

func (r *PostgresIngestionRepository) SetDeletionJournal(j ports.DeletionJournal) { r.journal = j }

func IngestionHash(value any) string {
	sum := sha256.Sum256(dbutilstore.MarshalJSON(value))
	return hex.EncodeToString(sum[:])
}

// Source bindings, not caller-provided owner/actor identifiers, establish authority.
func ingestionAuthorityTx(ctx context.Context, tx *sqlpkg.Tx, b domain.ObserveBatch) (string, string, error) {
	a, ok := security.AccessFromContext(ctx)
	if !ok || a.WorkspaceID != b.WorkspaceID {
		return "", "", domain.ErrAccessDenied
	}
	var namespace string
	var allowed []byte
	err := tx.QueryRowContext(ctx, `SELECT i.identity_namespace_id,g.allowed_event_types FROM integrations i JOIN workspace_sources s ON s.user_id=i.user_id AND s.integration_id=i.id JOIN integration_agent_grants g ON g.user_id=s.user_id AND g.workspace_id=s.workspace_id AND g.integration_id=i.id JOIN workspaces w ON w.user_id=s.user_id AND w.id=s.workspace_id WHERE i.user_id=$1 AND i.id=$2 AND s.workspace_id=$3 AND s.external_space_id=$4 AND g.agent_id=$5 AND i.status='active' AND w.status='active' FOR SHARE OF i,s,g,w`, a.UserID, b.IntegrationID, b.WorkspaceID, b.ExternalSpaceID, a.AgentID).Scan(&namespace, &allowed)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return "", "", domain.ErrAccessDenied
	}
	if err != nil {
		return "", "", err
	}
	var types []string
	if err = json.Unmarshal(allowed, &types); err != nil {
		return "", "", err
	}
	for _, e := range b.Events {
		if !slices.Contains(types, e.Type) {
			return "", "", domain.ErrAccessDenied
		}
	}
	return a.UserID, namespace, nil
}

func (r *PostgresIngestionRepository) Observe(ctx context.Context, b domain.ObserveBatch, key string, budget int) (domain.IngestionReceipt, error) {
	var result domain.IngestionReceipt
	if err := ingestionservice.ValidateBatch(b, key); err != nil {
		return result, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer dbutilstore.Rollback(tx)
	user, namespace, err := ingestionAuthorityTx(ctx, tx, b)
	if err != nil {
		return result, err
	}
	if err = LockLifecycleScopeTx(ctx, tx, user, b.WorkspaceID, b.PartitionID); err != nil {
		return result, err
	}
	stream := dbutilstore.IngestionID("stream", user, b.WorkspaceID, b.IntegrationID, b.ExternalSpaceID, b.ThreadID, b.PartitionID)
	// Serialize all streams of an integration-space so shared message identities and
	// cross-thread duplicate deliveries cannot race into different windows.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, dbutilstore.IngestionID("ingest-lock", user, b.WorkspaceID, b.IntegrationID)); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO memory_streams(id,user_id,workspace_id,integration_id,external_space_id,thread_id,partition_id) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, stream, user, b.WorkspaceID, b.IntegrationID, b.ExternalSpaceID, b.ThreadID, b.PartitionID); err != nil {
		return result, err
	}
	var sequence int64
	var conversation string
	if err = tx.QueryRowContext(ctx, `SELECT accepted_sequence,current_conversation_id FROM memory_streams WHERE id=$1 FOR UPDATE`, stream).Scan(&sequence, &conversation); err != nil {
		return result, err
	}
	batchHash := IngestionHash(b)
	receiptID := dbutilstore.IngestionID("ingestion", stream, key)
	var existingHash string
	var acceptances []byte
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,events FROM ingestion_receipts WHERE id=$1`, receiptID).Scan(&existingHash, &acceptances)
	if err == nil {
		if existingHash != batchHash {
			return result, ingestionservice.ErrIdempotencyConflict
		}
		if err = json.Unmarshal(acceptances, &result.Events); err != nil {
			return result, err
		}
		for i := range result.Events {
			result.Events[i].Status = "duplicate"
		}
		if err = tx.Commit(); err != nil {
			return result, err
		}
		prior, err := r.Progress(ctx, receiptID)
		prior.Events = result.Events
		return prior, err
	}
	if !errors.Is(err, sqlpkg.ErrNoRows) {
		return result, err
	}
	first := sequence + 1
	deletedSources := []string{}
	deletionOperation := dbutilstore.IngestionID("source-retract", receiptID)
	a, _ := security.AccessFromContext(ctx)
	for _, original := range b.Events {
		event := original
		eventHash := IngestionHash(original)
		eventID := dbutilstore.IngestionID("event", user, b.WorkspaceID, b.IntegrationID, event.EventID)
		sourceID := dbutilstore.IngestionID("source", user, b.WorkspaceID, b.IntegrationID, event.SourceMessageID)
		var prevHash, prevSource, prevStream, prevWindow string
		var prevSeq int64
		err = tx.QueryRowContext(ctx, `SELECT payload_hash,source_message_id,stream_id,sequence_num,conversation_id FROM source_events WHERE id=$1`, eventID).Scan(&prevHash, &prevSource, &prevStream, &prevSeq, &prevWindow)
		if err == nil {
			if prevHash != eventHash || prevStream != stream {
				return result, ingestionservice.ErrIdempotencyConflict
			}
			result.Events = append(result.Events, domain.EventAcceptance{ConversationID: prevWindow, EventID: event.EventID, MessageID: prevSource, Status: "duplicate", Sequence: prevSeq})
			continue
		}
		if !errors.Is(err, sqlpkg.ErrNoRows) {
			return result, err
		}
		var previousRevision int64
		var tombstoned bool
		var oldPartition, oldHash string
		err = tx.QueryRowContext(ctx, `SELECT revision,tombstoned,partition_id,payload_hash FROM source_messages WHERE id=$1 FOR UPDATE`, sourceID).Scan(&previousRevision, &tombstoned, &oldPartition, &oldHash)
		if err != nil && !errors.Is(err, sqlpkg.ErrNoRows) {
			return result, err
		}
		exists := err == nil
		if exists && oldPartition != b.PartitionID {
			return result, domain.ErrAccessDenied
		}
		// Event ID is excluded from projection equality: multiple observers can report
		// the same revision with different delivery event IDs without new evidence.
		projection := original
		projection.EventID = ""
		projectionHash := IngestionHash(projection)
		deleted := event.Type == "message.deleted"
		if exists && previousRevision == event.SourceRevision && oldHash != projectionHash && !tombstoned && !deleted {
			return result, ingestionservice.ErrIdempotencyConflict
		}
		apply := !exists || event.SourceRevision > previousRevision || deleted && !tombstoned
		if tombstoned {
			apply = false
		}
		if apply && !deleted {
			blocked, e := sourceErasedTx(ctx, tx, user, b.WorkspaceID, b.PartitionID, sourceID, event, namespace)
			if e != nil {
				return result, e
			}
			if blocked {
				apply = false
				tombstoned = true
				if !exists {
					if _, e = tx.ExecContext(ctx, `INSERT INTO source_messages(id,user_id,workspace_id,integration_id,external_message_id,partition_id,stream_id,revision,speaker_actor_id,parts,reply_to,occurred_at,tombstoned,payload_hash,content_status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'','[]',NULL,$9,true,$10,'erased') ON CONFLICT DO NOTHING`, sourceID, user, b.WorkspaceID, b.IntegrationID, event.SourceMessageID, b.PartitionID, stream, event.SourceRevision, event.OccurredAt, projectionHash); e != nil {
						return result, e
					}
				}
			}
		}
		if deleted {
			deletedSources = append(deletedSources, sourceID)
		}
		if apply && !deleted {
			event.Speaker, err = resolveIngestSpeaker(ctx, tx, user, b.WorkspaceID, namespace, event.Speaker)
			if err != nil {
				return result, err
			}
			for i := range event.Parts {
				p := &event.Parts[i]
				if p.Author != nil {
					author, e := resolveIngestSpeaker(ctx, tx, user, b.WorkspaceID, namespace, *p.Author)
					if e != nil {
						return result, e
					}
					p.Author = &author
					if p.Type == "mention" {
						if p.ActorID != "" && p.ActorID != author.ActorID {
							return result, domain.ErrAccessDenied
						}
						p.ActorID = author.ActorID
					}
				}
				if p.ActorID != "" {
					if err = scopestore.ValidateIngestActor(ctx, tx, user, b.WorkspaceID, p.ActorID); err != nil {
						return result, err
					}
				}
				if p.Source != nil {
					if err = bindQuotedSourceTx(ctx, tx, user, p); err != nil {
						return result, err
					}
				}
			}
			if event.ReplyTo != nil {
				if err = ValidateSourceRefTx(ctx, tx, user, *event.ReplyTo); err != nil {
					return result, err
				}
			}
		}
		if apply {
			status := "active"
			if deleted {
				status = "retracted"
				event.Parts = []domain.SourcePart{}
				event.Speaker.ActorID = ""
				event.ReplyTo = nil
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO source_messages(id,user_id,workspace_id,integration_id,external_message_id,partition_id,stream_id,revision,speaker_actor_id,parts,reply_to,occurred_at,tombstoned,payload_hash,content_status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) ON CONFLICT(id) DO UPDATE SET revision=GREATEST(source_messages.revision,EXCLUDED.revision),speaker_actor_id=EXCLUDED.speaker_actor_id,parts=EXCLUDED.parts,reply_to=EXCLUDED.reply_to,occurred_at=EXCLUDED.occurred_at,tombstoned=EXCLUDED.tombstoned,payload_hash=EXCLUDED.payload_hash,content_status=EXCLUDED.content_status,updated_at=now()`, sourceID, user, b.WorkspaceID, b.IntegrationID, event.SourceMessageID, b.PartitionID, stream, event.SourceRevision, event.Speaker.ActorID, dbutilstore.MarshalJSON(event.Parts), dbutilstore.MarshalJSON(event.ReplyTo), event.OccurredAt, deleted, projectionHash, status)
			if err != nil {
				return result, err
			}
			if !deleted {
				for _, part := range event.Parts {
					if part.Source != nil {
						ref := part.Source
						if ref.MessageID == sourceID {
							return result, ingestionservice.ErrInvalidEvent
						}
						if _, err = tx.ExecContext(ctx, `INSERT INTO source_links(id,user_id,workspace_id,source_message_id,source_revision,parent_message_id,parent_revision) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, dbutilstore.IngestionID("source-link", sourceID, fmt.Sprint(event.SourceRevision), ref.MessageID, fmt.Sprint(ref.Revision)), user, b.WorkspaceID, sourceID, event.SourceRevision, ref.MessageID, ref.Revision); err != nil {
							return result, err
						}
					}
				}
			}
			if exists || deleted {
				if err = invalidateIngestSourceTx(ctx, tx, user, b.WorkspaceID, sourceID); err != nil {
					return result, err
				}
			}
		}
		sequence++
		window := ""
		if !apply && !tombstoned {
			err = tx.QueryRowContext(ctx, `SELECT conversation_id FROM ingestion_window_sources WHERE source_message_id=$1 AND source_revision=$2 ORDER BY created_at DESC LIMIT 1`, sourceID, event.SourceRevision).Scan(&window)
			if err != nil && !errors.Is(err, sqlpkg.ErrNoRows) {
				return result, err
			}
		}
		if apply && !deleted {
			if conversation != "" {
				var count, tokens int
				var state string
				err = tx.QueryRowContext(ctx, `SELECT finalize_status,COALESCE((metadata->>'message_count')::int,0),COALESCE((metadata->>'input_budget_units')::int,0) FROM conversations WHERE id=$1 FOR UPDATE`, conversation).Scan(&state, &count, &tokens)
				if err != nil {
					return result, err
				}
				if state != "active" {
					conversation = ""
				} else if count >= 100 || tokens+eventInputBudget(event) > budget {
					if err = sealIngestWindowTx(ctx, tx, user, b.WorkspaceID, conversation, -1, "threshold"); err != nil {
						return result, err
					}
					conversation = ""
				}
			}
			if conversation == "" {
				conversation = uuid.NewString()
				metadata := map[string]any{"memory_persist_enabled": true, "source_retain_enabled": true, "stream_id": stream, "message_count": 0, "input_budget_units": 0, "partition_id": b.PartitionID}
				_, err = tx.ExecContext(ctx, `INSERT INTO conversations(id,user_id,workspace_id,agent_id,partition_id,context_mode,request_format,mode,finalize_status,summary_status,analysis_status,metadata) VALUES($1,$2,$3,$4,$5,'events','events','memory','active','pending','pending',$6)`, conversation, user, b.WorkspaceID, a.AgentID, b.PartitionID, dbutilstore.MarshalJSON(metadata))
				if err != nil {
					return result, err
				}
			}
			if err = appendIngestMessageTx(ctx, tx, user, b.WorkspaceID, conversation, sourceID, event); err != nil {
				return result, err
			}
			window = conversation
		}
		// Retain identity and revision in the event journal; body lives in revocable
		// source storage and its bounded conversation projection, not another copy.
		_, err = tx.ExecContext(ctx, `INSERT INTO source_events(id,user_id,workspace_id,integration_id,external_event_id,source_message_id,source_revision,stream_id,sequence_num,event_type,payload_hash,payload,conversation_id,processing_conversation_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'{}',$12,$12)`, eventID, user, b.WorkspaceID, b.IntegrationID, event.EventID, sourceID, event.SourceRevision, stream, sequence, event.Type, eventHash, window)
		if err != nil {
			return result, err
		}
		result.Events = append(result.Events, domain.EventAcceptance{ConversationID: window, EventID: event.EventID, MessageID: sourceID, Status: "accepted", Sequence: sequence})
	}
	if conversation != "" {
		var ready bool
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE((metadata->>'message_count')::int,0)>=100 OR COALESCE((metadata->>'input_budget_units')::int,0)>=$2 FROM conversations WHERE id=$1`, conversation, budget).Scan(&ready); err != nil {
			return result, err
		}
		if ready {
			if err = sealIngestWindowTx(ctx, tx, user, b.WorkspaceID, conversation, -1, "threshold"); err != nil {
				return result, err
			}
			conversation = ""
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE memory_streams SET accepted_sequence=$2,current_conversation_id=$3,updated_at=now() WHERE id=$1`, stream, sequence, conversation); err != nil {
		return result, err
	}
	if first > sequence {
		first = sequence
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ingestion_receipts(id,user_id,workspace_id,partition_id,stream_id,idempotency_key,payload_hash,first_sequence,last_sequence,events) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, receiptID, user, b.WorkspaceID, b.PartitionID, stream, key, batchHash, first, sequence, dbutilstore.MarshalJSON(result.Events))
	if err != nil {
		return result, err
	}
	if len(deletedSources) > 0 {
		if r.journal == nil {
			return result, errors.New("deletion journal unavailable")
		}
		intent, e := sourceDeletionIntentTx(ctx, tx, user, b.WorkspaceID, b.PartitionID, deletionOperation, deletedSources)
		if e != nil {
			return result, e
		}
		intent.RequestHash = batchHash
		entry, e := r.journal.Append(ctx, intent)
		if e != nil {
			return result, e
		}
		if e = ApplyDeletionIntentTx(ctx, tx, entry, a.AgentID, budget); e != nil {
			return result, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE source_events SET operation_id=$1 WHERE stream_id=$2 AND sequence_num>=$3 AND sequence_num<=$4 AND event_type='message.deleted'`, deletionOperation, stream, first, sequence); e != nil {
			return result, e
		}
	}
	if err = RepairInvalidatedWindowsTx(ctx, tx, user, b.WorkspaceID, b.PartitionID, budget); err != nil {
		return result, err
	}
	// Every accepted batch has a durable processing trigger, including batches
	// whose window is still open. The trigger contains references only.
	if _, err = tx.ExecContext(ctx, `INSERT INTO outbox_events(id,user_id,event_type,aggregate_type,aggregate_id,payload,status,available_at) VALUES($1,$2,'ingestion.accepted','ingestion_receipt',$3,$4,'pending',now()) ON CONFLICT DO NOTHING`, "ingestion.accepted:"+receiptID, user, receiptID, dbutilstore.MarshalJSON(map[string]any{"schema_version": 2, "user_id": user, "workspace_id": b.WorkspaceID, "partition_id": b.PartitionID, "receipt_id": receiptID})); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return r.Progress(ctx, receiptID)
}

// A UTF-8 byte upper bound including structural identity/evidence fields keeps
// windows conservative across configured model tokenizers. Individual oversized
// sources are retained once and segmented by the existing LLM input builder.
func eventInputBudget(e domain.SourceEvent) int { return len(dbutilstore.MarshalJSON(e)) + 64 }

func resolveIngestSpeaker(ctx context.Context, tx *sqlpkg.Tx, user, workspace, namespace string, s domain.SourceSpeaker) (domain.SourceSpeaker, error) {
	if s.Kind == "unknown" {
		s.ActorID = ""
		return s, nil
	}
	if s.ExternalID == "" || s.Kind != "human" && s.Kind != "bot" {
		return s, ingestionservice.ErrInvalidEvent
	}
	actor := dbutilstore.IngestionID("actor", user, namespace, s.ExternalID)
	if s.ActorID != "" && s.ActorID != actor {
		return s, domain.ErrAccessDenied
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO actors(id,user_id,kind,display_name) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, actor, user, s.Kind, "")
	if err != nil {
		return s, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO actor_identities(id,user_id,identity_namespace_id,external_actor_id,actor_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, dbutilstore.IngestionID("identity", user, namespace, s.ExternalID), user, namespace, s.ExternalID, actor)
	if err != nil {
		return s, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_members(id,user_id,workspace_id,actor_id,display_name) VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id,workspace_id,actor_id) DO UPDATE SET display_name=EXCLUDED.display_name,revision=workspace_members.revision+1,updated_at=now()`, dbutilstore.IngestionID("member", workspace, actor), user, workspace, actor, s.DisplayName)
	s.ActorID = actor
	return s, err
}

func ValidateSourceRefTx(ctx context.Context, tx *sqlpkg.Tx, user string, ref domain.SourceRef) error {
	if ref.WorkspaceID != security.WorkspaceID(ctx, user) {
		return domain.ErrAccessDenied
	}
	var parts []byte
	err := tx.QueryRowContext(ctx, `SELECT parts FROM source_messages WHERE user_id=$1 AND workspace_id=$2 AND id=$3 AND revision=$4 AND NOT tombstoned AND content_status='active' AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` FOR SHARE`, user, ref.WorkspaceID, ref.MessageID, ref.Revision).Scan(&parts)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return domain.ErrAccessNotFound
	}
	if err != nil {
		return err
	}
	var decoded []domain.SourcePart
	if err = json.Unmarshal(parts, &decoded); err != nil {
		return err
	}
	if ref.PartIndex < 0 || ref.PartIndex >= len(decoded) {
		return ingestionservice.ErrInvalidEvent
	}
	if ref.Span != nil && (ref.Span.Start < 0 || ref.Span.End <= ref.Span.Start || ref.Span.End > utf8.RuneCountInString(decoded[ref.PartIndex].Text)) {
		return ingestionservice.ErrInvalidEvent
	}
	return nil
}

func appendIngestMessageTx(ctx context.Context, tx *sqlpkg.Tx, user, workspace, conversation, source string, event domain.SourceEvent) error {
	var seq int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE conversation_id=$1`, conversation).Scan(&seq); err != nil {
		return err
	}
	var texts []string
	blocks := []domain.ContentBlock{}
	for _, p := range event.Parts {
		if p.Type == "media" && p.Media != nil {
			blocks = append(blocks, *p.Media)
			texts = append(texts, "["+string(p.Media.Type)+" attachment]")
		} else {
			blocks = append(blocks, domain.ContentBlock{Type: domain.ContentBlockText, Text: p.Text})
		}
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	provenance := map[string]any{"source_message_id": source, "source_revision": event.SourceRevision, "speaker": event.Speaker, "parts": event.Parts, "reply_to": event.ReplyTo, "occurred_at": event.OccurredAt}
	role := "user"
	if event.Speaker.Kind == "bot" {
		role = "assistant"
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO messages(id,user_id,workspace_id,conversation_id,role,text_content,content_blocks,tool_calls,tool_result,provider_payload,sequence_num) VALUES($1,$2,$3,$4,$5,$6,$9,'[]','{}',$7,$8)`, dbutilstore.IngestionID("message", conversation, source, fmt.Sprint(event.SourceRevision)), user, workspace, conversation, role, strings.Join(texts, "\n"), dbutilstore.MarshalJSON(provenance), seq, dbutilstore.MarshalJSON(blocks))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ingestion_window_sources(id,user_id,workspace_id,conversation_id,source_message_id,source_revision,sequence_num) VALUES($1,$2,$3,$4,$5,$6,$7)`, dbutilstore.IngestionID("window-source", conversation, source), user, workspace, conversation, source, event.SourceRevision, seq)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE conversations SET write_revision=write_revision+1,metadata=metadata||jsonb_build_object('message_count',$2::int,'input_budget_units',COALESCE((metadata->>'input_budget_units')::int,0)+$3::int),updated_at=now() WHERE id=$1`, conversation, seq+1, eventInputBudget(event))
	return err
}

// Authority is invalidated in the same transaction as the source edit/delete.
// Stale artifacts remain quarantined until validated against live revisions.
func invalidateIngestSourceTx(ctx context.Context, tx *sqlpkg.Tx, user, workspace, source string) error {
	affected, err := SourceDescendantsTx(ctx, tx, user, workspace, []string{source})
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE source_messages SET content_status='quarantined',updated_at=now() WHERE user_id=$1 AND workspace_id=$2 AND id=ANY($3::text[]) AND id<>$4 AND NOT tombstoned`, user, workspace, affected, source); err != nil {
		return err
	}
	for _, id := range affected {
		if err = invalidateSourceArtifactsTx(ctx, tx, user, workspace, id); err != nil {
			return err
		}
	}
	return nil
}

func invalidateSourceArtifactsTx(ctx context.Context, tx *sqlpkg.Tx, user, workspace, source string) error {
	_, err := tx.ExecContext(ctx, `UPDATE memory_nodes SET access_status='quarantined',revision=revision+1,updated_at=now() WHERE user_id=$1 AND workspace_id=$2 AND access_status<>'erased' AND (id IN(SELECT node_id FROM memory_source_dependencies WHERE user_id=$1 AND workspace_id=$2 AND source_message_id=$3) OR source_conversation_id IN(SELECT conversation_id FROM ingestion_window_sources WHERE source_message_id=$3))`, user, workspace, source)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE conversations SET finalize_status='invalidated',summary_status='failed',analysis_status='failed',updated_at=now() WHERE user_id=$1 AND workspace_id=$2 AND id IN(SELECT conversation_id FROM ingestion_window_sources WHERE source_message_id=$3)`, user, workspace, source)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE messages SET text_content='',content_blocks='[]',provider_payload='{}',updated_at=now() WHERE user_id=$1 AND workspace_id=$2 AND conversation_id IN(SELECT conversation_id FROM ingestion_window_sources WHERE source_message_id=$3)`, user, workspace, source)
	if err != nil {
		return err
	}
	for _, q := range []string{
		`UPDATE memory_operations o SET result='{}',status='invalidated',updated_at=now() WHERE o.user_id=$1 AND o.kind='export' AND ((o.workspace_id=$2 AND o.partition_id IN(SELECT partition_id FROM source_messages WHERE id=$3)) OR EXISTS(SELECT 1 FROM memory_publications p WHERE p.user_id=o.user_id AND p.target_workspace_id=o.workspace_id AND p.target_partition_id=o.partition_id AND p.source_node_id IN(SELECT node_id FROM memory_source_dependencies WHERE source_message_id=$3)))`,
		`UPDATE memory_source_snapshots SET content_status='quarantined' WHERE user_id=$1 AND workspace_id=$2 AND conversation_id IN(SELECT conversation_id FROM ingestion_window_sources WHERE source_message_id=$3)`,
		`UPDATE memory_candidates SET status='invalidated',content='',source_refs='[]',revision=revision+1,updated_at=now() WHERE user_id=$1 AND workspace_id=$2 AND EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(source_refs,'null'::jsonb),'[]'::jsonb)) ref WHERE ref->>'message_id'=$3)`,
		`UPDATE memory_publications SET status='paused',revision=revision+1,updated_at=now() WHERE user_id=$1 AND source_workspace_id=$2 AND source_node_id IN(SELECT id FROM memory_nodes WHERE access_status<>'active' AND id IN(SELECT node_id FROM memory_source_dependencies WHERE source_message_id=$3))`,
	} {
		if _, err = tx.ExecContext(ctx, q, user, workspace, source); err != nil {
			return err
		}
	}
	return nil
}

func sealIngestWindowTx(ctx context.Context, tx *sqlpkg.Tx, user, workspace, id string, revision int64, reason string) error {
	var rev int64
	var state, partition, agent string
	err := tx.QueryRowContext(ctx, `SELECT write_revision,finalize_status,partition_id,agent_id FROM conversations WHERE user_id=$1 AND workspace_id=$2 AND id=$3 AND context_mode='events' AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` FOR UPDATE`, user, workspace, id).Scan(&rev, &state, &partition, &agent)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return domain.ErrAccessNotFound
	}
	if err != nil {
		return err
	}
	if revision >= 0 && revision != rev {
		return domain.ErrAccessConflict
	}
	if state != "active" {
		return nil
	}
	var empty bool
	if err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM ingestion_window_sources WHERE conversation_id=$1)`, id).Scan(&empty); err != nil {
		return err
	}
	if empty {
		_, err = tx.ExecContext(ctx, `UPDATE conversations SET finalize_status='ended',summary_status='completed',analysis_status='completed',metadata=metadata||jsonb_build_object('finalize_reason',$2::text,'empty_window',true),updated_at=now() WHERE id=$1`, id, reason)
		return err
	}
	payload := map[string]any{"schema_version": 2, "user_id": user, "workspace_id": workspace, "partition_id": partition, "agent_id": agent, "conversation_id": id, "conversation_revision": rev, "finalize_reason": reason, "finalize_status": "ended", "memory_persist_enabled": true, "source_retain_enabled": true}
	_, err = tx.ExecContext(ctx, `INSERT INTO outbox_events(id,user_id,event_type,aggregate_type,aggregate_id,payload,status,available_at) VALUES($1,$2,'conversation.finalize','conversation',$3,$4,'pending',now()) ON CONFLICT DO NOTHING`, "conversation.finalize:"+id, user, id, dbutilstore.MarshalJSON(payload))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE conversations SET finalize_status='ending',metadata=metadata||jsonb_build_object('finalize_reason',$2::text,'finalize_target','ended'),updated_at=now() WHERE id=$1`, id, reason)
	return err
}

func (r *PostgresIngestionRepository) FinalizeWindow(ctx context.Context, id string, revision int64) error {
	a, ok := security.AccessFromContext(ctx)
	if !ok {
		return domain.ErrAccessDenied
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbutilstore.Rollback(tx)
	if err = sealIngestWindowTx(ctx, tx, a.UserID, a.WorkspaceID, id, revision, "explicit"); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *PostgresIngestionRepository) FinalizeIdle(ctx context.Context, before time.Time) error {
	rows, err := r.db.QueryContext(ctx, `SELECT id,user_id,workspace_id,partition_id FROM conversations WHERE context_mode='events' AND finalize_status='active' AND updated_at<$1 ORDER BY updated_at LIMIT 100`, before)
	if err != nil {
		return err
	}
	type window struct{ id, user, workspace, partition string }
	var windows []window
	for rows.Next() {
		var w window
		if err = rows.Scan(&w.id, &w.user, &w.workspace, &w.partition); err != nil {
			_ = rows.Close()
			return err
		}
		windows = append(windows, w)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, w := range windows {
		bound, err := security.WithDataScope(ctx, w.user, w.workspace, w.partition)
		if err != nil {
			return err
		}
		tx, err := r.db.BeginTx(bound, nil)
		if err != nil {
			return err
		}
		var stale bool
		err = tx.QueryRowContext(bound, `SELECT updated_at<$2 FROM conversations WHERE id=$1 FOR UPDATE`, w.id, before).Scan(&stale)
		if err == nil && stale {
			err = sealIngestWindowTx(bound, tx, w.user, w.workspace, w.id, -1, "idle")
		}
		if err != nil {
			dbutilstore.Rollback(tx)
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (r *PostgresIngestionRepository) Source(ctx context.Context, id string) (domain.SourceRecord, error) {
	var out domain.SourceRecord
	a, ok := security.AccessFromContext(ctx)
	if !ok {
		return out, domain.ErrAccessDenied
	}
	var parts, reply []byte
	err := r.db.QueryRowContext(ctx, `SELECT id,workspace_id,partition_id,integration_id,revision,speaker_actor_id,parts,reply_to,occurred_at FROM source_messages WHERE user_id=$1 AND workspace_id=$2 AND id=$3 AND NOT tombstoned AND content_status='active' AND `+storeaccess.PartitionSQL(ctx, "partition_id"), a.UserID, a.WorkspaceID, id).Scan(&out.ID, &out.WorkspaceID, &out.PartitionID, &out.IntegrationID, &out.Revision, &out.SpeakerActorID, &parts, &reply, &out.OccurredAt)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return out, domain.ErrAccessNotFound
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(parts, &out.Parts); err != nil {
		return out, err
	}
	if len(reply) > 0 {
		err = json.Unmarshal(reply, &out.ReplyTo)
	}
	return out, err
}

func bindQuotedSourceTx(ctx context.Context, tx *sqlpkg.Tx, user string, p *domain.SourcePart) error {
	if p.Source == nil || p.Type != "quote" {
		return ingestionservice.ErrInvalidEvent
	}
	ref := *p.Source
	if err := ValidateSourceRefTx(ctx, tx, user, ref); err != nil {
		return err
	}
	var raw []byte
	var speaker, kind string
	err := tx.QueryRowContext(ctx, `SELECT s.parts,s.speaker_actor_id,COALESCE(a.kind,'unknown') FROM source_messages s LEFT JOIN actors a ON a.user_id=s.user_id AND a.id=s.speaker_actor_id WHERE s.user_id=$1 AND s.workspace_id=$2 AND s.id=$3`, user, ref.WorkspaceID, ref.MessageID).Scan(&raw, &speaker, &kind)
	if err != nil {
		return err
	}
	var parts []domain.SourcePart
	if err = json.Unmarshal(raw, &parts); err != nil {
		return err
	}
	original := parts[ref.PartIndex]
	text := original.Text
	if ref.Span != nil {
		text = string([]rune(text)[ref.Span.Start:ref.Span.End])
	}
	if p.Text != text {
		return ingestionservice.ErrInvalidEvent
	}
	author := domain.SourceSpeaker{ActorID: speaker, Kind: kind}
	if original.Type == "quote" {
		author = domain.SourceSpeaker{Kind: "unknown"}
		if original.Author != nil {
			author = *original.Author
		}
	}
	if p.Author != nil && p.Author.ActorID != "" && p.Author.ActorID != author.ActorID {
		return domain.ErrAccessDenied
	}
	p.Author = &author
	return nil
}
