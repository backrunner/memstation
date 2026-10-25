package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type SourceLink struct{ ent.Schema }

func (SourceLink) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (SourceLink) Fields() []ent.Field {
	return []ent.Field{field.String("id"), field.String("user_id"), field.String("workspace_id"), field.String("source_message_id"), field.String("parent_message_id"), field.Int64("source_revision"), field.Int64("parent_revision")}
}

func (SourceLink) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "parent_message_id").StorageKey("source_links_parent_idx"), index.Fields("source_message_id", "source_revision", "parent_message_id", "parent_revision").Unique().StorageKey("source_links_source_message_id_source_revision_parent_messa_key")}
}
