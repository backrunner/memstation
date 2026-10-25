package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MemorySourceDependency struct{ ent.Schema }

func (MemorySourceDependency) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemorySourceDependency) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("user_id"),
		field.String("workspace_id"),
		field.String("node_id"),
		field.String("source_message_id"),
		field.Int64("source_revision"),
	}
}

func (MemorySourceDependency) Indexes() []ent.Index {
	return []ent.Index{index.Fields("node_id", "source_message_id", "source_revision").Unique().StorageKey("memory_source_dependencies_node_id_source_message_id_source_key"), index.Fields("user_id", "workspace_id", "source_message_id").StorageKey("source_dependencies_message")}
}
