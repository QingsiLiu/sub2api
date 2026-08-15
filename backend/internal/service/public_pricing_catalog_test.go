package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	appTimezone "github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type publicPricingSettingsReaderStub struct {
	runtime PublicPricingRuntime
	err     error
}

func (s *publicPricingSettingsReaderStub) GetPublicPricingRuntime(context.Context) (PublicPricingRuntime, error) {
	return s.runtime, s.err
}

type publicPricingGroupReaderStub struct {
	groups []Group
	err    error
}

func (s *publicPricingGroupReaderStub) ListActive(context.Context) ([]Group, error) {
	return append([]Group(nil), s.groups...), s.err
}

type publicPricingChannelReaderStub struct {
	channels []AvailableChannel
	err      error
}

func (s *publicPricingChannelReaderStub) ListAvailable(context.Context) ([]AvailableChannel, error) {
	return append([]AvailableChannel(nil), s.channels...), s.err
}

type publicPricingUserReaderStub struct {
	groups   []Group
	rates    map[int64]float64
	groupErr error
	rateErr  error
}

func (s *publicPricingUserReaderStub) GetAvailableGroups(context.Context, int64) ([]Group, error) {
	return append([]Group(nil), s.groups...), s.groupErr
}

func (s *publicPricingUserReaderStub) GetUserGroupRates(context.Context, int64) (map[int64]float64, error) {
	if s.rateErr != nil {
		return nil, s.rateErr
	}
	result := make(map[int64]float64, len(s.rates))
	for id, rate := range s.rates {
		result[id] = rate
	}
	return result, nil
}

func defaultPublicPricingRuntimeForTest(groupIDs []int64, modelIDs []string) PublicPricingRuntime {
	fx := decimal.RequireFromString("6.8")
	return PublicPricingRuntime{
		Enabled:           true,
		GroupIDs:          groupIDs,
		ModelIDs:          modelIDs,
		NativeCNYGroupIDs: map[int64]struct{}{},
		QuotaUSDPerCNY:    decimal.NewFromInt(1),
		USDCNYReference:   &fx,
		FXSource:          defaultPublicPricingFXSource,
		FXEffectiveDate:   defaultPublicPricingFXEffectiveDate,
	}
}

func newPublicPricingQuoteService(channel *Channel) (*PublicPricingCatalogService, *BillingService, *ModelPricingResolver) {
	billing := NewBillingService(&config.Config{}, nil)
	channelService := &ChannelService{}
	cache := newEmptyChannelCache()
	cache.loadedAt = time.Now()
	if channel != nil {
		cache.byID[channel.ID] = channel
		for _, groupID := range channel.GroupIDs {
			cache.channelByGroupID[groupID] = channel
			platform := PlatformOpenAI
			if len(channel.ModelPricing) > 0 && channel.ModelPricing[0].Platform != "" {
				platform = channel.ModelPricing[0].Platform
			}
			cache.groupPlatform[groupID] = platform
			expandPricingToCache(cache, channel, groupID, platform)
		}
	}
	channelService.cache.Store(cache)
	resolver := NewModelPricingResolver(channelService, billing)
	return &PublicPricingCatalogService{
		billing:  billing,
		resolver: resolver,
		now:      func() time.Time { return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC) },
	}, billing, resolver
}

func findPublicPricingItem(t *testing.T, items []PublicPricingItem, key string) PublicPricingItem {
	t.Helper()
	for _, item := range items {
		if item.Key == key {
			return item
		}
	}
	t.Fatalf("pricing item %q not found", key)
	return PublicPricingItem{}
}

