package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MemoryOperation struct{ ent.Schema }

func (MemoryOperation) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryOperation) Fields() []ent.Field {
	return []ent.Field{field.String("id"), field.String("user_id"), field.String("workspace_id"), field.String("partition_id").Default(""), field.String("agent_id"), field.String("kind"), field.String("status").Default("pending"), field.Int64("revision").Default(1), field.JSON("selector", map[string]any{}), field.Bool("read_blocked").Default(false), field.Bool("online_cleaned").Default(false), field.Bool("journal_persisted").Default(false), field.String("backup_status").Default("retained_until_expiry"), field.String("failure_code").Default(""), field.JSON("result", map[string]any{}).Default(map[string]any{}), field.Time("expires_at").Optional().Nillable()}
}

func (MemoryOperation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "partition_id", "status", "created_at").StorageKey("memory_operations_scope")}
}
