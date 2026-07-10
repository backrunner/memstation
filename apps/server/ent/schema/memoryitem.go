package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type MemoryItem struct{ ent.Schema }

func (MemoryItem) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryItem) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Unique(),
		field.String("generation_id").Default(""),
		field.String("user_id"),
		field.String("conversation_id").Default(""),
		field.String("node_id").Unique(),
		field.String("item_type"),
		field.String("title").Default(""),
		field.String("content"),
		field.String("status").Default("current"),
		field.Float("confidence").Default(0.75),
		field.Time("valid_from").Optional().Nillable(),
		field.Time("valid_to").Optional().Nillable(),
		field.String("review_status").Default("auto"),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Time("deleted_at").Optional().Nillable(),
	}
}
