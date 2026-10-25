package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IngestionReceipt struct{ ent.Schema }

func (IngestionReceipt) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (IngestionReceipt) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.Time("processing_started_at").Optional().Nillable(),
		field.String("user_id"),
		field.String("workspace_id"),
		field.String("partition_id").Default(""),
		field.String("stream_id"),
		field.String("idempotency_key"),
		field.String("payload_hash"),
		field.Int64("first_sequence"),
		field.Int64("last_sequence"),
		field.JSON("events", []map[string]any{}),
	}
}

func (IngestionReceipt) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "workspace_id", "stream_id", "idempotency_key").Unique().StorageKey("ingestion_receipts_user_id_workspace_id_stream_id_idempoten_key")}
}
