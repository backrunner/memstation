package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type Impression struct{ ent.Schema }

func (Impression) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (Impression) Fields() []ent.Field {
	return []ent.Field{
		field.String("generation_id").Default(""),
		field.String("user_id"),
		field.String("conversation_id"),
		field.String("node_id").Unique(),
		field.String("subject").Optional(),
		field.String("subject_type").Optional(),
		field.String("evaluation").Optional(),
		field.String("sentiment").Optional(),
		field.String("familiarity").Optional(),
		field.String("description"),
	}
}
