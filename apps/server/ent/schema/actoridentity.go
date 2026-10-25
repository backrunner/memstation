package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ActorIdentity struct{ ent.Schema }

func (ActorIdentity) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (ActorIdentity) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("user_id"),
		field.String("identity_namespace_id"),
		field.String("external_actor_id"),
		field.String("actor_id"),
		field.String("verification_status").Default("integration"),
	}
}

func (ActorIdentity) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "identity_namespace_id", "external_actor_id").Unique().StorageKey("actor_identities_user_id_identity_namespace_id_external_act_key")}
}