func TestPublicPricingGroupPriceMatchesUnifiedBillingQuote(t *testing.T) {
	ctx := context.Background()
	service, billing, resolver := newPublicPricingQuoteService(nil)
	runtime := defaultPublicPricingRuntimeForTest([]int64{4}, []string{"gpt-5.4"})
	group := Group{ID: 4, Name: "Plus", Platform: PlatformOpenAI, RateMultiplier: 0.1, Status: StatusActive}
	view := PublicPricingGroup{ID: 4, CurrencyMode: "usd_quota", EffectiveMultiplier: "0.1"}
	official := activeOfficialPricing("gpt-5.4", service.now())

	price, err := service.buildGroupPrice(ctx, runtime, &group, view, nil, "gpt-5.4", official, service.now())
	require.NoError(t, err)
	require.Equal(t, PricingSourceFallback, price.PriceSource)

	input := findPublicPricingItem(t, price.Items, "input")
	require.Equal(t, "2.5", input.SystemBasePrice)
	require.Equal(t, "0.1", input.EffectiveMultiplier)
	require.Equal(t, "0.25", input.ActualCNYPrice)
	require.Equal(t, "17", *input.OfficialCNYPrice)
	require.Equal(t, "16.75", *input.SavingsCNY)
	require.Equal(t, "98.53", *input.SavingsPercent)

	groupID := group.ID
	resolved := resolver.Resolve(ctx, PricingInput{Model: "gpt-5.4", GroupID: &groupID})
	breakdown, err := billing.CalculateCostUnified(CostInput{
		Ctx:            ctx,
		Model:          "gpt-5.4",
		GroupID:        &groupID,
		Tokens:         UsageTokens{InputTokens: 1},
		RateMultiplier: 0.1,
		Resolver:       resolver,
		Resolved:       resolved,
	})
	require.NoError(t, err)
	expectedUnitPrice := decimal.NewFromFloat(breakdown.ActualCost).Mul(decimal.NewFromInt(1_000_000))
	require.Equal(t, formatPricingDecimal(expectedUnitPrice, 8), input.ActualCNYPrice)
}

func TestPublicPricingSpecialAndPeakMultipliersMatchSettlementRules(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newPublicPricingQuoteService(nil)
	runtime := defaultPublicPricingRuntimeForTest([]int64{4}, []string{"gpt-5.4"})
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, appTimezone.Location())
	group := Group{
		ID:                 4,
		Name:               "Peak",
		Platform:           PlatformOpenAI,
		RateMultiplier:     0.2,
		Status:             StatusActive,
		SubscriptionType:   SubscriptionTypeSubscription,
		PeakRateEnabled:    true,
		PeakStart:          "00:00",
		PeakEnd:            "23:59",
		PeakRateMultiplier: 1.5,
	}
	view := PublicPricingGroup{ID: 4, CurrencyMode: "usd_quota", EffectiveMultiplier: "0.24", MultiplierSource: "user"}

	price, err := service.buildGroupPrice(ctx, runtime, &group, view, map[int64]float64{4: 0.16}, "gpt-5.4", activeOfficialPricing("gpt-5.4", now), now)
	require.NoError(t, err)
	input := findPublicPricingItem(t, price.Items, "input")
	require.Equal(t, "0.24", input.EffectiveMultiplier)
	require.Equal(t, "user", input.MultiplierSource)
	require.Equal(t, "0.6", input.ActualCNYPrice)
}

func TestPublicPricingPerRequestDoesNotApplyTokenPeakMultiplier(t *testing.T) {
	price1K := 0.04
	channel := &Channel{
		ID:       91,
		Status:   StatusActive,
		GroupIDs: []int64{4},
		ModelPricing: []ChannelModelPricing{{
			Platform:    PlatformOpenAI,
			Models:      []string{"request-model"},
			BillingMode: BillingModePerRequest,
			Intervals: []PricingInterval{{
				TierLabel:       "1K",
				PerRequestPrice: &price1K,
			}},
		}},
	}
	service, _, _ := newPublicPricingQuoteService(channel)
	runtime := defaultPublicPricingRuntimeForTest([]int64{4}, []string{"request-model"})
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, appTimezone.Location())
	group := Group{
		ID:                 4,
		Name:               "Peak",
		Platform:           PlatformOpenAI,
		RateMultiplier:     0.2,
		Status:             StatusActive,
		SubscriptionType:   SubscriptionTypeSubscription,
		PeakRateEnabled:    true,
		PeakStart:          "00:00",
		PeakEnd:            "23:59",
		PeakRateMultiplier: 2,
	}
	view := PublicPricingGroup{ID: 4, CurrencyMode: "usd_quota", EffectiveMultiplier: "0.4"}

	price, err := service.buildGroupPrice(context.Background(), runtime, &group, view, nil, "request-model", nil, now)
	require.NoError(t, err)
	require.Equal(t, "per_request", price.BillingMode)
	require.Len(t, price.Items, 1)
	require.Equal(t, "0.2", price.Items[0].EffectiveMultiplier)
	require.Equal(t, "0.04", price.Items[0].SystemBasePrice)
	require.Equal(t, "0.008", price.Items[0].ActualCNYPrice)
}

