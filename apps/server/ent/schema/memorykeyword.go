package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type MemoryKeyword struct{ ent.Schema }

func (MemoryKeyword) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryKeyword) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id"),
		field.String("summary_id"),
		field.String("keyword"),
		field.Float("weight").Default(1),
	}
}
