package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type MemoryNode struct{ ent.Schema }

func (MemoryNode) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryNode) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id"),
		field.String("node_type"),
		field.String("entity_id"),
		field.Float("importance").Default(1),
		field.Float("decay_factor").Default(1),
		field.Time("activity_at").Optional(),
		field.String("time_tier").Default(""),
		field.String("embedding_status").Default("pending"),
	}
}
