package service

import (
	"encoding/json"
	"math"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// PlanQuotaPatch distinguishes an omitted quota from an explicit unlimited null.
type PlanQuotaPatch struct {
	Set   bool
	Value *float64
}

func (p *PlanQuotaPatch) UnmarshalJSON(data []byte) error {
	p.Set = true
	return json.Unmarshal(data, &p.Value)
}
func validatePlanQuotas(values ...*float64) error {
	for _, value := range values {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return infraerrors.BadRequest("PLAN_QUOTA_INVALID", "plan quotas must be finite and non-negative or null for unlimited")
		}
	}
	return nil
}
