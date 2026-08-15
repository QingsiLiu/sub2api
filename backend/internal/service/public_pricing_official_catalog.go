package service

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	pricingComparisonExact       = "exact"
	pricingComparisonUnavailable = "unavailable"
	officialRatePeriodPeak       = "peak"
	officialRatePeriodOffPeak    = "off_peak"
)

type publicPricingModelDefinition struct {
	DisplayName string
	Provider    string
}

var publicPricingModelDefinitions = map[string]publicPricingModelDefinition{
	"gpt-5.5":                   {DisplayName: "GPT-5.5", Provider: "openai"},
	"gpt-5.6":                   {DisplayName: "GPT-5.6", Provider: "openai"},
	"gpt-5.6-sol":               {DisplayName: "GPT-5.6 Sol", Provider: "openai"},
	"gpt-5.6-luna":              {DisplayName: "GPT-5.6 Luna", Provider: "openai"},
	"gpt-5.6-terra":             {DisplayName: "GPT-5.6 Terra", Provider: "openai"},
	"gpt-5.4":                   {DisplayName: "GPT-5.4", Provider: "openai"},
	"gpt-5.4-mini":              {DisplayName: "GPT-5.4 Mini", Provider: "openai"},
	"claude-opus-4-8":           {DisplayName: "Claude Opus 4.8", Provider: "anthropic"},
	"claude-fable-5":            {DisplayName: "Claude Fable 5", Provider: "anthropic"},
	"claude-opus-5":             {DisplayName: "Claude Opus 5", Provider: "anthropic"},
	"claude-sonnet-5":           {DisplayName: "Claude Sonnet 5", Provider: "anthropic"},
	"claude-opus-4-7":           {DisplayName: "Claude Opus 4.7", Provider: "anthropic"},
	"claude-sonnet-4-6":         {DisplayName: "Claude Sonnet 4.6", Provider: "anthropic"},
	"claude-haiku-4-5-20251001": {DisplayName: "Claude Haiku 4.5", Provider: "anthropic"},
	"gemini-3.5-flash":          {DisplayName: "Gemini 3.5 Flash", Provider: "gemini"},
	"gemini-3.1-flash-lite":     {DisplayName: "Gemini 3.1 Flash-Lite", Provider: "gemini"},
	"gemini-3.1-pro-preview":    {DisplayName: "Gemini 3.1 Pro Preview", Provider: "gemini"},
	"grok-4.5":                  {DisplayName: "Grok 4.5", Provider: "grok"},
	"deepseek-v4-flash":         {DisplayName: "DeepSeek V4 Flash", Provider: "cn"},
	"deepseek-v4-pro":           {DisplayName: "DeepSeek V4 Pro", Provider: "cn"},
	"kimi-k3":                   {DisplayName: "Kimi K3", Provider: "cn"},
	"glm-5.2":                   {DisplayName: "GLM-5.2", Provider: "cn"},
	"qwen3.8-max":               {DisplayName: "Qwen 3.8 Max", Provider: "cn"},
}

type officialPricingVersion struct {
	ModelID        string
	ReferenceModel string
	Source         string
	Currency       string
	EffectiveFrom  time.Time
	EffectiveUntil *time.Time
	Location       *time.Location
	RatePeriod     string
	DailyWindows   []officialPricingDailyWindow
	OutsideWindows bool
	Items          map[string]decimal.Decimal
}

type officialPricingDailyWindow struct {
	StartMinute int
	EndMinute   int
}

func officialVersion(model, reference, source, currency, from, until string, items map[string]string) officialPricingVersion {
	version := officialPricingVersion{
		ModelID:        model,
		ReferenceModel: reference,
		Source:         source,
		Currency:       currency,
		EffectiveFrom:  mustPricingDate(from),
		Items:          make(map[string]decimal.Decimal, len(items)),
	}
	if strings.TrimSpace(until) != "" {
		parsed := mustPricingDate(until)
		version.EffectiveUntil = &parsed
	}
	for key, value := range items {
		version.Items[key] = decimal.RequireFromString(value)
	}
	return version
}

func officialScheduledVersion(
	model, reference, source, currency, from, until string,
	location *time.Location,
	ratePeriod string,
	windows []officialPricingDailyWindow,
	outsideWindows bool,
	items map[string]string,
) officialPricingVersion {
	version := officialVersion(model, reference, source, currency, from, until, items)
	version.Location = location
	version.RatePeriod = ratePeriod
	version.DailyWindows = append([]officialPricingDailyWindow(nil), windows...)
	version.OutsideWindows = outsideWindows
	return version
}

func mustPricingDate(value string) time.Time {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		panic(err)
	}
	return parsed
}

var deepSeekPricingLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

var deepSeekPeakPricingWindows = []officialPricingDailyWindow{
	{StartMinute: 9 * 60, EndMinute: 12 * 60},
	{StartMinute: 14 * 60, EndMinute: 18 * 60},
}

