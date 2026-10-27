package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	recordutilstore "memstation/apps/server/internal/store/sql/internal/recordutil"
)

func normalizeSourceWindow(in domain.SourceWindowInput) (domain.SourceWindowInput, error) {
	if in.ConversationID == "" || in.Radius < 0 || in.Radius > 2 || len(in.Sequences) > 32 || len(in.Refs) > 32 {
		return in, domain.ErrAccessConflict
	}
	if in.Limit <= 0 {
		in.Limit = 8
	}
	in.Limit = min(in.Limit, 32)
	if in.MaxBytes <= 0 {
		in.MaxBytes = 8192
	}
	in.MaxBytes = min(in.MaxBytes, 65536)
	for _, seq := range in.Sequences {
		if seq < 0 || seq > 1_000_000 {
			return in, domain.ErrAccessConflict
		}
	}
	return in, nil
}

func (r *HybridConversationRepository) ReadSourceWindow(ctx context.Context, in domain.SourceWindowInput) ([]domain.SourceTurn, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresConversationRepository(db).ReadSourceWindow(ctx, in)
	}
	return r.fallback.ReadSourceWindow(ctx, in)
}

func (r *InMemoryConversationRepository) ReadSourceWindow(ctx context.Context, in domain.SourceWindowInput) ([]domain.SourceTurn, error) {
	if _, authenticated := security.AccessFromContext(ctx); authenticated {
		if err := security.RequireCapability(ctx, domain.CapabilitySourcesRead); err != nil {
			return nil, err
		}
	}
	var err error
	in, err = normalizeSourceWindow(in)
	if err != nil {
		return nil, err
	}
	w, err := security.DataScope(ctx, in.UserID, in.WorkspaceID)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.conversations[recordutilstore.Key(in.UserID, in.ConversationID)]
	if !ok || c.WorkspaceID != w || !security.CanReadPartition(ctx, c.PartitionID) || c.FinalizeStatus == "invalidated" {
		return nil, domain.ErrConversationNotFound
	}
	anchors := append([]int(nil), in.Sequences...)
	if len(in.Refs) > 0 {
		anchors = nil
		for _, ref := range in.Refs {
			found := false
			for seq, m := range c.Messages {
				if m.Source == nil || m.Source.MessageID != ref.MessageID {
					continue
				}
				if ref.WorkspaceID != w || ref.Revision != m.Source.Revision || ref.PartIndex < 0 || ref.PartIndex >= len(m.Source.Parts) {
					return nil, domain.ErrAccessConflict
				}
				anchors = append(anchors, seq)
				found = true
				break
			}
			if !found {
				return nil, domain.ErrAccessConflict
			}
		}
	} else if c.ContextMode == "events" || in.ConversationRevision <= 0 || c.Revision != in.ConversationRevision {
		return nil, domain.ErrAccessConflict
	}
	var turns []domain.SourceTurn
	for seq, m := range c.Messages {
		if !nearSourceAnchor(seq, anchors, in.Radius) {
			continue
		}
		t := domain.SourceTurn{UserID: in.UserID, WorkspaceID: w, ConversationID: in.ConversationID, Sequence: seq, Role: m.Role, Text: m.Text, SourceDate: dbutilstore.StringValue(c.Metadata, "source_session_time"), ExternalRef: dbutilstore.StringValue(m.Metadata, "ref"), Speaker: dbutilstore.StringValue(m.Metadata, "speaker")}
		if m.Source != nil {
			t.OccurredAt = m.Source.OccurredAt
			t.Speaker = m.Source.Speaker.ActorID
			t.Parts = m.Source.Parts
			for i := range m.Source.Parts {
				t.Refs = append(t.Refs, domain.SourceRef{WorkspaceID: w, MessageID: m.Source.MessageID, Revision: m.Source.Revision, PartIndex: i})
			}
		}
		turns = append(turns, t)
	}
	return boundSourceTurns(turns, in), nil
}

func nearSourceAnchor(seq int, anchors []int, radius int) bool {
	for _, a := range anchors {
		if seq >= a-radius && seq <= a+radius {
			return true
		}
	}
	return false
}

func boundSourceTurns(turns []domain.SourceTurn, in domain.SourceWindowInput) []domain.SourceTurn {
	sort.Slice(turns, func(i, j int) bool { return turns[i].Sequence < turns[j].Sequence })
	out := []domain.SourceTurn{}
	bytes := 0
	for _, t := range turns {
		if strings.TrimSpace(t.Text) == "" || bytes+len(t.Text) > in.MaxBytes {
			continue
		}
		out = append(out, t)
		bytes += len(t.Text)
		if len(out) >= in.Limit {
			break
		}
	}
	return out
}

