package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestActiveOfficialPricingUsesCurrentDeepSeekVersionBeforeAugust17(t *testing.T) {
	version := activeOfficialPricing("deepseek-v4-flash", time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
	require.NotNil(t, version)
	require.Equal(t, "DeepSeek-V4-Flash-0731", version.ReferenceModel)
	require.Equal(t, "1", version.Items["input"].String())
	require.Equal(t, "2", version.Items["output"].String())
	require.Equal(t, "0.02", version.Items["cache_read"].String())
	require.Equal(t, "2026-08-17", version.EffectiveUntil.Format("2006-01-02"))
	require.Empty(t, version.RatePeriod)
}

func TestActiveOfficialPricingSwitchesDeepSeekVersionOnAugust17BeijingTime(t *testing.T) {
	flash := activeOfficialPricing("deepseek-v4-flash", time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	require.NotNil(t, flash)
	require.Equal(t, "1.5", flash.Items["input"].String())
	require.Equal(t, "4.5", flash.Items["output"].String())
	require.Equal(t, "0.05", flash.Items["cache_read"].String())
	require.Equal(t, officialRatePeriodOffPeak, flash.RatePeriod)

	pro := activeOfficialPricing("deepseek-v4-pro", time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	require.NotNil(t, pro)
	require.Equal(t, "4.5", pro.Items["input"].String())
	require.Equal(t, "13.5", pro.Items["output"].String())
	require.Equal(t, "0.15", pro.Items["cache_read"].String())
	require.Equal(t, officialRatePeriodOffPeak, pro.RatePeriod)
}

func TestActiveOfficialPricingUsesOfficialBeijingEffectiveMomentFromLosAngeles(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	beforeBeijingMidnight := activeOfficialPricing(
		"deepseek-v4-flash",
		time.Date(2026, 8, 16, 8, 59, 59, 0, losAngeles),
	)
	require.NotNil(t, beforeBeijingMidnight)
	require.Equal(t, "1", beforeBeijingMidnight.Items["input"].String())

	atBeijingMidnight := activeOfficialPricing(
		"deepseek-v4-flash",
		time.Date(2026, 8, 16, 9, 0, 0, 0, losAngeles),
	)
	require.NotNil(t, atBeijingMidnight)
	require.Equal(t, "1.5", atBeijingMidnight.Items["input"].String())
	require.Equal(t, officialRatePeriodOffPeak, atBeijingMidnight.RatePeriod)
}

func TestActiveOfficialPricingUsesDeepSeekPeakWindowsInBeijingTime(t *testing.T) {
	tests := []struct {
		hour       int
		minute     int
		wantPeriod string
		wantInput  string
	}{
		{hour: 8, minute: 59, wantPeriod: officialRatePeriodOffPeak, wantInput: "1.5"},
		{hour: 9, minute: 0, wantPeriod: officialRatePeriodPeak, wantInput: "3"},
		{hour: 11, minute: 59, wantPeriod: officialRatePeriodPeak, wantInput: "3"},
		{hour: 12, minute: 0, wantPeriod: officialRatePeriodOffPeak, wantInput: "1.5"},
		{hour: 13, minute: 59, wantPeriod: officialRatePeriodOffPeak, wantInput: "1.5"},
		{hour: 14, minute: 0, wantPeriod: officialRatePeriodPeak, wantInput: "3"},
		{hour: 17, minute: 59, wantPeriod: officialRatePeriodPeak, wantInput: "3"},
		{hour: 18, minute: 0, wantPeriod: officialRatePeriodOffPeak, wantInput: "1.5"},
	}

	for _, tt := range tests {
		at := time.Date(2026, 8, 17, tt.hour, tt.minute, 0, 0, deepSeekPricingLocation)
		version := activeOfficialPricing("deepseek-v4-flash", at)
		require.NotNil(t, version)
		require.Equal(t, tt.wantPeriod, version.RatePeriod, "%02d:%02d", tt.hour, tt.minute)
		require.Equal(t, tt.wantInput, version.Items["input"].String(), "%02d:%02d", tt.hour, tt.minute)
	}
}

func TestOfficialCatalogContainsVerifiedCurrentProviderPrices(t *testing.T) {
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	terra := activeOfficialPricing("gpt-5.6-terra", at)
	require.NotNil(t, terra)
	require.Equal(t, "2", terra.Items["input"].String())
	require.Equal(t, "12", terra.Items["output"].String())
	require.Equal(t, "0.2", terra.Items["cache_read"].String())

	luna := activeOfficialPricing("gpt-5.6-luna", at)
	require.NotNil(t, luna)
	require.Equal(t, "0.2", luna.Items["input"].String())
	require.Equal(t, "1.2", luna.Items["output"].String())

	gpt55 := activeOfficialPricing("gpt-5.5", at)
	require.NotNil(t, gpt55)
	require.Equal(t, "30", gpt55.Items["output"].String())
	require.Equal(t, "45", gpt55.Items["output:long_context"].String())
	require.Equal(t, "2026-04-24", gpt55.EffectiveFrom.Format("2006-01-02"))

	fable := activeOfficialPricing("claude-fable-5", at)
	require.NotNil(t, fable)
	require.Equal(t, "10", fable.Items["input"].String())
	require.Equal(t, "50", fable.Items["output"].String())
	require.Equal(t, "1", fable.Items["cache_read"].String())

	sonnet := activeOfficialPricing("claude-sonnet-5", at)
	require.NotNil(t, sonnet)
	require.Equal(t, "2", sonnet.Items["input"].String())
	require.Equal(t, "10", sonnet.Items["output"].String())
	require.Equal(t, "0.2", sonnet.Items["cache_read"].String())

	gemini := activeOfficialPricing("gemini-3.5-flash", at)
	require.NotNil(t, gemini)
	require.Equal(t, "1.5", gemini.Items["input"].String())
	require.Equal(t, "9", gemini.Items["output"].String())
	require.Equal(t, "0.15", gemini.Items["cache_read"].String())

	grok := activeOfficialPricing("grok-4.5", at)
	require.NotNil(t, grok)
	require.Equal(t, "2", grok.Items["input"].String())
	require.Equal(t, "6", grok.Items["output"].String())
	require.Equal(t, "0.3", grok.Items["cache_read"].String())
}

func TestActiveOfficialPricingDoesNotGuessInternalAliases(t *testing.T) {
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	for _, model := range []string{"kimi-k3", "glm-5.2", "qwen3.8-max"} {
		require.Nil(t, activeOfficialPricing(model, at), model)
	}
}

func TestOfficialPriceForSpecRequiresAnExactComparableItem(t *testing.T) {
	version := activeOfficialPricing("gpt-5.4", time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC))
	require.NotNil(t, version)

	price, ok := officialPriceForSpec(version, publicPricingItemSpec{Key: "input:long_context", Kind: "input"})
	require.True(t, ok)
	require.Equal(t, "5", price.String())

	_, ok = officialPriceForSpec(version, publicPricingItemSpec{Key: "cache_write", Kind: "cache_write"})
	require.False(t, ok)

	_, ok = officialPriceForSpec(version, publicPricingItemSpec{Key: "input:interval:128000-inf", Kind: "input"})
	require.False(t, ok)
}
