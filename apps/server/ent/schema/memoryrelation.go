package schema

import "entgo.io/ent/schema/field"
import "entgo.io/ent"

type MemoryRelation struct{ ent.Schema }

func (MemoryRelation) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryRelation) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id"),
		field.String("src_node_id"),
		field.String("dst_node_id"),
		field.String("relation_type"),
		field.Float("weight").Default(1),
		field.String("source").Default("analysis"),
		field.JSON("metadata", map[string]any{}).Optional(),
	}
}
