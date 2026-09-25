package dto

import (
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Complete legacy records keep their JSON contract. A recovered record may know
// its exact charge without knowing tokens/model; emit null, never invented zero.
func (u UsageLog) MarshalJSON() ([]byte, error) {
	type plainUsageLog UsageLog
	data, err := json.Marshal(plainUsageLog(u))
	if err != nil {
		return nil, err
	}
	if u.Financial == nil {
		return data, nil
	}
	fields := map[string]json.RawMessage{}
	if err = json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	metadata, err := json.Marshal(u.Financial)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(metadata, &fields); err != nil {
		return nil, err
	}
	for _, field := range u.Financial.UnknownFields {
		if _, present := fields[field]; present {
			fields[field] = json.RawMessage("null")
		}
	}
	return json.Marshal(fields)
}

// Explicit marshaling prevents UsageLog's embedded MarshalJSON from hiding the
// administrative fields. They retain the same omission rules as the legacy DTO.
func (u AdminUsageLog) MarshalJSON() ([]byte, error) {
	data, err := u.UsageLog.MarshalJSON()
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if err = json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	admin := struct {
		RouteBillingSnapshot    *service.RouteBillingSnapshot `json:"route_billing_snapshot,omitempty"`
		UpstreamModel           *string                       `json:"upstream_model,omitempty"`
		UpstreamReasoningEffort *string                       `json:"upstream_reasoning_effort,omitempty"`
		UpstreamResponseModel   *string                       `json:"upstream_response_model,omitempty"`
		UpstreamModelMismatch   *bool                         `json:"upstream_model_mismatch,omitempty"`
		ChannelID               *int64                        `json:"channel_id,omitempty"`
		ModelMappingChain       *string                       `json:"model_mapping_chain,omitempty"`
		UpstreamRequestID       *string                       `json:"upstream_request_id,omitempty"`
		BillingTier             *string                       `json:"billing_tier,omitempty"`
		AccountRateMultiplier   *float64                      `json:"account_rate_multiplier"`
		AccountStatsCost        *float64                      `json:"account_stats_cost,omitempty"`
		IPAddress               *string                       `json:"ip_address,omitempty"`
		Account                 *AccountSummary               `json:"account,omitempty"`
	}{u.RouteBillingSnapshot, u.UpstreamModel, u.UpstreamReasoningEffort, u.UpstreamResponseModel, u.UpstreamModelMismatch, u.ChannelID, u.ModelMappingChain, u.UpstreamRequestID, u.BillingTier, u.AccountRateMultiplier, u.AccountStatsCost, u.IPAddress, u.Account}
	data, err = json.Marshal(admin)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}
