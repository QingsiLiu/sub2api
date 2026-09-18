package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type SubscriptionRequest struct{ ent.Schema }

func (SubscriptionRequest) Fields() []ent.Field {
	return []ent.Field{field.String("request_key").Unique(), field.Int64("subscription_id"), field.Int64("api_key_id"), field.String("status").Default("admitted"), field.JSON("lots", json.RawMessage{}), field.Time("admitted_at"), field.Time("settled_at").Optional().Nillable(), field.String("billing_request_id").Default(""), field.Float("cost_usd").Default(0)}
}
func (SubscriptionRequest) Indexes() []ent.Index {
	return []ent.Index{index.Fields("subscription_id", "status")}
}
