package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MemoryNode struct{ ent.Schema }

func (MemoryNode) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryNode) Fields() []ent.Field {
	return []ent.Field{
		field.String("generation_id").Default(""),
		field.String("source_conversation_id").Default(""),
		field.String("user_id"),
		field.String("workspace_id").Immutable(),
		field.String("partition_id").Default("").Immutable(),
		field.String("access_status").Default("active"),
		field.JSON("subject_actor_ids", []string{}).Default([]string{}),
		field.JSON("source_refs", []map[string]any{}).Default([]map[string]any{}),
		field.Int64("revision").Default(1),
		field.String("node_type"),
		field.String("entity_id"),
		field.Float("importance").Default(1),
		field.Float("decay_factor").Default(1),
		field.Time("activity_at").Optional(),
		field.String("time_tier").Default(""),
		field.String("embedding_status").Default("pending"),
		field.Int64("embedding_revision").Default(0).NonNegative(),
	}
}

func (MemoryNode) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "partition_id", "node_type", "entity_id").Unique().StorageKey("memory_nodes_scoped_entity"), index.Fields("user_id", "workspace_id", "id").Unique(), index.Fields("user_id", "workspace_id", "node_type", "activity_at"), index.Fields("user_id", "workspace_id", "partition_id", "access_status", "generation_id").StorageKey("memory_nodes_access"), index.Fields("user_id", "workspace_id", "source_conversation_id").StorageKey("memory_nodes_source_window")}
}
