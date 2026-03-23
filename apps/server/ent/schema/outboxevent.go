package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type OutboxEvent struct{ ent.Schema }

func (OutboxEvent) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (OutboxEvent) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id"),
		field.String("event_type"),
		field.String("aggregate_type"),
		field.String("aggregate_id"),
		field.JSON("payload", map[string]any{}),
		field.String("status").Default("pending"),
		field.Time("available_at"),
		field.Time("processed_at").Optional().Nillable(),
		field.Int("retry_count").Default(0),
	}
}
