package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

type SubscriptionOperation struct{ ent.Schema }

func (SubscriptionOperation) Fields() []ent.Field {
	return []ent.Field{field.Int64("subscription_id"), field.Int64("entitlement_id"), field.String("operation"), field.String("source_type"), field.String("source_reference").Default(""), field.Int64("actor_id").Default(0), field.JSON("detail", map[string]any{}).Optional(), field.Time("created_at").Default(time.Now)}
}
func (SubscriptionOperation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("subscription_id", "created_at")}
}

func (SubscriptionOperation) Edges() []ent.Edge {
	return []ent.Edge{edge.From("subscription", UserSubscription.Type).Ref("entitlement_operations").Field("subscription_id").Unique().Required()}
}
