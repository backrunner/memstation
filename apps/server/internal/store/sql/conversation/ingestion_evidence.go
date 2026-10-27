package conversation

import (
	"context"
	"encoding/json"

	"memstation/apps/server/internal/domain"
)

func (r *PostgresConversationRepository) loadIngestMessages(ctx context.Context, c domain.ConversationRecord) ([]domain.UnifiedMessage, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT s.id,s.revision,s.speaker_actor_id,s.parts,s.reply_to,s.occurred_at,COALESCE(a.kind,'unknown') FROM ingestion_window_sources w JOIN source_messages s ON s.id=w.source_message_id LEFT JOIN actors a ON a.user_id=s.user_id AND a.id=s.speaker_actor_id WHERE w.user_id=$1 AND w.workspace_id=$2 AND w.conversation_id=$3 AND s.partition_id=$4 AND s.revision=w.source_revision AND NOT s.tombstoned AND s.content_status='active' ORDER BY w.sequence_num`, c.UserID, c.WorkspaceID, c.ConversationID, c.PartitionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.UnifiedMessage
	for rows.Next() {
		source := domain.MessageSource{WorkspaceID: c.WorkspaceID}
		var parts, reply []byte
		if err = rows.Scan(&source.MessageID, &source.Revision, &source.Speaker.ActorID, &parts, &reply, &source.OccurredAt, &source.Speaker.Kind); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(parts, &source.Parts); err != nil {
			return nil, err
		}
		if len(reply) > 0 {
			if err = json.Unmarshal(reply, &source.ReplyTo); err != nil {
				return nil, err
			}
		}
		message := domain.UnifiedMessage{Role: domain.RoleUser, Source: &source}
		if source.Speaker.Kind == "bot" {
			message.Role = domain.RoleAssistant
		}
		for _, part := range source.Parts {
			if part.Type == "media" && part.Media != nil {
				message.Blocks = append(message.Blocks, *part.Media)
			} else {
				message.Blocks = append(message.Blocks, domain.ContentBlock{Type: domain.ContentBlockText, Text: part.Text})
			}
			if message.Text != "" {
				message.Text += "\n"
			}
			if part.Type == "media" && part.Media != nil {
				message.Text += "[" + string(part.Media.Type) + " attachment]"
			} else {
				message.Text += part.Text
			}
		}
		result = append(result, message)
	}
	return result, rows.Err()
}
