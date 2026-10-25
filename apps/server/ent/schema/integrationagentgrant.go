package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IntegrationAgentGrant struct{ ent.Schema }

func (IntegrationAgentGrant) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (IntegrationAgentGrant) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("user_id"),
		field.String("workspace_id"),
		field.String("integration_id"),
		field.String("agent_id"),
		field.JSON("allowed_event_types", []string{}).Default([]string{}),
		field.Int64("revision").Default(1),
	}
}

func (IntegrationAgentGrant) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "integration_id", "agent_id").Unique().StorageKey("integration_agent_grants_user_id_workspace_id_integration_i_key")}
}
