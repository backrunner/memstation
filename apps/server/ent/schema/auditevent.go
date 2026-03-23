package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type AuditEvent struct{ ent.Schema }

func (AuditEvent) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (AuditEvent) Fields() []ent.Field {
	return []ent.Field{
		field.String("actor_type"),
		field.String("actor_id"),
		field.String("action"),
		field.String("target_type"),
		field.String("target_id"),
		field.JSON("payload", map[string]any{}).Optional(),
	}
}
