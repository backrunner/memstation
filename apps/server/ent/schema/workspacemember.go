package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type WorkspaceMember struct{ ent.Schema }

func (WorkspaceMember) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (WorkspaceMember) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("user_id"),
		field.String("workspace_id"),
		field.String("actor_id"),
		field.String("display_name"),
		field.String("status").Default("active"),
		field.Int64("revision").Default(1),
	}
}

func (WorkspaceMember) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "actor_id").Unique().StorageKey("workspace_members_user_id_workspace_id_actor_id_key")}
}
