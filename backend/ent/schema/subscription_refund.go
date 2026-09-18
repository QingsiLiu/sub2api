package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

type SubscriptionRefund struct{ ent.Schema }

func (SubscriptionRefund) Fields() []ent.Field {
	return []ent.Field{field.Int64("order_id").Unique(), field.Int64("subscription_id"), field.String("status").Default("pending"), field.JSON("snapshot", json.RawMessage{}), field.Time("created_at").Default(time.Now), field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now)}
}
func (SubscriptionRefund) Indexes() []ent.Index {
	return []ent.Index{index.Fields("subscription_id", "status")}
}
