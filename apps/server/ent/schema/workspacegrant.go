package schema
import (
 "entgo.io/ent"
 "entgo.io/ent/schema/field"
 "entgo.io/ent/schema/index"
)
type WorkspaceGrant struct { ent.Schema }
func (WorkspaceGrant) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (WorkspaceGrant) Fields() []ent.Field { return []ent.Field{ field.String("id"), field.String("user_id"), field.String("workspace_id"), field.String("agent_id"), field.JSON("capabilities", []string{}), field.Int64("revision").Default(1), } }
func (WorkspaceGrant) Indexes() []ent.Index { return []ent.Index{ index.Fields("user_id", "workspace_id", "agent_id").Unique(), } }
