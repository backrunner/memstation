package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type TokenUsage struct{ ent.Schema }

func (TokenUsage) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (TokenUsage) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id"),
		field.String("conversation_id"),
		field.String("provider").Optional(),
		field.String("model").Optional(),
		field.Int("input_tokens").Default(0),
		field.Int("output_tokens").Default(0),
		field.Int("total_tokens").Default(0),
		field.Float("cost").Default(0),
	}
}
