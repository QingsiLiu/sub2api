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

// UserSubscriptionEntitlement is one purchased, independently expiring
// portion of a user's aggregate subscription.
type UserSubscriptionEntitlement struct{ ent.Schema }

func (UserSubscriptionEntitlement) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "user_subscription_entitlements"}}
}

func (UserSubscriptionEntitlement) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_subscription_id"),
		field.Int64("plan_id").Optional().Nillable(),
		field.Int64("source_order_id").Optional().Nillable(),
		field.Int("lot_index").Default(0),
		field.String("source_type").Default("legacy"),
		field.String("source_reference").Default(""),
		field.String("purchase_mode").MaxLen(20).Default("renew"),
		field.String("status").MaxLen(20).Default("active"),
		field.Time("starts_at").SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("expires_at").SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Float("daily_limit_usd").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("weekly_limit_usd").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("monthly_limit_usd").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Time("daily_window_start").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("weekly_window_start").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("monthly_window_start").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Float("daily_usage_usd").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(0),
		field.Float("weekly_usage_usd").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(0),
		field.Float("monthly_usage_usd").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(0),
		field.Float("lifetime_usage_usd").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(0),
		field.Time("refunded_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("created_at").Immutable().Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (UserSubscriptionEntitlement) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_subscription_id", "expires_at"),
		index.Fields("source_order_id", "lot_index").Unique(),
		index.Fields("status", "expires_at"),
	}
}

func (UserSubscriptionEntitlement) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("subscription", UserSubscription.Type).Ref("entitlements").Field("user_subscription_id").Unique().Required(),
		edge.From("plan", SubscriptionPlan.Type).Ref("entitlements").Field("plan_id").Unique(),
		edge.From("source_order", PaymentOrder.Type).Ref("subscription_entitlements").Field("source_order_id").Unique(),
		edge.To("order_lines", SubscriptionEntitlementOrder.Type),
	}
}
