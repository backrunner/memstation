package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MemoryPublication struct{ ent.Schema }

func (MemoryPublication) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryPublication) Fields() []ent.Field {
	return []ent.Field{field.String("id"), field.String("retraction_operation_id").Default(""), field.String("user_id"), field.String("source_workspace_id"), field.String("source_node_id"), field.String("target_workspace_id"), field.String("target_node_id"), field.String("target_partition_id").Default(""), field.Int64("source_revision"), field.Int64("revision").Default(1), field.String("status").Default("active"), field.JSON("actor_mapping", map[string]string{}).Default(map[string]string{})}
}

func (MemoryPublication) Indexes() []ent.Index {
	return []ent.Index{index.Fields("target_node_id").Unique().StorageKey("memory_publications_target_node_id_key")}
}
