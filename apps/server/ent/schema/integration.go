package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Integration struct{ ent.Schema }

func (Integration) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (Integration) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("user_id"),
		field.String("name"),
		field.String("kind"),
		field.String("identity_namespace_id"),
		field.String("status").Default("active"),
		field.Int64("revision").Default(1),
	}
}

func (Integration) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "id").Unique().StorageKey("integrations_user_id_id_key")}
}
