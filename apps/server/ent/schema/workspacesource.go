package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type WorkspaceSource struct{ ent.Schema }

func (WorkspaceSource) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (WorkspaceSource) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("user_id"),
		field.String("workspace_id"),
		field.String("integration_id"),
		field.String("external_space_id"),
		field.Int64("revision").Default(1),
	}
}

func (WorkspaceSource) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "integration_id", "external_space_id").Unique().StorageKey("workspace_sources_user_id_integration_id_external_space_id_key"), index.Fields("user_id", "workspace_id", "integration_id", "external_space_id").Unique().StorageKey("workspace_sources_user_id_workspace_id_integration_id_exter_key")}
}
