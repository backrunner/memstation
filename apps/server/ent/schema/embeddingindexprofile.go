package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type EmbeddingIndexProfile struct{ ent.Schema }

func (EmbeddingIndexProfile) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (EmbeddingIndexProfile) Fields() []ent.Field {
	return []ent.Field{field.String("id"), field.String("user_id"), field.String("workspace_id"), field.String("generation_id"), field.Int("embedding_version"), field.String("model_version"), field.Int("dimensions").Positive(), field.String("fingerprint")}
}

func (EmbeddingIndexProfile) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "generation_id", "embedding_version").Unique().StorageKey("embedding_index_profiles_user_id_workspace_id_generation_id_key")}
}
