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

// SubscriptionPlanGroup maps a plan to an entitled group.
type SubscriptionPlanGroup struct{ ent.Schema }

func (SubscriptionPlanGroup) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "subscription_plan_groups"}}
}
func (SubscriptionPlanGroup) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("subscription_plan_id"), field.Int64("group_id"),
		field.Time("created_at").Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}
func (SubscriptionPlanGroup) Indexes() []ent.Index {
	return []ent.Index{index.Fields("subscription_plan_id", "group_id").Unique(), index.Fields("group_id")}
}
func (SubscriptionPlanGroup) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("plan", SubscriptionPlan.Type).Ref("group_entitlements").Field("subscription_plan_id").Unique().Required(),
		edge.From("group", Group.Type).Ref("plan_entitlements").Field("group_id").Unique().Required(),
	}
}
