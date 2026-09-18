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

// SubscriptionEntitlementOrder records the exact purchase operation applied
// to a lot. Renewal orders extend an existing lot, so a separate line table
// preserves the original lot owner and the complete refund audit trail.
type SubscriptionEntitlementOrder struct{ ent.Schema }

func (SubscriptionEntitlementOrder) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "subscription_entitlement_orders"}}
}

func (SubscriptionEntitlementOrder) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("entitlement_id"),
		field.Int64("order_id"),
		field.Int("lot_index").Default(0),
		field.String("operation").MaxLen(20),
		field.Int("days_added").Default(0),
		field.Time("before_expires_at").Optional().Nillable(),
		field.Time("after_expires_at").Optional().Nillable(),
		field.Time("reversed_at").Optional().Nillable(),
		field.Time("created_at").Immutable().Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (SubscriptionEntitlementOrder) Indexes() []ent.Index {
	return []ent.Index{index.Fields("order_id", "lot_index").Unique(), index.Fields("entitlement_id")}
}

func (SubscriptionEntitlementOrder) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("entitlement", UserSubscriptionEntitlement.Type).Ref("order_lines").Field("entitlement_id").Unique().Required(),
		edge.From("order", PaymentOrder.Type).Ref("subscription_entitlement_orders").Field("order_id").Unique().Required(),
	}
}
