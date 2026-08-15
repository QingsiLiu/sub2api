package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	SettingKeyPublicPricingEnabled           = "public_pricing_enabled"
	SettingKeyPublicPricingGroupIDs          = "public_pricing_group_ids"
	SettingKeyPublicPricingModelIDs          = "public_pricing_model_ids"
	SettingKeyPublicPricingNativeCNYGroupIDs = "public_pricing_native_cny_group_ids"
	SettingKeyPublicPricingQuotaUSDPerCNY    = "public_pricing_quota_usd_per_cny"
	SettingKeyPublicPricingUSDCNYReference   = "public_pricing_usd_cny_reference"
	SettingKeyPublicPricingFXSource          = "public_pricing_fx_source"
	SettingKeyPublicPricingFXEffectiveDate   = "public_pricing_fx_effective_date"
)

var defaultPublicPricingGroupIDs = []int64{4, 27, 80, 47, 46, 41, 67, 82}

var excludedPublicPricingGroupIDs = map[int64]struct{}{34: {}, 63: {}}

var defaultPublicPricingModelIDs = []string{
	"gpt-5.5", "gpt-5.6", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-5.6-terra", "gpt-5.4", "gpt-5.4-mini",
	"claude-opus-4-8", "claude-fable-5", "claude-opus-5", "claude-sonnet-5", "claude-opus-4-7", "claude-sonnet-4-6", "claude-haiku-4-5-20251001",
	"gemini-3.5-flash", "gemini-3.1-flash-lite", "gemini-3.1-pro-preview",
	"grok-4.5",
	"deepseek-v4-flash", "deepseek-v4-pro", "kimi-k3", "glm-5.2", "qwen3.8-max",
}

var excludedPublicPricingModelIDs = map[string]struct{}{"gpt-image-2": {}}

const (
	defaultPublicPricingUSDCNYReference = "6.8"
	defaultPublicPricingFXSource        = "运营固定对比汇率（非实时牌价）"
	defaultPublicPricingFXEffectiveDate = "2026-08-14"
)

// PublicPricingRuntime is the validated runtime configuration for the public
// pricing catalog. Amounts are decimals so no exchange-rate or savings
// calculation needs to pass through a frontend float.
type PublicPricingRuntime struct {
	Enabled           bool
	GroupIDs          []int64
	ModelIDs          []string
	NativeCNYGroupIDs map[int64]struct{}
	QuotaUSDPerCNY    decimal.Decimal
	USDCNYReference   *decimal.Decimal
	FXSource          string
	FXEffectiveDate   string
}

func (r PublicPricingRuntime) HasOfficialCNYComparison() bool {
	return r.USDCNYReference != nil && !r.USDCNYReference.IsZero() &&
		strings.TrimSpace(r.FXSource) != "" && strings.TrimSpace(r.FXEffectiveDate) != ""
}