func TestPublicPricingIntervalQuoteUsesTheMatchingSettlementTier(t *testing.T) {
	maxTokens := 128000
	lowInputPrice := 0.000001
	highInputPrice := 0.000002
	channel := &Channel{
		ID:       92,
		Status:   StatusActive,
		GroupIDs: []int64{4},
		ModelPricing: []ChannelModelPricing{{
			Platform: PlatformOpenAI,
			Models:   []string{"tiered-model"},
			Intervals: []PricingInterval{
				{MinTokens: 0, MaxTokens: &maxTokens, InputPrice: &lowInputPrice},
				{MinTokens: maxTokens, InputPrice: &highInputPrice},
			},
		}},
	}
	service, _, _ := newPublicPricingQuoteService(channel)
	runtime := defaultPublicPricingRuntimeForTest([]int64{4}, []string{"tiered-model"})
	group := Group{ID: 4, Name: "Tiered", Platform: PlatformOpenAI, RateMultiplier: 0.1, Status: StatusActive}
	view := PublicPricingGroup{ID: 4, CurrencyMode: "usd_quota", EffectiveMultiplier: "0.1"}

	price, err := service.buildGroupPrice(context.Background(), runtime, &group, view, nil, "tiered-model", nil, service.now())
	require.NoError(t, err)
	low := findPublicPricingItem(t, price.Items, "input:interval:0-128000")
	high := findPublicPricingItem(t, price.Items, "input:interval:128000-inf")
	require.Equal(t, "1", low.SystemBasePrice)
	require.Equal(t, "0.1", low.ActualCNYPrice)
	require.Equal(t, "2", high.SystemBasePrice)
	require.Equal(t, "0.2", high.ActualCNYPrice)
}

func TestPublicPricingNativeCNYDoesNotApplyUSDExchangeRate(t *testing.T) {
	service, _, _ := newPublicPricingQuoteService(nil)
	runtime := defaultPublicPricingRuntimeForTest([]int64{82}, []string{"gpt-5.4"})
	runtime.NativeCNYGroupIDs[82] = struct{}{}
	group := Group{ID: 82, Name: "CN", Platform: PlatformOpenAI, RateMultiplier: 0.1, Status: StatusActive}
	view := PublicPricingGroup{ID: 82, CurrencyMode: "native_cny", EffectiveMultiplier: "0.1"}

	price, err := service.buildGroupPrice(context.Background(), runtime, &group, view, nil, "gpt-5.4", activeOfficialPricing("gpt-5.4", service.now()), service.now())
	require.NoError(t, err)
	input := findPublicPricingItem(t, price.Items, "input")
	require.Equal(t, "CNY", input.SystemBaseCurrency)
	require.Equal(t, "0.25", input.ActualCNYPrice)
	require.Equal(t, "fx_unavailable", input.ComparisonStatus)
	require.Nil(t, input.OfficialCNYPrice)
}

