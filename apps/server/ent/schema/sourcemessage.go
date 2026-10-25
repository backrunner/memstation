package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type SourceMessage struct{ ent.Schema }

func (SourceMessage) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (SourceMessage) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("user_id"),
		field.String("workspace_id"),
		field.String("integration_id"),
		field.String("external_message_id"),
		field.String("partition_id").Default(""),
		field.String("stream_id"),
		field.String("speaker_actor_id").Default(""),
		field.String("payload_hash"),
		field.String("content_status").Default("active"),
		field.Int64("revision"),
		field.JSON("parts", []map[string]any{}).Default([]map[string]any{}),
		field.JSON("reply_to", map[string]any{}).Optional(),
		field.Time("occurred_at"),
		field.Bool("tombstoned").Default(false),
	}
}

func (SourceMessage) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "id").Unique().StorageKey("source_messages_user_id_workspace_id_id_key"), index.Fields("user_id", "workspace_id", "integration_id", "external_message_id").Unique().StorageKey("source_messages_user_id_workspace_id_integration_id_externa_key")}
}
