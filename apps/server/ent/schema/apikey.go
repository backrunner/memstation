package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type APIKey struct{ ent.Schema }

func (APIKey) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (APIKey) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id"),
		field.String("name"),
field.String("agent_id"),
field.String("default_workspace_id"),
field.JSON("capability_ceiling", []string{}),
		field.String("prefix"),
		field.String("hash"),
		field.JSON("scopes", []string{}).Optional(),
		field.Time("expires_at").Optional().Nillable(),
		field.Time("last_used_at").Optional().Nillable(),
		field.Time("revoked_at").Optional().Nillable(),
		field.Time("deleted_at").Optional().Nillable(),
	}
}
