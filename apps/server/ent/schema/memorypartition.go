package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MemoryPartition struct{ ent.Schema }

func (MemoryPartition) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryPartition) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"), field.String("user_id"), field.String("workspace_id").Immutable(),
		field.String("agent_id"), field.JSON("audience_actor_ids", []string{}).Default([]string{}), field.Int64("revision").Default(1),
	}
}

func (MemoryPartition) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "id").Unique().StorageKey("memory_partitions_user_id_workspace_id_id_key")}
}
