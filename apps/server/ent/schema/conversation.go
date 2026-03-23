package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type Conversation struct{ ent.Schema }

func (Conversation) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (Conversation) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id"),
		field.String("request_format"),
		field.String("upstream_provider").Optional(),
		field.String("mode").Default("transparent"),
		field.String("finalize_status").Default("active"),
		field.String("summary_status").Default("pending"),
		field.String("analysis_status").Default("pending"),
		field.JSON("metadata", map[string]any{}).Optional(),
	}
}
