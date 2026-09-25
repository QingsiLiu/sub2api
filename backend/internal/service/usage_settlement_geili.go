package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

// UsageSettlementRepository is optional so legacy adapters and test doubles keep
// their small billing interface. Production repositories implement it.
type UsageSettlementRepository interface {
	PrepareSettlement(context.Context, *UsageBillingCommand, *UsageLog) error
	ProcessPendingSettlements(context.Context, int) ([]UsageSettlementApplied, error)
	DeliverSettledUsage(context.Context, int) (UsageSettlementDeliveryStats, error)
	SettlementHealth(context.Context) (UsageSettlementHealth, error)
}

type UsageSettlementApplied struct {
	Command *UsageBillingCommand
	Result  *UsageBillingApplyResult
}
type UsageSettlementDeliveryStats struct{ Claimed, Delivered, Failed int }
type UsageSettlementHealth struct {
	PartialLiveSettlements int64   `json:"partial_live_settlements"`
	PendingSettlements     int64   `json:"pending_settlements"`
	PendingDeliveries      int64   `json:"pending_deliveries"`
	FailedSettlements      int64   `json:"failed_settlements"`
	FailedDeliveries       int64   `json:"failed_deliveries"`
	OldestPendingSeconds   float64 `json:"oldest_pending_seconds"`
	Warning30Seconds       int64   `json:"warning_30_seconds"`
	Critical120Seconds     int64   `json:"critical_120_seconds"`
	AmountMismatchCount    int64   `json:"amount_mismatch_count"`
}

