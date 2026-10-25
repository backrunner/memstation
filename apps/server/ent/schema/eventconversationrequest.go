package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type EventConversationRequest struct{ ent.Schema }

func (EventConversationRequest) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (EventConversationRequest) Fields() []ent.Field {
	return []ent.Field{field.String("id"), field.String("user_id"), field.String("workspace_id"), field.String("partition_id").Default(""), field.String("stream_id"), field.String("conversation_id"), field.String("payload_hash")}
}
