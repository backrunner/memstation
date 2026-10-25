package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type DeletionJournalState struct{ ent.Schema }

func (DeletionJournalState) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (DeletionJournalState) Fields() []ent.Field {
	return []ent.Field{field.String("id"), field.String("deployment_id"), field.Int64("sequence_num").Default(0), field.String("digest").Default("")}
}
func (DeletionJournalState) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "deletion_journal_state"}}
}
