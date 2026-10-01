//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func f64(v float64) *float64 { return &v }

func TestNormalizeModelPlazaGeiliConfig(t *testing.T) {
	t.Run("defaults fill rates and keep lists non-nil", func(t *testing.T) {
		got, err := NormalizeModelPlazaGeiliConfig(ModelPlazaGeiliConfig{})
		require.NoError(t, err)
		require.Equal(t, 6.8, got.USDCNYRate)
		require.Equal(t, 1.0, got.QuotaUSDPerCNY)
		require.NotNil(t, got.GroupWhitelist)
		require.Empty(t, got.GroupWhitelist)
		require.NotNil(t, got.OfficialOverrides)
	})

	t.Run("dedupes ids and canonicalizes overrides", func(t *testing.T) {
		got, err := NormalizeModelPlazaGeiliConfig(ModelPlazaGeiliConfig{
			GroupWhitelist: []int64{4, 4, 27},
			CNYGroupIDs:    []int64{86, 86},
			OfficialOverrides: []ModelPlazaOfficialOverride{
				{Model: "  DeepSeek-V4 ", Currency: "CNY", Input: f64(4), Note: " 高峰价 "},
				{Model: "gpt-x"},
			},
		})
		require.NoError(t, err)
		require.Equal(t, []int64{4, 27}, got.GroupWhitelist)
		require.Equal(t, []int64{86}, got.CNYGroupIDs)
		require.Equal(t, "DeepSeek-V4", got.OfficialOverrides[0].Model)
		require.Equal(t, ModelPlazaCurrencyCNY, got.OfficialOverrides[0].Currency)
		require.Equal(t, "高峰价", got.OfficialOverrides[0].Note)
		require.Equal(t, ModelPlazaCurrencyUSD, got.OfficialOverrides[1].Currency)
	})

	bad := map[string]ModelPlazaGeiliConfig{
		"zero group id":      {GroupWhitelist: []int64{0}},
		"negative group id":  {CNYGroupIDs: []int64{-1}},
		"negative rate":      {USDCNYRate: -1},
		"empty model":        {OfficialOverrides: []ModelPlazaOfficialOverride{{Model: " "}}},
		"negative price":     {OfficialOverrides: []ModelPlazaOfficialOverride{{Model: "m", Input: f64(-1)}}},
		"unknown currency":   {OfficialOverrides: []ModelPlazaOfficialOverride{{Model: "m", Currency: "eur"}}},
		"duplicate override": {OfficialOverrides: []ModelPlazaOfficialOverride{{Model: "m"}, {Model: "M"}}},
	}
	for name, cfg := range bad {
		t.Run("rejects "+name, func(t *testing.T) {
			_, err := NormalizeModelPlazaGeiliConfig(cfg)
			require.Error(t, err)
		})
	}
}

func TestParseModelPlazaGeiliConfigFailsClosed(t *testing.T) {
	for _, raw := range []string{"", "not json", `{"group_whitelist":[0]}`} {
		got := ParseModelPlazaGeiliConfig(raw)
		require.Empty(t, got.GroupWhitelist, raw)
		require.Equal(t, 6.8, got.USDCNYRate, raw)
	}
	got := ParseModelPlazaGeiliConfig(`{"group_whitelist":[4,27],"cny_group_ids":[86]}`)
	require.Equal(t, []int64{4, 27}, got.GroupWhitelist)
	require.Equal(t, ModelPlazaCurrencyCNY, got.PlazaGroupCurrency(86))
	require.Equal(t, ModelPlazaCurrencyUSD, got.PlazaGroupCurrency(4))
}