// GetPublicPricingRuntime reads pricing settings directly so operational
// changes take effect without a process restart. Missing keys use the explicit
// launch whitelist; malformed values fail closed instead of publishing a
// misleading catalog.
func (s *SettingService) GetPublicPricingRuntime(ctx context.Context) (PublicPricingRuntime, error) {
	keys := []string{
		SettingKeyPublicPricingEnabled,
		SettingKeyPublicPricingGroupIDs,
		SettingKeyPublicPricingModelIDs,
		SettingKeyPublicPricingNativeCNYGroupIDs,
		SettingKeyPublicPricingQuotaUSDPerCNY,
		SettingKeyPublicPricingUSDCNYReference,
		SettingKeyPublicPricingFXSource,
		SettingKeyPublicPricingFXEffectiveDate,
	}
	values, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return PublicPricingRuntime{}, fmt.Errorf("load public pricing settings: %w", err)
	}

	enabled := true
	if raw, ok := values[SettingKeyPublicPricingEnabled]; ok {
		parsed, parseErr := strconv.ParseBool(strings.TrimSpace(raw))
		if parseErr != nil {
			return PublicPricingRuntime{}, fmt.Errorf("parse %s: %w", SettingKeyPublicPricingEnabled, parseErr)
		}
		enabled = parsed
	}

	groupIDs, err := parsePublicPricingInt64List(values[SettingKeyPublicPricingGroupIDs], defaultPublicPricingGroupIDs)
	if err != nil {
		return PublicPricingRuntime{}, fmt.Errorf("parse %s: %w", SettingKeyPublicPricingGroupIDs, err)
	}
	modelIDs, err := parsePublicPricingStringList(values[SettingKeyPublicPricingModelIDs], defaultPublicPricingModelIDs)
	if err != nil {
		return PublicPricingRuntime{}, fmt.Errorf("parse %s: %w", SettingKeyPublicPricingModelIDs, err)
	}
	groupIDs = filterPublicPricingGroupIDs(groupIDs)
	modelIDs = filterPublicPricingModelIDs(modelIDs)
	nativeIDs, err := parsePublicPricingInt64List(values[SettingKeyPublicPricingNativeCNYGroupIDs], []int64{82})
	if err != nil {
		return PublicPricingRuntime{}, fmt.Errorf("parse %s: %w", SettingKeyPublicPricingNativeCNYGroupIDs, err)
	}

	quota := decimal.NewFromInt(1)
	if raw := strings.TrimSpace(values[SettingKeyPublicPricingQuotaUSDPerCNY]); raw != "" {
		quota, err = decimal.NewFromString(raw)
		if err != nil || quota.LessThanOrEqual(decimal.Zero) {
			return PublicPricingRuntime{}, fmt.Errorf("%s must be a positive decimal", SettingKeyPublicPricingQuotaUSDPerCNY)
		}
	}

	var fx *decimal.Decimal
	if raw, exists := values[SettingKeyPublicPricingUSDCNYReference]; !exists {
		parsed := decimal.RequireFromString(defaultPublicPricingUSDCNYReference)
		fx = &parsed
	} else if raw = strings.TrimSpace(raw); raw != "" {
		parsed, parseErr := decimal.NewFromString(raw)
		if parseErr != nil || parsed.LessThanOrEqual(decimal.Zero) {
			return PublicPricingRuntime{}, fmt.Errorf("%s must be a positive decimal", SettingKeyPublicPricingUSDCNYReference)
		}
		fx = &parsed
	}

	fxSource := defaultPublicPricingFXSource
	if raw, exists := values[SettingKeyPublicPricingFXSource]; exists {
		fxSource = strings.TrimSpace(raw)
	}
	fxEffectiveDate := defaultPublicPricingFXEffectiveDate
	if raw, exists := values[SettingKeyPublicPricingFXEffectiveDate]; exists {
		fxEffectiveDate = strings.TrimSpace(raw)
	}
	if fxEffectiveDate != "" {
		if _, parseErr := time.Parse("2006-01-02", fxEffectiveDate); parseErr != nil {
			return PublicPricingRuntime{}, fmt.Errorf("%s must use YYYY-MM-DD format", SettingKeyPublicPricingFXEffectiveDate)
		}
	}

	nativeSet := make(map[int64]struct{}, len(nativeIDs))
	for _, id := range nativeIDs {
		nativeSet[id] = struct{}{}
	}

	return PublicPricingRuntime{
		Enabled:           enabled,
		GroupIDs:          groupIDs,
		ModelIDs:          modelIDs,
		NativeCNYGroupIDs: nativeSet,
		QuotaUSDPerCNY:    quota,
		USDCNYReference:   fx,
		FXSource:          fxSource,
		FXEffectiveDate:   fxEffectiveDate,
	}, nil
}

func filterPublicPricingGroupIDs(ids []int64) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, excluded := excludedPublicPricingGroupIDs[id]; !excluded {
			out = append(out, id)
		}
	}
	return out
}

func filterPublicPricingModelIDs(models []string) []string {
	out := make([]string, 0, len(models))
	for _, model := range models {
		if _, excluded := excludedPublicPricingModelIDs[model]; !excluded {
			out = append(out, model)
		}
	}
	return out
}

func parsePublicPricingInt64List(raw string, fallback []int64) ([]int64, error) {
	if strings.TrimSpace(raw) == "" {
		return append([]int64(nil), fallback...), nil
	}
	var ids []int64
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		parts := strings.Split(raw, ",")
		ids = make([]int64, 0, len(parts))
		for _, part := range parts {
			id, parseErr := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if parseErr != nil || id <= 0 {
				return nil, fmt.Errorf("invalid group id %q", part)
			}
			ids = append(ids, id)
		}
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("group ids must be positive")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func parsePublicPricingStringList(raw string, fallback []string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return append([]string(nil), fallback...), nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		values = strings.Split(raw, ",")
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("model list must not be empty")
	}
	return out, nil
}
