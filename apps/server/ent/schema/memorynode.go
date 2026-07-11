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
		field.String("user_id"),
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
	return []ent.Index{index.Fields("user_id", "node_type", "entity_id").Unique()}
}
