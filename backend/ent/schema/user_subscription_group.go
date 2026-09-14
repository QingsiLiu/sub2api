package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// UserSubscriptionGroup maps one shared subscription to an entitled group.
// The subscription row remains the single owner of quota and expiry windows.
type UserSubscriptionGroup struct{ ent.Schema }

func (UserSubscriptionGroup) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "user_subscription_groups"}}
}

func (UserSubscriptionGroup) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Immutable().Unique(),
		field.Int64("user_subscription_id"),
		field.Int64("group_id"),
		field.Time("created_at").Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (UserSubscriptionGroup) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_subscription_id", "group_id").Unique(),
		index.Fields("group_id"),
	}
}

func (UserSubscriptionGroup) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("subscription", UserSubscription.Type).Ref("group_entitlements").Field("user_subscription_id").Unique().Required(),
		edge.From("group", Group.Type).Ref("subscription_entitlements").Field("group_id").Unique().Required(),
	}
}
