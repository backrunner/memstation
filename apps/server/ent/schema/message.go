package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type Message struct{ ent.Schema }

func (Message) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (Message) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id"),
 field.String("workspace_id").Immutable(),
		field.String("conversation_id"),
		field.String("role"),
		field.JSON("content_blocks", []map[string]any{}).Optional(),
		field.String("text_content").Optional(),
		field.JSON("tool_calls", []map[string]any{}).Optional(),
		field.JSON("tool_result", map[string]any{}).Optional(),
		field.JSON("provider_payload", map[string]any{}).Optional(),
		field.Int("sequence_num").Default(0),
	}
}
