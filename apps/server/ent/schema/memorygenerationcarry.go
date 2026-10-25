package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MemoryGenerationCarry struct{ ent.Schema }

func (MemoryGenerationCarry) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryGenerationCarry) Fields() []ent.Field {
	return []ent.Field{field.String("id"), field.String("user_id"), field.String("workspace_id"), field.String("partition_id").Default(""), field.String("generation_id"), field.String("node_id"), field.String("source_generation_id"), field.Int64("source_revision"), field.Int64("embedding_revision"), field.String("status").Default("queued"), field.String("last_error").Default("")}
}

func (MemoryGenerationCarry) Indexes() []ent.Index {
	return []ent.Index{index.Fields("generation_id", "node_id").Unique().StorageKey("memory_generation_carries_generation_id_node_id_key"), index.Fields("user_id", "workspace_id", "generation_id").StorageKey("memory_generation_carries_scope")}
}
