package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MemoryTombstone struct{ ent.Schema }

func (MemoryTombstone) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryTombstone) Fields() []ent.Field {
	return []ent.Field{field.String("id"), field.String("user_id"), field.String("workspace_id"), field.String("partition_id").Default(""), field.String("operation_id"), field.String("target_type"), field.String("target_id"), field.Time("cutoff")}
}

func (MemoryTombstone) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "partition_id", "target_type", "target_id").Unique().StorageKey("memory_tombstones_user_id_workspace_id_partition_id_target__key")}
}
