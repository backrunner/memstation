package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MemoryCandidate struct{ ent.Schema }

func (MemoryCandidate) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryCandidate) Fields() []ent.Field {
	return []ent.Field{field.String("id"), field.String("user_id"), field.String("workspace_id"), field.String("partition_id").Default(""), field.String("agent_id"), field.String("kind"), field.String("content"), field.String("origin"), field.JSON("subject_actor_ids", []string{}).Default([]string{}), field.JSON("source_refs", []map[string]any{}).Default([]map[string]any{}), field.String("status").Default("pending"), field.Int64("revision").Default(1), field.String("target_node_id").Default(""), field.Int64("target_revision").Default(0), field.String("result_node_id").Default("")}
}

func (MemoryCandidate) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "partition_id", "status", "created_at").StorageKey("memory_candidates_scope")}
}