func (r *PostgresConversationRepository) ReadSourceWindow(ctx context.Context, in domain.SourceWindowInput) ([]domain.SourceTurn, error) {
	if _, authenticated := security.AccessFromContext(ctx); authenticated {
		if err := security.RequireCapability(ctx, domain.CapabilitySourcesRead); err != nil {
			return nil, err
		}
	}
	var err error
	in, err = normalizeSourceWindow(in)
	if err != nil {
		return nil, err
	}
	w, err := security.DataScope(ctx, in.UserID, in.WorkspaceID)
	if err != nil {
		return nil, err
	}
	// Both branches join the live parent and enforce its access boundary in the
	// same statement as the content read, rather than trusting cached metadata.
	args := []any{in.UserID, w, in.ConversationID, in.Radius, in.Limit, in.MaxBytes}
	parent := `c.user_id=$1 AND c.workspace_id=$2 AND c.id=$3 AND c.finalize_status<>'invalidated' AND ` + storeaccess.PartitionSQL(ctx, "c.partition_id")
	var query string
	if len(in.Refs) > 0 {
		for _, ref := range in.Refs {
			if ref.WorkspaceID != w || ref.Revision <= 0 || ref.PartIndex < 0 {
				return nil, domain.ErrAccessConflict
			}
		}
		raw, _ := json.Marshal(in.Refs)
		args = append(args, string(raw))
		query = `WITH refs AS (SELECT * FROM jsonb_to_recordset($7::jsonb) AS x(message_id text,revision bigint,part_index int)),
 anchors AS (SELECT a.sequence_num FROM ingestion_window_sources a JOIN source_messages s ON s.id=a.source_message_id
 JOIN refs ref ON ref.message_id=s.id AND ref.revision=s.revision AND ref.part_index<jsonb_array_length(s.parts)
 JOIN conversations c ON c.id=a.conversation_id AND c.user_id=a.user_id AND c.workspace_id=a.workspace_id
 WHERE ` + parent + ` AND c.context_mode='events' AND s.user_id=$1 AND s.workspace_id=$2 AND s.partition_id=c.partition_id
 AND a.source_revision=s.revision AND NOT s.tombstoned AND s.content_status='active'),
 valid AS (SELECT COUNT(*)=(SELECT COUNT(*) FROM refs) AS ok FROM anchors)
 SELECT w.sequence_num,s.parts::text,COALESCE(s.speaker_actor_id,''),s.occurred_at,s.id,s.revision,
 COALESCE(c.metadata->>'source_session_time',''),CASE WHEN actor.kind='bot' THEN 'assistant' ELSE 'user' END,'{}'::jsonb
 FROM ingestion_window_sources w JOIN conversations c ON c.id=w.conversation_id AND c.user_id=w.user_id AND c.workspace_id=w.workspace_id
 JOIN source_messages s ON s.id=w.source_message_id
 LEFT JOIN actors actor ON actor.user_id=s.user_id AND actor.id=s.speaker_actor_id
 WHERE ` + parent + ` AND (SELECT ok FROM valid) AND s.user_id=$1 AND s.workspace_id=$2 AND s.partition_id=c.partition_id
 AND s.revision=w.source_revision AND NOT s.tombstoned AND s.content_status='active'
 AND octet_length(s.parts::text)<=$6 AND EXISTS(SELECT 1 FROM anchors a WHERE w.sequence_num BETWEEN a.sequence_num-$4 AND a.sequence_num+$4)
 ORDER BY w.sequence_num LIMIT $5`
	} else {
		if len(in.Sequences) == 0 || in.ConversationRevision <= 0 {
			return nil, nil
		}
		args = append(args, in.ConversationRevision)
		parent += ` AND c.write_revision=$7`
		predicates := []string{}
		for _, seq := range in.Sequences {
			args = append(args, seq)
			predicates = append(predicates, fmt.Sprintf("m.sequence_num BETWEEN $%d::int-$4 AND $%d::int+$4", len(args), len(args)))
		}
		query = `SELECT m.sequence_num,m.text_content,'',NULL::timestamptz,'',0,
 COALESCE(c.metadata->>'source_session_time',''),m.role,m.provider_payload
 FROM messages m JOIN conversations c ON c.id=m.conversation_id AND c.user_id=m.user_id AND c.workspace_id=m.workspace_id
 WHERE ` + parent + ` AND c.context_mode<>'events' AND octet_length(m.text_content)<=$6 AND (` + strings.Join(predicates, " OR ") + `) ORDER BY m.sequence_num LIMIT $5`
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var turns []domain.SourceTurn
	for rows.Next() {
		t := domain.SourceTurn{UserID: in.UserID, WorkspaceID: w, ConversationID: in.ConversationID}
		var occurred *time.Time
		var id, role string
		var revision int64
		var metadata []byte
		if err = rows.Scan(&t.Sequence, &t.Text, &t.Speaker, &occurred, &id, &revision, &t.SourceDate, &role, &metadata); err != nil {
			return nil, err
		}
		t.Role = domain.MessageRole(role)
		if occurred != nil {
			t.OccurredAt = *occurred
		}
		if id != "" {
			var parts []domain.SourcePart
			if err = json.Unmarshal([]byte(t.Text), &parts); err != nil {
				return nil, err
			}
			t.Parts = parts
			var text []string
			for i, p := range parts {
				text = append(text, p.Text)
				t.Refs = append(t.Refs, domain.SourceRef{WorkspaceID: w, MessageID: id, Revision: revision, PartIndex: i})
			}
			t.Text = strings.Join(text, "\n")
		} else {
			var m map[string]any
			if len(metadata) > 0 {
				err = json.Unmarshal(metadata, &m)
			}
			if err != nil {
				return nil, err
			}
			t.ExternalRef = dbutilstore.StringValue(m, "ref")
			t.Speaker = dbutilstore.StringValue(m, "speaker")
		}
		turns = append(turns, t)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return boundSourceTurns(turns, in), nil
}
