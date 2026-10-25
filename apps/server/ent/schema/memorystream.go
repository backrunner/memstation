package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MemoryStream struct{ ent.Schema }

func (MemoryStream) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryStream) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("user_id"),
		field.String("workspace_id"),
		field.String("integration_id"),
		field.String("external_space_id"),
		field.String("thread_id").Default(""),
		field.String("partition_id").Default(""),
		field.String("current_conversation_id").Default(""),
		field.String("generation_id").Default(""),
		field.Int64("accepted_sequence").Default(0),
		field.Int64("indexed_sequence").Default(0),
	}
}

func (MemoryStream) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "id").Unique().StorageKey("memory_streams_user_id_workspace_id_id_key"), index.Fields("user_id", "workspace_id", "integration_id", "external_space_id", "thread_id", "partition_id").Unique().StorageKey("memory_streams_user_id_workspace_id_integration_id_external_key")}
}