func TestPublicPricingCatalogBuildsPublicAndPersonalizedViews(t *testing.T) {
	group4 := Group{ID: 4, Name: "Public", Platform: PlatformOpenAI, RateMultiplier: 0.1, Status: StatusActive}
	group27 := Group{ID: 27, Name: "Stable", Platform: PlatformOpenAI, RateMultiplier: 0.2, Status: StatusActive}
	runtime := defaultPublicPricingRuntimeForTest([]int64{4, 27}, []string{"gpt-5.4"})
	settings := &publicPricingSettingsReaderStub{runtime: runtime}
	groups := &publicPricingGroupReaderStub{groups: []Group{group4, group27}}
	channels := &publicPricingChannelReaderStub{channels: []AvailableChannel{{
		ID:              1,
		Status:          StatusActive,
		Groups:          []AvailableGroupRef{{ID: 4, Platform: PlatformOpenAI}, {ID: 27, Platform: PlatformOpenAI}},
		SupportedModels: []SupportedModel{{Name: "gpt-5.4", Platform: PlatformOpenAI}},
	}}}
	users := &publicPricingUserReaderStub{groups: []Group{group27}, rates: map[int64]float64{27: 0.16}}
	service, _, _ := newPublicPricingQuoteService(nil)
	service.settings = settings
	service.groups = groups
	service.channels = channels
	service.apiKeys = users

	publicCatalog, err := service.PublicCatalog(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int64{4, 27}, sortedPricingGroupIDs(publicCatalog.Groups))
	require.Len(t, publicCatalog.Models, 1)
	require.Len(t, publicCatalog.Models[0].GroupPrices, 2)
	require.False(t, publicCatalog.Stale)

	personalCatalog, err := service.UserCatalog(context.Background(), 42)
	require.NoError(t, err)
	require.Len(t, personalCatalog.Groups, 1)
	require.Equal(t, int64(27), personalCatalog.Groups[0].ID)
	require.Equal(t, "0.16", personalCatalog.Groups[0].EffectiveMultiplier)
	require.Equal(t, "user", personalCatalog.Groups[0].MultiplierSource)
	input := findPublicPricingItem(t, personalCatalog.Models[0].GroupPrices[0].Items, "input")
	require.Equal(t, "0.16", input.EffectiveMultiplier)
	require.Equal(t, "0.4", input.ActualCNYPrice)
}

func TestPublicPricingCatalogUsesLastGoodPublicSnapshotOnly(t *testing.T) {
	group := Group{ID: 4, Name: "Public", Platform: PlatformOpenAI, RateMultiplier: 0.1, Status: StatusActive}
	settings := &publicPricingSettingsReaderStub{runtime: defaultPublicPricingRuntimeForTest([]int64{4}, []string{"gpt-5.4"})}
	service, _, _ := newPublicPricingQuoteService(nil)
	service.settings = settings
	service.groups = &publicPricingGroupReaderStub{groups: []Group{group}}
	service.channels = &publicPricingChannelReaderStub{channels: []AvailableChannel{{
		Status:          StatusActive,
		Groups:          []AvailableGroupRef{{ID: 4, Platform: PlatformOpenAI}},
		SupportedModels: []SupportedModel{{Name: "gpt-5.4", Platform: PlatformOpenAI}},
	}}}
	service.apiKeys = &publicPricingUserReaderStub{}

	fresh, err := service.PublicCatalog(context.Background())
	require.NoError(t, err)
	require.False(t, fresh.Stale)

	settings.err = errors.New("database unavailable")
	stale, err := service.PublicCatalog(context.Background())
	require.NoError(t, err)
	require.True(t, stale.Stale)
	require.Equal(t, fresh.DataVersion, stale.DataVersion)
	require.NotSame(t, fresh, stale)

	_, err = service.UserCatalog(context.Background(), 42)
	require.Error(t, err)
}

func TestPublicPricingCatalogDisabledNeverFallsBackToSnapshot(t *testing.T) {
	settings := &publicPricingSettingsReaderStub{runtime: PublicPricingRuntime{Enabled: false}}
	service, _, _ := newPublicPricingQuoteService(nil)
	service.settings = settings
	service.groups = &publicPricingGroupReaderStub{}
	service.channels = &publicPricingChannelReaderStub{}
	service.apiKeys = &publicPricingUserReaderStub{}

	_, err := service.PublicCatalog(context.Background())
	require.ErrorIs(t, err, ErrPublicPricingDisabled)
}

func TestPublicPricingCatalogResponseDoesNotExposeInternalFields(t *testing.T) {
	catalog := PublicPricingCatalog{
		GeneratedAt: "2026-08-14T00:00:00Z",
		Models:      []PublicPricingModel{{ModelID: "gpt-5.4", DisplayName: "GPT-5.4"}},
	}
	encoded, err := json.Marshal(catalog)
	require.NoError(t, err)
	payload := string(encoded)
	for _, forbidden := range []string{"channel_name", "account", "api_key", "user_id", "model_mapping", "cost_multiplier"} {
		require.False(t, strings.Contains(payload, forbidden), forbidden)
	}
}
