package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IngestionWindowSource struct{ ent.Schema }

func (IngestionWindowSource) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (IngestionWindowSource) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("user_id"),
		field.String("workspace_id"),
		field.String("conversation_id"),
		field.String("source_message_id"),
		field.Int64("source_revision"),
		field.Int("sequence_num"),
	}
}

func (IngestionWindowSource) Indexes() []ent.Index {
	return []ent.Index{index.Fields("conversation_id", "sequence_num").Unique().StorageKey("ingestion_window_sources_conversation_id_sequence_num_key"), index.Fields("conversation_id", "source_message_id").Unique().StorageKey("ingestion_window_sources_conversation_id_source_message_id_key")}
}
