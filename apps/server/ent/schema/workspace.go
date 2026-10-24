package schema
import (
 "entgo.io/ent"
 "entgo.io/ent/schema/field"
 "entgo.io/ent/schema/index"
)
type Workspace struct { ent.Schema }
func (Workspace) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (Workspace) Fields() []ent.Field { return []ent.Field{ field.String("id"), field.String("user_id"), field.String("name"), field.String("kind").Default("personal"), field.String("status").Default("active"), field.Int64("policy_version").Default(1), field.JSON("capabilities", []string{}), } }
func (Workspace) Indexes() []ent.Index { return []ent.Index{ index.Fields("user_id", "id").Unique(), } }
