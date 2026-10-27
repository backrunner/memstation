package schema
import (
 "entgo.io/ent"
 "entgo.io/ent/schema/field"
 "entgo.io/ent/schema/index"
)
type Agent struct { ent.Schema }
func (Agent) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (Agent) Fields() []ent.Field { return []ent.Field{ field.String("id"), field.String("user_id"), field.String("actor_id"), field.String("name"), field.String("status").Default("active"), field.String("default_workspace_id"), field.Int64("revision").Default(1),  } }
func (Agent) Indexes() []ent.Index { return []ent.Index{ index.Fields("user_id", "id").Unique(), index.Fields("user_id", "actor_id").Unique(), } }
