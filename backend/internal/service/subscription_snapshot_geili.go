package service

import (
	"encoding/json"
	"fmt"
)

type subscriptionPurchaseSnapshot struct {
	Version int      `json:"version"`
	Daily   *float64 `json:"daily_limit_usd"`
	Weekly  *float64 `json:"weekly_limit_usd"`
	Monthly *float64 `json:"monthly_limit_usd"`
	Days    int      `json:"validity_days"`
}

func readSubscriptionSnapshot(m map[string]any) (subscriptionPurchaseSnapshot, error) {
	var s subscriptionPurchaseSnapshot
	b, e := json.Marshal(m)
	if e != nil {
		return s, e
	}
	if e = json.Unmarshal(b, &s); e != nil {
		return s, e
	}
	if s.Version != 1 || s.Days <= 0 {
		return s, fmt.Errorf("invalid subscription purchase snapshot")
	}
	return s, validatePlanQuotas(s.Daily, s.Weekly, s.Monthly)
}