func TestListVisibleGroupsWithConfig_WhitelistFiltersBeforeDiscovery(t *testing.T) {
	groups := []Group{
		{ID: 10, Name: "listed", Platform: PlatformOpenAI},
		{ID: 30, Name: "internal", Platform: PlatformOpenAI},
	}
	newSvc := func() (*ModelPlazaService, *plazaLiveCatalogStub) {
		svc := newPlazaService(nil, groups, nil)
		stub := &plazaLiveCatalogStub{models: map[int64][]GroupCatalogModel{
			10: {{Name: "m1", Platform: PlatformOpenAI}},
			30: {{Name: "m2", Platform: PlatformOpenAI}},
		}}
		svc.catalog = stub
		return svc, stub
	}

	svc, stub := newSvc()
	out, err := svc.ListVisibleGroupsWithConfig(context.Background(), nil, false, &ModelPlazaGeiliConfig{GroupWhitelist: []int64{10}})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, int64(10), out[0].ID)
	require.Equal(t, []int64{10}, stub.calls, "non-whitelisted group must never be contacted")

	// 空白名单 = 不展示任何分组（fail-closed）。
	svc, stub = newSvc()
	empty := DefaultModelPlazaGeiliConfig()
	out, err = svc.ListVisibleGroupsWithConfig(context.Background(), nil, false, &empty)
	require.NoError(t, err)
	require.Empty(t, out)
	require.Empty(t, stub.calls)

	// 白名单不放宽专属分组：匿名仍看不到。
	exclusive := []Group{{ID: 20, Name: "private", Platform: PlatformOpenAI, IsExclusive: true}}
	svc = newPlazaService(nil, exclusive, nil)
	svc.catalog = &plazaLiveCatalogStub{models: map[int64][]GroupCatalogModel{20: {{Name: "m", Platform: PlatformOpenAI}}}}
	out, err = svc.ListVisibleGroupsWithConfig(context.Background(), nil, false, &ModelPlazaGeiliConfig{GroupWhitelist: []int64{20}})
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestListVisibleGroupsWithConfig_OfficialOverrides(t *testing.T) {
	pricingSvc := newStubPricingServiceFromMap(map[string]*LiteLLMModelPricing{
		"claude-sonnet": {
			Mode:                        "chat",
			InputCostPerToken:           3e-6,
			OutputCostPerToken:          1.5e-5,
			CacheCreationInputTokenCost: 3.75e-6,
			CacheReadInputTokenCost:     3e-7,
		},
	})
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, "anthropic", "claude-sonnet", "deepseek-v4")}
	groups := []Group{{ID: 10, Name: "g", Platform: "anthropic", RateMultiplier: 1}}
	svc := newPlazaService(channels, groups, pricingSvc)
	svc.billingService = NewBillingService(&config.Config{}, pricingSvc)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)

	cfg := &ModelPlazaGeiliConfig{
		GroupWhitelist: []int64{10},
		OfficialOverrides: []ModelPlazaOfficialOverride{
			{Model: "Claude-Sonnet", Currency: ModelPlazaCurrencyUSD, Output: f64(20), Note: "手动"},
			{Model: "deepseek-v4", Currency: ModelPlazaCurrencyCNY, Input: f64(4), Output: f64(16), Note: "高峰价"},
		},
	}
	for i := 0; i < 2; i++ { // 二次调用确认被 memo 的官方价未被覆盖污染
		out, err := svc.ListVisibleGroupsWithConfig(context.Background(), nil, false, cfg)
		require.NoError(t, err)
		require.Len(t, out, 1)
		byName := map[string]PlazaModel{}
		for _, m := range out[0].Models {
			byName[m.Name] = m
		}

		usd := byName["claude-sonnet"].OfficialPricing
		require.NotNil(t, usd)
		require.InDelta(t, 3e-6, *usd.InputPrice, 1e-12, "未覆盖字段保留官方值")
		require.InDelta(t, 2e-5, *usd.OutputPrice, 1e-12, "覆盖字段按每百万换算为每 token")
		require.InDelta(t, 3e-7, *usd.CacheReadPrice, 1e-12)
		require.Equal(t, "手动", usd.Note)
		require.Empty(t, usd.Currency)

		cny := byName["deepseek-v4"].OfficialPricing
		require.NotNil(t, cny, "官方无此模型时覆盖项也能提供参考价")
		require.Equal(t, ModelPlazaCurrencyCNY, cny.Currency)
		require.InDelta(t, 4e-6, *cny.InputPrice, 1e-12)
		require.InDelta(t, 1.6e-5, *cny.OutputPrice, 1e-12)
		require.Nil(t, cny.CacheReadPrice)
		require.Equal(t, "高峰价", cny.Note)
	}
}
