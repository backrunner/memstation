package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type KeyMemory struct{ ent.Schema }

func (KeyMemory) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (KeyMemory) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id"),
		field.String("node_id").Unique(),
		field.String("key"),
		field.String("value"),
		field.String("category").Optional(),
		field.String("source").Optional(),
		field.String("review_status").Default("pending"),
		field.Time("last_confirmed_at").Optional().Nillable(),
		field.Time("deleted_at").Optional().Nillable(),
	}
}
