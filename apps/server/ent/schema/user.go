package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type User struct{ ent.Schema }

func (User) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Default("default"),
		field.String("email").Optional(),
		field.Bool("is_admin").Default(false),
	}
}
