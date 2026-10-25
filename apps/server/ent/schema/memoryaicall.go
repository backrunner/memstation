package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MemoryAICall struct{ ent.Schema }

func (MemoryAICall) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (MemoryAICall) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"), field.String("user_id"), field.String("workspace_id"), field.String("partition_id").Default(""), field.JSON("partition_ids", []string{}).Default([]string{""}),
		field.String("role"), field.String("model_alias"), field.String("model_version").Default(""), field.String("trace_id").Default(""), field.String("job_id").Default(""), field.Int("attempt").Default(0),
		field.String("provider_request_id").Default(""), field.String("broker_usage_id").Default(""), field.String("status").Default("started"), field.Int("input_tokens").Default(0), field.Int("output_tokens").Default(0),
		field.String("reported_cost").Default(""), field.String("currency").Default(""), field.String("usage_authority").Default("broker"), field.String("failure_code").Default("")}
}
func (MemoryAICall) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "partition_id", "created_at", "id"), index.Fields("broker_usage_id").Unique().Annotations(entsql.IndexWhere("broker_usage_id<>''"))}
}