// UsageSettlementDetail is an explicit scalar whitelist. Do not embed UsageLog:
// its User/APIKey/Account associations can contain credentials and auth data.
// Null means unavailable, not a fabricated zero; historical partial records use
// nullable JSON directly and are never converted into full usage logs.
type UsageSettlementDetail struct {
	RouteBillingSnapshot      *RouteBillingSnapshot `json:"route_billing_snapshot"`
	UserID                    int64                 `json:"user_id"`
	APIKeyID                  int64                 `json:"api_key_id"`
	AccountID                 int64                 `json:"account_id"`
	RequestID                 string                `json:"request_id"`
	Model                     string                `json:"model"`
	RequestedModel            string                `json:"requested_model"`
	UpstreamModel             *string               `json:"upstream_model"`
	UpstreamResponseModel     *string               `json:"upstream_response_model"`
	UpstreamModelMismatch     *bool                 `json:"upstream_model_mismatch"`
	ChannelID                 *int64                `json:"channel_id"`
	ModelMappingChain         *string               `json:"model_mapping_chain"`
	BillingTier               *string               `json:"billing_tier"`
	BillingMode               *string               `json:"billing_mode"`
	ServiceTier               *string               `json:"service_tier"`
	ReasoningEffort           *string               `json:"reasoning_effort"`
	RequestedReasoningEffort  *string               `json:"requested_reasoning_effort"`
	InboundEndpoint           *string               `json:"inbound_endpoint"`
	UpstreamEndpoint          *string               `json:"upstream_endpoint"`
	GroupID                   *int64                `json:"group_id"`
	SubscriptionID            *int64                `json:"subscription_id"`
	InputTokens               int                   `json:"input_tokens"`
	OutputTokens              int                   `json:"output_tokens"`
	CacheCreationTokens       int                   `json:"cache_creation_tokens"`
	CacheReadTokens           int                   `json:"cache_read_tokens"`
	CacheCreation5mTokens     int                   `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens     int                   `json:"cache_creation_1h_tokens"`
	ImageInputTokens          int                   `json:"image_input_tokens"`
	ImageInputCost            float64               `json:"image_input_cost"`
	ImageOutputTokens         int                   `json:"image_output_tokens"`
	ImageOutputCost           float64               `json:"image_output_cost"`
	InputCost                 float64               `json:"input_cost"`
	OutputCost                float64               `json:"output_cost"`
	CacheCreationCost         float64               `json:"cache_creation_cost"`
	CacheReadCost             float64               `json:"cache_read_cost"`
	TotalCost                 float64               `json:"total_cost"`
	ActualCost                float64               `json:"actual_cost"`
	RateMultiplier            float64               `json:"rate_multiplier"`
	LongContextBillingApplied bool                  `json:"long_context_billing_applied"`
	AccountRateMultiplier     *float64              `json:"account_rate_multiplier"`
	AccountStatsCost          *float64              `json:"account_stats_cost"`
	BillingType               int8                  `json:"billing_type"`
	RequestType               RequestType           `json:"request_type"`
	Stream                    bool                  `json:"stream"`
	OpenAIWSMode              bool                  `json:"openai_ws_mode"`
	NativeCompactionV2        bool                  `json:"native_compaction_v2"`
	DurationMs                *int                  `json:"duration_ms"`
	FirstTokenMs              *int                  `json:"first_token_ms"`
	UserAgent                 *string               `json:"user_agent"`
	IPAddress                 *string               `json:"ip_address"`
	SessionID                 *string               `json:"session_id"`
	UpstreamRequestID         *string               `json:"upstream_request_id"`
	CacheTTLOverridden        bool                  `json:"cache_ttl_overridden"`
	ImageCount                int                   `json:"image_count"`
	ImageSize                 *string               `json:"image_size"`
	ImageInputSize            *string               `json:"image_input_size"`
	ImageOutputSize           *string               `json:"image_output_size"`
	ImageSizeSource           *string               `json:"image_size_source"`
	ImageSizeBreakdown        map[string]int        `json:"image_size_breakdown"`
	MediaType                 *string               `json:"media_type"`
	VideoCount                int                   `json:"video_count"`
	VideoResolution           *string               `json:"video_resolution"`
	VideoDurationSeconds      *int                  `json:"video_duration_seconds"`
	CreatedAt                 time.Time             `json:"created_at"`
}

func NewUsageSettlementDetail(log *UsageLog) (*UsageSettlementDetail, error) {
	if log == nil {
		return nil, nil
	}
	d := &UsageSettlementDetail{
		RouteBillingSnapshot:      log.RouteBillingSnapshot,
		UserID:                    log.UserID,
		APIKeyID:                  log.APIKeyID,
		AccountID:                 log.AccountID,
		RequestID:                 log.RequestID,
		Model:                     log.Model,
		RequestedModel:            log.RequestedModel,
		UpstreamModel:             log.UpstreamModel,
		UpstreamResponseModel:     log.UpstreamResponseModel,
		UpstreamModelMismatch:     log.UpstreamModelMismatch,
		ChannelID:                 log.ChannelID,
		ModelMappingChain:         log.ModelMappingChain,
		BillingTier:               log.BillingTier,
		BillingMode:               log.BillingMode,
		ServiceTier:               log.ServiceTier,
		ReasoningEffort:           log.ReasoningEffort,
		RequestedReasoningEffort:  log.RequestedReasoningEffort,
		InboundEndpoint:           log.InboundEndpoint,
		UpstreamEndpoint:          log.UpstreamEndpoint,
		GroupID:                   log.GroupID,
		SubscriptionID:            log.SubscriptionID,
		InputTokens:               log.InputTokens,
		OutputTokens:              log.OutputTokens,
		CacheCreationTokens:       log.CacheCreationTokens,
		CacheReadTokens:           log.CacheReadTokens,
		CacheCreation5mTokens:     log.CacheCreation5mTokens,
		CacheCreation1hTokens:     log.CacheCreation1hTokens,
		ImageInputTokens:          log.ImageInputTokens,
		ImageInputCost:            log.ImageInputCost,
		ImageOutputTokens:         log.ImageOutputTokens,
		ImageOutputCost:           log.ImageOutputCost,
		InputCost:                 log.InputCost,
		OutputCost:                log.OutputCost,
		CacheCreationCost:         log.CacheCreationCost,
		CacheReadCost:             log.CacheReadCost,
		TotalCost:                 log.TotalCost,
		ActualCost:                log.ActualCost,
		RateMultiplier:            log.RateMultiplier,
		LongContextBillingApplied: log.LongContextBillingApplied,
		AccountRateMultiplier:     log.AccountRateMultiplier,
		AccountStatsCost:          log.AccountStatsCost,
		BillingType:               log.BillingType,
		RequestType:               log.RequestType,
		Stream:                    log.Stream,
		OpenAIWSMode:              log.OpenAIWSMode,
		NativeCompactionV2:        log.NativeCompactionV2,
		DurationMs:                log.DurationMs,
		FirstTokenMs:              log.FirstTokenMs,
		UserAgent:                 log.UserAgent,
		IPAddress:                 log.IPAddress,
		SessionID:                 log.SessionID,
		UpstreamRequestID:         log.UpstreamRequestID,
		CacheTTLOverridden:        log.CacheTTLOverridden,
		ImageCount:                log.ImageCount,
		ImageSize:                 log.ImageSize,
		ImageInputSize:            log.ImageInputSize,
		ImageOutputSize:           log.ImageOutputSize,
		ImageSizeSource:           log.ImageSizeSource,
		ImageSizeBreakdown:        log.ImageSizeBreakdown,
		MediaType:                 log.MediaType,
		VideoCount:                log.VideoCount,
		VideoResolution:           log.VideoResolution,
		VideoDurationSeconds:      log.VideoDurationSeconds,
		CreatedAt:                 log.CreatedAt,
	}
	// Marshal/unmarshal also deep-copies maps and pointers, freezes the snapshot,
	// and rejects NaN/Inf before a request can become a persistent poison task.
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var frozen UsageSettlementDetail
	if err := json.Unmarshal(raw, &frozen); err != nil {
		return nil, err
	}
	return &frozen, nil
}

func (d *UsageSettlementDetail) UsageLog() *UsageLog {
	if d == nil {
		return nil
	}
	return &UsageLog{
		RouteBillingSnapshot:      d.RouteBillingSnapshot,
		UserID:                    d.UserID,
		APIKeyID:                  d.APIKeyID,
		AccountID:                 d.AccountID,
		RequestID:                 d.RequestID,
		Model:                     d.Model,
		RequestedModel:            d.RequestedModel,
		UpstreamModel:             d.UpstreamModel,
		UpstreamResponseModel:     d.UpstreamResponseModel,
		UpstreamModelMismatch:     d.UpstreamModelMismatch,
		ChannelID:                 d.ChannelID,
		ModelMappingChain:         d.ModelMappingChain,
		BillingTier:               d.BillingTier,
		BillingMode:               d.BillingMode,
		ServiceTier:               d.ServiceTier,
		ReasoningEffort:           d.ReasoningEffort,
		RequestedReasoningEffort:  d.RequestedReasoningEffort,
		InboundEndpoint:           d.InboundEndpoint,
		UpstreamEndpoint:          d.UpstreamEndpoint,
		GroupID:                   d.GroupID,
		SubscriptionID:            d.SubscriptionID,
		InputTokens:               d.InputTokens,
		OutputTokens:              d.OutputTokens,
		CacheCreationTokens:       d.CacheCreationTokens,
		CacheReadTokens:           d.CacheReadTokens,
		CacheCreation5mTokens:     d.CacheCreation5mTokens,
		CacheCreation1hTokens:     d.CacheCreation1hTokens,
		ImageInputTokens:          d.ImageInputTokens,
		ImageInputCost:            d.ImageInputCost,
		ImageOutputTokens:         d.ImageOutputTokens,
		ImageOutputCost:           d.ImageOutputCost,
		InputCost:                 d.InputCost,
		OutputCost:                d.OutputCost,
		CacheCreationCost:         d.CacheCreationCost,
		CacheReadCost:             d.CacheReadCost,
		TotalCost:                 d.TotalCost,
		ActualCost:                d.ActualCost,
		RateMultiplier:            d.RateMultiplier,
		LongContextBillingApplied: d.LongContextBillingApplied,
		AccountRateMultiplier:     d.AccountRateMultiplier,
		AccountStatsCost:          d.AccountStatsCost,
		BillingType:               d.BillingType,
		RequestType:               d.RequestType,
		Stream:                    d.Stream,
		OpenAIWSMode:              d.OpenAIWSMode,
		NativeCompactionV2:        d.NativeCompactionV2,
		DurationMs:                d.DurationMs,
		FirstTokenMs:              d.FirstTokenMs,
		UserAgent:                 d.UserAgent,
		IPAddress:                 d.IPAddress,
		SessionID:                 d.SessionID,
		UpstreamRequestID:         d.UpstreamRequestID,
		CacheTTLOverridden:        d.CacheTTLOverridden,
		ImageCount:                d.ImageCount,
		ImageSize:                 d.ImageSize,
		ImageInputSize:            d.ImageInputSize,
		ImageOutputSize:           d.ImageOutputSize,
		ImageSizeSource:           d.ImageSizeSource,
		ImageSizeBreakdown:        d.ImageSizeBreakdown,
		MediaType:                 d.MediaType,
		VideoCount:                d.VideoCount,
		VideoResolution:           d.VideoResolution,
		VideoDurationSeconds:      d.VideoDurationSeconds,
		CreatedAt:                 d.CreatedAt,
	}
}

func ValidateUsageSettlementCommand(c *UsageBillingCommand) error {
	if c == nil {
		return errors.New("nil usage settlement command")
	}
	if strings.TrimSpace(c.RequestID) == "" {
		return ErrUsageBillingRequestIDRequired
	}
	if c.UserID < 0 || c.APIKeyID < 0 || c.AccountID < 0 || (c.UserID == 0 && (c.BalanceCost > 0 || c.SubscriptionCost > 0 || c.PlatformQuotaCost > 0)) {
		return errors.New("invalid usage settlement identity")
	}
	for _, amount := range []float64{c.BalanceCost, c.SubscriptionCost, c.APIKeyQuotaCost, c.APIKeyRateLimitCost, c.AccountQuotaCost, c.PlatformQuotaCost} {
		if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
			return errors.New("invalid usage settlement monetary amount")
		}
	}
	if c.TerminalFailure && (c.BalanceCost != 0 || c.SubscriptionCost != 0 || c.APIKeyQuotaCost != 0 || c.APIKeyRateLimitCost != 0 || c.AccountQuotaCost != 0 || c.PlatformQuotaCost != 0 || c.UsageDetail != nil) {
		return errors.New("terminal failure must have no charge or usage detail")
	}
	if c.BalanceCost > 0 && c.SubscriptionCost > 0 {
		return errors.New("mixed balance and subscription settlement")
	}
	if c.SubscriptionCost > 0 && c.SubscriptionID == nil {
		return errors.New("subscription settlement identity missing")
	}
	return nil
}
