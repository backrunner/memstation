package schema
import (
 "entgo.io/ent"
 "entgo.io/ent/schema/field"
 "entgo.io/ent/schema/index"
)
type Actor struct { ent.Schema }
func (Actor) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (Actor) Fields() []ent.Field { return []ent.Field{ field.String("id"), field.String("user_id"), field.String("kind"), field.String("display_name"), field.String("status").Default("active"), field.Int64("revision").Default(1), } }
func (Actor) Indexes() []ent.Index { return []ent.Index{ index.Fields("user_id", "id").Unique(), } }
