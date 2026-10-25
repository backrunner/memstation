package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type DeletionJournalEntry struct{ ent.Schema }

func (DeletionJournalEntry) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (DeletionJournalEntry) Fields() []ent.Field {
	return []ent.Field{field.String("id"), field.Int64("sequence_num").Unique(), field.String("operation_id"), field.JSON("payload", map[string]any{}), field.String("digest")}
}

func (DeletionJournalEntry) Indexes() []ent.Index {
	return []ent.Index{index.Fields("sequence_num").Unique().StorageKey("deletion_journal_entries_sequence_num_key")}
}
