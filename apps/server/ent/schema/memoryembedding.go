package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type MemoryEmbedding struct{ ent.Schema }

func (MemoryEmbedding) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryEmbedding) Fields() []ent.Field {
	return []ent.Field{
		field.String("embedding_id").Unique(),
		field.String("user_id"),
		field.String("node_id"),
		field.String("node_type"),
		field.Int("embedding_version").Default(1),
		field.String("embedding_model"),
		field.String("content_hash"),
		field.Bytes("vector").Optional(),
	}
}
