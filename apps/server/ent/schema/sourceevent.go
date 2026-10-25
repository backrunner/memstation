package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type SourceEvent struct{ ent.Schema }

func (SourceEvent) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (SourceEvent) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("operation_id").Default(""),
		field.String("processing_conversation_id").Default(""),
		field.String("user_id"),
		field.String("workspace_id"),
		field.String("integration_id"),
		field.String("external_event_id"),
		field.String("source_message_id"),
		field.String("stream_id"),
		field.String("event_type"),
		field.String("payload_hash"),
		field.String("conversation_id").Default(""),
		field.Int64("source_revision"),
		field.Int64("sequence_num"),
		field.JSON("payload", map[string]any{}),
	}
}

func (SourceEvent) Indexes() []ent.Index {
	return []ent.Index{index.Fields("operation_id").StorageKey("source_events_operation_idx").Annotations(entsql.IndexWhere("(operation_id <> ''::text)")), index.Fields("stream_id", "sequence_num").Unique().StorageKey("source_events_stream_id_sequence_num_key"), index.Fields("user_id", "workspace_id", "integration_id", "external_event_id").Unique().StorageKey("source_events_user_id_workspace_id_integration_id_external__key"), index.Fields("conversation_id").StorageKey("source_events_window")}
}
