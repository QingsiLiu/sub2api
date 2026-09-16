package service

// RouteBillingSnapshot is immutable evidence of the route and quota selected
// for a request. It is stored with the usage row, including in batched writes.
type RouteBillingSnapshot struct {
	RoutingSource       string            `json:"routing_source,omitempty"`
	TargetGroupName     string            `json:"target_group_name,omitempty"`
	SubscriptionName    string            `json:"subscription_name,omitempty"`
	Attempts            []KeyRouteAttempt `json:"attempts,omitempty"`
	SubscriptionID      *int64            `json:"subscription_id"`
	RequestedModel      string            `json:"requested_model"`
	UpstreamModel       string            `json:"upstream_model"`
	ResolvedPlatform    string            `json:"resolved_platform"`
	TargetGroupID       int64             `json:"target_group_id"`
	RouteID             *int64            `json:"route_id"`
	BillingMode         string            `json:"billing_mode"`
	RawCost             float64           `json:"raw_cost"`
	EffectiveMultiplier float64           `json:"effective_multiplier"`
	ActualCost          float64           `json:"actual_cost"`
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
	requested := route.PublicModel
	if requested == "" {
		requested = l.Model
	}
	if upstream == "" {
		upstream = l.Model
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
		SubscriptionID: l.SubscriptionID, RequestedModel: requested,
		RoutingSource: route.Source, TargetGroupName: key.Group.Name, Attempts: append([]KeyRouteAttempt(nil), key.RouteAttempts...),
		UpstreamModel: upstream, ResolvedPlatform: route.TargetPlatform,
		TargetGroupID: *key.GroupID, RouteID: routeID, BillingMode: mode,
		RawCost: l.TotalCost, EffectiveMultiplier: l.RateMultiplier, ActualCost: l.ActualCost,
	}
}
