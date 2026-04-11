package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type UserSetting struct{ ent.Schema }

func (UserSetting) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (UserSetting) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id").Unique(),
		field.String("chat_model").Optional(),
		field.String("summary_model").Optional(),
		field.String("analysis_model").Optional(),
		field.String("embedding_model").Optional(),
		field.String("embedding_base_url").Optional(),
		field.String("embedding_api_key_encrypted").Optional(),
		field.Bool("retain_source_materials").Default(true),
		field.Bool("tool_write_enabled").Default(false),
		field.Int("max_injection_tokens").Default(2048),
	}
}
