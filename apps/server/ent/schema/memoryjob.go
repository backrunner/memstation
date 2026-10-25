package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MemoryJob struct{ ent.Schema }

func (MemoryJob) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryJob) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("generation_id").Default(""),
		field.String("queue").Default("default"),
		field.String("operation_id").Default(""),
		field.String("user_id"),
		field.String("workspace_id"),
		field.String("partition_id").Default(""),
		field.String("conversation_id").Default(""),
		field.String("task_type"),
		field.String("status"),
		field.String("failure_code").Default(""),
		field.Int("attempt").Default(0),
		field.Time("next_retry_at").Optional().Nillable(),
	}
}

func (MemoryJob) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "generation_id", "conversation_id").StorageKey("memory_jobs_generation"), index.Fields("operation_id").StorageKey("memory_jobs_operation_idx").Annotations(entsql.IndexWhere("(operation_id <> ''::text)")), index.Fields("user_id", "workspace_id", "partition_id", "status", "updated_at").StorageKey("memory_jobs_workspace_status")}
}