var officialPublicPricingCatalog = []officialPricingVersion{
	// OpenAI official API pricing. Units are USD per 1M tokens unless the item is request:*.
	officialVersion("gpt-5.5", "gpt-5.5", "https://developers.openai.com/api/docs/models/gpt-5.5", "USD", "2026-04-24", "", map[string]string{
		"input": "5", "output": "30", "cache_read": "0.5",
		"input:long_context": "10", "output:long_context": "45", "cache_read:long_context": "1",
	}),
	officialVersion("gpt-5.6", "gpt-5.6-sol", "https://developers.openai.com/api/docs/pricing", "USD", "2026-07-09", "", map[string]string{
		"input": "5", "output": "30", "cache_read": "0.5", "cache_write": "6.25",
		"input:long_context": "10", "output:long_context": "45", "cache_read:long_context": "1", "cache_write:long_context": "12.5",
	}),
	officialVersion("gpt-5.6-sol", "gpt-5.6-sol", "https://developers.openai.com/api/docs/pricing", "USD", "2026-07-09", "", map[string]string{
		"input": "5", "output": "30", "cache_read": "0.5", "cache_write": "6.25",
		"input:long_context": "10", "output:long_context": "45", "cache_read:long_context": "1", "cache_write:long_context": "12.5",
	}),
	officialVersion("gpt-5.6-terra", "gpt-5.6-terra", "https://developers.openai.com/api/docs/pricing", "USD", "2026-07-30", "", map[string]string{
		"input": "2", "output": "12", "cache_read": "0.2", "cache_write": "2.5",
		"input:long_context": "4", "output:long_context": "18", "cache_read:long_context": "0.4", "cache_write:long_context": "5",
	}),
	officialVersion("gpt-5.6-luna", "gpt-5.6-luna", "https://developers.openai.com/api/docs/pricing", "USD", "2026-07-30", "", map[string]string{
		"input": "0.2", "output": "1.2", "cache_read": "0.02", "cache_write": "0.25",
		"input:long_context": "0.4", "output:long_context": "1.8", "cache_read:long_context": "0.04", "cache_write:long_context": "0.5",
	}),
	officialVersion("gpt-5.4", "gpt-5.4", "https://developers.openai.com/api/docs/models/gpt-5.4", "USD", "2026-03-05", "", map[string]string{
		"input": "2.5", "output": "15", "cache_read": "0.25",
		"input:long_context": "5", "output:long_context": "22.5", "cache_read:long_context": "0.5",
	}),
	officialVersion("gpt-5.4-mini", "gpt-5.4-mini", "https://developers.openai.com/api/docs/models/gpt-5.4-mini", "USD", "2026-03-17", "", map[string]string{
		"input": "0.75", "output": "4.5", "cache_read": "0.075",
	}),

	// Anthropic standard token pricing. Cache-write keys distinguish 5m and 1h TTLs.
	officialVersion("claude-opus-4-8", "claude-opus-4-8", "https://docs.anthropic.com/en/docs/about-claude/pricing", "USD", "2026-05-28", "", map[string]string{
		"input": "5", "output": "25", "cache_write_5m": "6.25", "cache_write_1h": "10", "cache_read": "0.5",
	}),
	officialVersion("claude-opus-5", "claude-opus-5", "https://docs.anthropic.com/en/docs/about-claude/pricing", "USD", "2026-07-24", "", map[string]string{
		"input": "5", "output": "25", "cache_write_5m": "6.25", "cache_write_1h": "10", "cache_read": "0.5",
	}),
	officialVersion("claude-opus-4-7", "claude-opus-4-7", "https://docs.anthropic.com/en/docs/about-claude/pricing", "USD", "2026-04-16", "", map[string]string{
		"input": "5", "output": "25", "cache_write_5m": "6.25", "cache_write_1h": "10", "cache_read": "0.5",
	}),
	officialVersion("claude-fable-5", "claude-fable-5", "https://docs.anthropic.com/en/docs/about-claude/pricing", "USD", "2026-06-09", "", map[string]string{
		"input": "10", "output": "50", "cache_write_5m": "12.5", "cache_write_1h": "20", "cache_read": "1",
	}),
	officialVersion("claude-sonnet-5", "claude-sonnet-5", "https://docs.anthropic.com/en/docs/about-claude/pricing", "USD", "2026-06-30", "", map[string]string{
		"input": "2", "output": "10", "cache_write_5m": "2.5", "cache_write_1h": "4", "cache_read": "0.2",
	}),
	officialVersion("claude-sonnet-4-6", "claude-sonnet-4-6", "https://docs.anthropic.com/en/docs/about-claude/pricing", "USD", "2026-02-17", "", map[string]string{
		"input": "3", "output": "15", "cache_write_5m": "3.75", "cache_write_1h": "6", "cache_read": "0.3",
	}),
	officialVersion("claude-haiku-4-5-20251001", "claude-haiku-4-5", "https://docs.anthropic.com/en/docs/about-claude/pricing", "USD", "2025-10-15", "", map[string]string{
		"input": "1", "output": "5", "cache_write_5m": "1.25", "cache_write_1h": "2", "cache_read": "0.1",
	}),

	// Google Gemini pricing. Long-context item keys are used when the resolver exposes a tier.
	officialVersion("gemini-3.1-pro-preview", "gemini-3.1-pro-preview", "https://ai.google.dev/gemini-api/docs/pricing", "USD", "2026-02-19", "", map[string]string{
		"input": "2", "output": "12", "cache_read": "0.2",
		"input:long_context": "4", "output:long_context": "18", "cache_read:long_context": "0.4",
	}),
	officialVersion("gemini-3.5-flash", "gemini-3.5-flash", "https://ai.google.dev/gemini-api/docs/pricing", "USD", "2026-05-19", "", map[string]string{
		"input": "1.5", "output": "9", "cache_read": "0.15",
	}),
	officialVersion("gemini-3.1-flash-lite", "gemini-3.1-flash-lite", "https://ai.google.dev/gemini-api/docs/pricing", "USD", "2026-05-07", "", map[string]string{
		"input": "0.25", "output": "1.5", "cache_read": "0.025",
	}),

	officialVersion("grok-4.5", "grok-4.5", "https://docs.x.ai/developers/models/grok-4.5", "USD", "2026-07-08", "", map[string]string{
		"input": "2", "output": "6", "cache_read": "0.3",
		"input:long_context": "4", "output:long_context": "12", "cache_read:long_context": "0.6",
	}),

	// DeepSeek native-CNY versions. The 2026-08-17 change is defined in
	// Beijing time and alternates between official peak and off-peak prices.
	officialScheduledVersion("deepseek-v4-flash", "DeepSeek-V4-Flash-0731", "https://api-docs.deepseek.com/zh-cn/quick_start/pricing/", "CNY", "2026-07-31", "2026-08-17", deepSeekPricingLocation, "", nil, false, map[string]string{
		"input": "1", "output": "2", "cache_read": "0.02",
	}),
	officialScheduledVersion("deepseek-v4-flash", "DeepSeek-V4-Flash-0731", "https://api-docs.deepseek.com/zh-cn/quick_start/pricing/", "CNY", "2026-08-17", "", deepSeekPricingLocation, officialRatePeriodPeak, deepSeekPeakPricingWindows, false, map[string]string{
		"input": "3", "output": "9", "cache_read": "0.1",
	}),
	officialScheduledVersion("deepseek-v4-flash", "DeepSeek-V4-Flash-0731", "https://api-docs.deepseek.com/zh-cn/quick_start/pricing/", "CNY", "2026-08-17", "", deepSeekPricingLocation, officialRatePeriodOffPeak, deepSeekPeakPricingWindows, true, map[string]string{
		"input": "1.5", "output": "4.5", "cache_read": "0.05",
	}),
	officialScheduledVersion("deepseek-v4-pro", "DeepSeek-V4-Pro-0813", "https://api-docs.deepseek.com/zh-cn/quick_start/pricing/", "CNY", "2026-08-13", "2026-08-17", deepSeekPricingLocation, "", nil, false, map[string]string{
		"input": "3", "output": "6", "cache_read": "0.025",
	}),
	officialScheduledVersion("deepseek-v4-pro", "DeepSeek-V4-Pro-0813", "https://api-docs.deepseek.com/zh-cn/quick_start/pricing/", "CNY", "2026-08-17", "", deepSeekPricingLocation, officialRatePeriodPeak, deepSeekPeakPricingWindows, false, map[string]string{
		"input": "9", "output": "27", "cache_read": "0.3",
	}),
	officialScheduledVersion("deepseek-v4-pro", "DeepSeek-V4-Pro-0813", "https://api-docs.deepseek.com/zh-cn/quick_start/pricing/", "CNY", "2026-08-17", "", deepSeekPricingLocation, officialRatePeriodOffPeak, deepSeekPeakPricingWindows, true, map[string]string{
		"input": "4.5", "output": "13.5", "cache_read": "0.15",
	}),
}

func activeOfficialPricing(modelID string, at time.Time) *officialPricingVersion {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	for i := range officialPublicPricingCatalog {
		version := &officialPublicPricingCatalog[i]
		if version.ModelID != modelID || !version.activeAt(at) {
			continue
		}
		return version
	}
	return nil
}

func (v *officialPricingVersion) activeAt(at time.Time) bool {
	if v == nil {
		return false
	}
	local := at
	if v.Location != nil {
		local = at.In(v.Location)
	}
	atDate := local.Format("2006-01-02")
	if atDate < v.EffectiveFrom.Format("2006-01-02") {
		return false
	}
	if v.EffectiveUntil != nil && atDate >= v.EffectiveUntil.Format("2006-01-02") {
		return false
	}
	if len(v.DailyWindows) == 0 {
		return true
	}
	minute := local.Hour()*60 + local.Minute()
	inWindow := false
	for _, window := range v.DailyWindows {
		if minute >= window.StartMinute && minute < window.EndMinute {
			inWindow = true
			break
		}
	}
	if v.OutsideWindows {
		return !inWindow
	}
	return inWindow
}
