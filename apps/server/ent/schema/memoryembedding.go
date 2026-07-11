package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MemoryEmbedding struct{ ent.Schema }

func (MemoryEmbedding) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryEmbedding) Fields() []ent.Field {
	return []ent.Field{
		field.String("embedding_id").Unique(),
		field.String("generation_id").Default(""),
		field.String("user_id"),
		field.String("node_id"),
		field.String("node_type"),
		field.Int("embedding_version").Default(1),
		field.String("embedding_model"),
		field.String("content_hash"),
		field.Bytes("vector").Optional(),
	}
}

func (MemoryEmbedding) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "node_id", "embedding_version").Unique()}
}
