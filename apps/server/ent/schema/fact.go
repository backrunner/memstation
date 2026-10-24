package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type Fact struct{ ent.Schema }

func (Fact) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (Fact) Fields() []ent.Field {
	return []ent.Field{
		field.String("generation_id").Default(""),
		field.String("user_id"),
		field.String("conversation_id"),
		field.String("node_id").Unique(),
		field.String("category").Optional(),
		field.String("subject").Optional(),
		field.String("fact_key").Optional(),
		field.String("lineage_group_id").Optional(),
		field.Int("revision_seq").Default(1),
		field.String("content"),
		field.JSON("metadata", map[string]any{}).Default(map[string]any{}),
		field.Float("confidence").Default(0.8),
		field.Bool("is_superseded").Default(false),
		field.String("superseded_by_id").Optional(),
		field.Time("valid_from").Optional().Nillable(),
		field.Time("valid_to").Optional().Nillable(),
	}
}
