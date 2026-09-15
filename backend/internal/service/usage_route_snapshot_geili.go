package service

// RouteBillingSnapshot is immutable evidence of the route and quota selected
// for a request. It is stored with the usage row, including in batched writes.
type RouteBillingSnapshot struct {
	SubscriptionID      *int64  `json:"subscription_id"`
	RequestedModel      string  `json:"requested_model"`
	UpstreamModel       string  `json:"upstream_model"`
	ResolvedPlatform    string  `json:"resolved_platform"`
	TargetGroupID       int64   `json:"target_group_id"`
	RouteID             *int64  `json:"route_id"`
	BillingMode         string  `json:"billing_mode"`
	RawCost             float64 `json:"raw_cost"`
	EffectiveMultiplier float64 `json:"effective_multiplier"`
	ActualCost          float64 `json:"actual_cost"`
}

func (l *UsageLog) CaptureRouteBilling(key *APIKey) {
	if key == nil || key.CompositeRoute == nil || key.GroupID == nil {
		return
	}
	route := key.CompositeRoute
	upstream := route.UpstreamModel
	if l.UpstreamModel != nil && *l.UpstreamModel != "" {
		upstream = *l.UpstreamModel
	}
	mode := "balance"
	if l.SubscriptionID != nil {
		mode = "subscription"
	}
	var routeID *int64
	if route.Route != nil {
		id := route.Route.ID
		routeID = &id
	}
	l.RouteBillingSnapshot = &RouteBillingSnapshot{
		SubscriptionID: l.SubscriptionID, RequestedModel: route.PublicModel,
		UpstreamModel: upstream, ResolvedPlatform: route.TargetPlatform,
		TargetGroupID: *key.GroupID, RouteID: routeID, BillingMode: mode,
		RawCost: l.TotalCost, EffectiveMultiplier: l.RateMultiplier, ActualCost: l.ActualCost,
	}
}
