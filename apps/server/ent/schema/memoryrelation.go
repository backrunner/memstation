package schema

import (
	"fmt"
	"strings"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MemoryRelation struct{ ent.Schema }

func (MemoryRelation) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryRelation) Fields() []ent.Field {
	return []ent.Field{
		field.String("generation_id").Default(""),
		field.String("user_id"),
		field.String("src_node_id"),
		field.String("dst_node_id"),
		field.String("relation_type").Validate(func(value string) error {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("relation type is required")
			}
			return nil
		}),
		field.Float("weight").Default(1).Range(0, 1),
		field.String("source").Default("analysis"),
		field.JSON("metadata", map[string]any{}).Optional(),
	}
}

func (MemoryRelation) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "src_node_id", "dst_node_id", "relation_type").Unique(),
		index.Fields("user_id", "generation_id", "src_node_id"),
		index.Fields("user_id", "generation_id", "dst_node_id"),
	}
}
