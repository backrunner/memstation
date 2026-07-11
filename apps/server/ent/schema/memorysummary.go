package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type MemorySummary struct{ ent.Schema }

func (MemorySummary) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemorySummary) Fields() []ent.Field {
	return []ent.Field{
		field.String("generation_id").Default(""),
		field.String("user_id"),
		field.String("conversation_id"),
		field.String("node_id").Unique(),
		field.String("summary"),
		field.String("topic_label").Optional(),
		field.Int("summary_version").Default(1),
		field.Int("chunk_index").Default(0),
		field.Int("chunk_total").Default(1),
	}
}
