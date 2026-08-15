package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

var ErrPublicPricingDisabled = errors.New("public pricing is disabled")

type PublicPricingExchange struct {
	QuotaUSDPerCNY  string  `json:"quota_usd_per_cny"`
	USDCNYReference *string `json:"usd_cny_reference"`
	Source          string  `json:"source"`
	EffectiveDate   string  `json:"effective_date"`
}

type PublicPricingGroup struct {
	ID                  int64  `json:"id"`
	Name                string `json:"name"`
	Platform            string `json:"platform"`
	DefaultMultiplier   string `json:"default_multiplier"`
	EffectiveMultiplier string `json:"effective_multiplier"`
	MultiplierSource    string `json:"multiplier_source"`
	CurrencyMode        string `json:"currency_mode"`
	PeakRateEnabled     bool   `json:"peak_rate_enabled"`
	PeakRateActive      bool   `json:"peak_rate_active"`
	PeakStart           string `json:"peak_start,omitempty"`
	PeakEnd             string `json:"peak_end,omitempty"`
	PeakRateMultiplier  string `json:"peak_rate_multiplier,omitempty"`
}

type PublicPricingItem struct {
	Key                 string  `json:"key"`
	Unit                string  `json:"unit"`
	TierLabel           string  `json:"tier_label,omitempty"`
	MinContextTokens    int     `json:"min_context_tokens,omitempty"`
	MaxContextTokens    *int    `json:"max_context_tokens,omitempty"`
	OfficialPrice       *string `json:"official_price"`
	OfficialCurrency    string  `json:"official_currency,omitempty"`
	OfficialCNYPrice    *string `json:"official_cny_price"`
	SystemBasePrice     string  `json:"system_base_price"`
	SystemBaseCurrency  string  `json:"system_base_currency"`
	EffectiveMultiplier string  `json:"effective_multiplier"`
	MultiplierSource    string  `json:"multiplier_source"`
	ActualCNYPrice      string  `json:"actual_cny_price"`
	SavingsCNY          *string `json:"savings_cny"`
	SavingsPercent      *string `json:"savings_percent"`
	ComparisonStatus    string  `json:"comparison_status"`
}

type PublicPricingGroupPrice struct {
	GroupID     int64               `json:"group_id"`
	BillingMode string              `json:"billing_mode"`
	PriceSource string              `json:"price_source"`
	Items       []PublicPricingItem `json:"items"`
}

type PublicPricingModel struct {
	ModelID                string                    `json:"model_id"`
	DisplayName            string                    `json:"display_name"`
	Provider               string                    `json:"provider"`
	BillingMode            string                    `json:"billing_mode"`
	ComparisonStatus       string                    `json:"comparison_status"`
	OfficialReferenceModel string                    `json:"official_reference_model,omitempty"`
	OfficialSource         string                    `json:"official_source,omitempty"`
	OfficialEffectiveFrom  string                    `json:"official_effective_from,omitempty"`
	OfficialEffectiveUntil string                    `json:"official_effective_until,omitempty"`
	OfficialRatePeriod     string                    `json:"official_rate_period,omitempty"`
	GroupPrices            []PublicPricingGroupPrice `json:"group_prices"`
}

type PublicPricingCatalog struct {
	GeneratedAt string                `json:"generated_at"`
	DataVersion string                `json:"data_version"`
	Stale       bool                  `json:"stale"`
	Exchange    PublicPricingExchange `json:"exchange"`
	Groups      []PublicPricingGroup  `json:"groups"`
	Models      []PublicPricingModel  `json:"models"`
}

type publicPricingSettingsReader interface {
	GetPublicPricingRuntime(ctx context.Context) (PublicPricingRuntime, error)
}

type publicPricingGroupReader interface {
	ListActive(ctx context.Context) ([]Group, error)
}

type publicPricingChannelReader interface {
	ListAvailable(ctx context.Context) ([]AvailableChannel, error)
}

type publicPricingUserReader interface {
	GetAvailableGroups(ctx context.Context, userID int64) ([]Group, error)
	GetUserGroupRates(ctx context.Context, userID int64) (map[int64]float64, error)
}

type PublicPricingCatalogService struct {
	settings publicPricingSettingsReader
	groups   publicPricingGroupReader
	channels publicPricingChannelReader
	billing  *BillingService
	resolver *ModelPricingResolver
	apiKeys  publicPricingUserReader

	now func() time.Time

	lastPublicMu sync.RWMutex
	lastPublic   *PublicPricingCatalog
}

func NewPublicPricingCatalogService(
	settings *SettingService,
	groups *GroupService,
	channels *ChannelService,
	billing *BillingService,
	resolver *ModelPricingResolver,
	apiKeys *APIKeyService,
) *PublicPricingCatalogService {
	return &PublicPricingCatalogService{
		settings: settings,
		groups:   groups,
		channels: channels,
		billing:  billing,
		resolver: resolver,
		apiKeys:  apiKeys,
		now:      time.Now,
	}
}

func (s *PublicPricingCatalogService) PublicCatalog(ctx context.Context) (*PublicPricingCatalog, error) {
	catalog, err := s.build(ctx, nil)
	if err == nil {
		s.setLastPublic(catalog)
		return catalog, nil
	}
	if errors.Is(err, ErrPublicPricingDisabled) {
		return nil, err
	}
	if stale := s.getLastPublic(); stale != nil {
		stale.Stale = true
		return stale, nil
	}
	return nil, err
}

func (s *PublicPricingCatalogService) UserCatalog(ctx context.Context, userID int64) (*PublicPricingCatalog, error) {
	return s.build(ctx, &userID)
}

func (s *PublicPricingCatalogService) build(ctx context.Context, userID *int64) (*PublicPricingCatalog, error) {
	runtime, err := s.settings.GetPublicPricingRuntime(ctx)
	if err != nil {
		return nil, err
	}
	if !runtime.Enabled {
		return nil, ErrPublicPricingDisabled
	}

	allGroups, err := s.groups.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active pricing groups: %w", err)
	}
	groupByID := make(map[int64]Group, len(allGroups))
	for i := range allGroups {
		groupByID[allGroups[i].ID] = allGroups[i]
	}

	allowedForUser := map[int64]struct{}(nil)
	userRates := map[int64]float64{}
	if userID != nil {
		available, listErr := s.apiKeys.GetAvailableGroups(ctx, *userID)
		if listErr != nil {
			return nil, fmt.Errorf("list user pricing groups: %w", listErr)
		}
		allowedForUser = make(map[int64]struct{}, len(available))
		for i := range available {
			allowedForUser[available[i].ID] = struct{}{}
		}
		userRates, err = s.apiKeys.GetUserGroupRates(ctx, *userID)
		if err != nil {
			return nil, fmt.Errorf("get user pricing multipliers: %w", err)
		}
	}

	selectedGroups := make([]Group, 0, len(runtime.GroupIDs))
	for _, id := range runtime.GroupIDs {
		group, ok := groupByID[id]
		if !ok {
			continue
		}
		if allowedForUser != nil {
			if _, ok := allowedForUser[id]; !ok {
				continue
			}
		}
		selectedGroups = append(selectedGroups, group)
	}

	availableModels, err := s.availableModelsByGroup(ctx, selectedGroups, runtime.ModelIDs)
	if err != nil {
		return nil, err
	}

	now := s.now()
	groupViews := make([]PublicPricingGroup, 0, len(selectedGroups))
	groupViewByID := make(map[int64]PublicPricingGroup, len(selectedGroups))
	for i := range selectedGroups {
		group := &selectedGroups[i]
		baseRate := decimal.NewFromFloat(group.RateMultiplier)
		source := "default"
		if userRate, ok := userRates[group.ID]; ok {
			baseRate = decimal.NewFromFloat(userRate)
			source = "user"
		}
		peak := decimal.NewFromFloat(group.PeakMultiplierAt(now))
		peakActive := !peak.Equal(decimal.NewFromInt(1))
		currencyMode := "usd_quota"
		if _, ok := runtime.NativeCNYGroupIDs[group.ID]; ok {
			currencyMode = "native_cny"
		}
		view := PublicPricingGroup{
			ID:                  group.ID,
			Name:                group.Name,
			Platform:            group.Platform,
			DefaultMultiplier:   formatPricingDecimal(decimal.NewFromFloat(group.RateMultiplier), 6),
			EffectiveMultiplier: formatPricingDecimal(baseRate.Mul(peak), 6),
			MultiplierSource:    source,
			CurrencyMode:        currencyMode,
			PeakRateEnabled:     group.PeakRateEnabled,
			PeakRateActive:      peakActive,
			PeakStart:           group.PeakStart,
			PeakEnd:             group.PeakEnd,
			PeakRateMultiplier:  formatPricingDecimal(decimal.NewFromFloat(group.PeakRateMultiplier), 6),
		}
		groupViews = append(groupViews, view)
		groupViewByID[group.ID] = view
	}

	models := make([]PublicPricingModel, 0, len(runtime.ModelIDs))
	usedGroups := make(map[int64]struct{})
	for _, modelID := range runtime.ModelIDs {
		modelID = strings.ToLower(modelID)
		definition := publicPricingModelDefinitions[modelID]
		if definition.DisplayName == "" {
			definition = publicPricingModelDefinition{DisplayName: modelID, Provider: providerForPricingModel(modelID)}
		}

		model := PublicPricingModel{
			ModelID:          modelID,
			DisplayName:      definition.DisplayName,
			Provider:         definition.Provider,
			ComparisonStatus: pricingComparisonUnavailable,
		}
		official := activeOfficialPricing(modelID, now)
		if official != nil {
			model.ComparisonStatus = pricingComparisonExact
			model.OfficialReferenceModel = official.ReferenceModel
			model.OfficialSource = official.Source
			model.OfficialEffectiveFrom = official.EffectiveFrom.Format("2006-01-02")
			model.OfficialRatePeriod = official.RatePeriod
			if official.EffectiveUntil != nil {
				model.OfficialEffectiveUntil = official.EffectiveUntil.Format("2006-01-02")
			}
		}

		for i := range selectedGroups {
			group := &selectedGroups[i]
			if _, ok := availableModels[group.ID][modelID]; !ok {
				continue
			}
			groupPrice, buildErr := s.buildGroupPrice(ctx, runtime, group, groupViewByID[group.ID], userRates, modelID, official, now)
			if buildErr != nil {
				continue
			}
			if len(groupPrice.Items) == 0 {
				continue
			}
			if model.BillingMode == "" {
				model.BillingMode = groupPrice.BillingMode
			} else if model.BillingMode != groupPrice.BillingMode {
				model.BillingMode = "mixed"
			}
			model.GroupPrices = append(model.GroupPrices, groupPrice)
			usedGroups[group.ID] = struct{}{}
		}
		if len(model.GroupPrices) > 0 {
			models = append(models, model)
		}
	}

	filteredGroups := groupViews[:0]
	for _, group := range groupViews {
		if _, ok := usedGroups[group.ID]; ok {
			filteredGroups = append(filteredGroups, group)
		}
	}

	exchange := PublicPricingExchange{
		QuotaUSDPerCNY: formatPricingDecimal(runtime.QuotaUSDPerCNY, 8),
		Source:         runtime.FXSource,
		EffectiveDate:  runtime.FXEffectiveDate,
	}
	if runtime.USDCNYReference != nil {
		formatted := formatPricingDecimal(*runtime.USDCNYReference, 8)
		exchange.USDCNYReference = &formatted
	}

	catalog := &PublicPricingCatalog{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Stale:       false,
		Exchange:    exchange,
		Groups:      filteredGroups,
		Models:      models,
	}
	catalog.DataVersion = pricingCatalogVersion(catalog)
	return catalog, nil
}

func (s *PublicPricingCatalogService) availableModelsByGroup(ctx context.Context, groups []Group, configuredModels []string) (map[int64]map[string]struct{}, error) {
	configured := make(map[string]struct{}, len(configuredModels))
	for _, model := range configuredModels {
		configured[strings.ToLower(model)] = struct{}{}
	}
	groupPlatform := make(map[int64]string, len(groups))
	out := make(map[int64]map[string]struct{}, len(groups))
	for i := range groups {
		groupPlatform[groups[i].ID] = groups[i].Platform
		out[groups[i].ID] = make(map[string]struct{})
	}

	channels, err := s.channels.ListAvailable(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pricing channels: %w", err)
	}
	for i := range channels {
		channel := &channels[i]
		if channel.Status != StatusActive {
			continue
		}
		for _, groupRef := range channel.Groups {
			platform, selected := groupPlatform[groupRef.ID]
			if !selected {
				continue
			}
			for _, model := range channel.SupportedModels {
				name := strings.ToLower(strings.TrimSpace(model.Name))
				if _, ok := configured[name]; !ok || model.Platform != platform {
					continue
				}
				out[groupRef.ID][name] = struct{}{}
			}
		}
	}
	return out, nil
}

func (s *PublicPricingCatalogService) buildGroupPrice(
	ctx context.Context,
	runtime PublicPricingRuntime,
	group *Group,
	groupView PublicPricingGroup,
	userRates map[int64]float64,
	modelID string,
	official *officialPricingVersion,
	now time.Time,
) (PublicPricingGroupPrice, error) {
	groupID := group.ID
	resolved := s.resolver.Resolve(ctx, PricingInput{Model: modelID, GroupID: &groupID})
	if resolved == nil {
		return PublicPricingGroupPrice{}, fmt.Errorf("pricing resolver returned nil")
	}
	baseRate := group.RateMultiplier
	multiplierSource := "default"
	if userRate, ok := userRates[group.ID]; ok {
		baseRate = userRate
		multiplierSource = "user"
	}
	peakRate := group.PeakMultiplierAt(now)

	specs := publicPricingItemSpecs(resolved)
	items := make([]PublicPricingItem, 0, len(specs))
	for _, spec := range specs {
		effectiveRate := baseRate
		if resolved.Mode == BillingModeToken && spec.ApplyPeak {
			effectiveRate *= peakRate
		}
		basePrice, err := s.quoteItem(ctx, modelID, groupID, resolved, spec, 1)
		if err != nil {
			continue
		}
		actual, err := s.quoteItem(ctx, modelID, groupID, resolved, spec, effectiveRate)
		if err != nil {
			continue
		}

		nativeCNY := groupView.CurrencyMode == "native_cny"
		actualCNY := actual
		baseCurrency := "CNY"
		if !nativeCNY {
			baseCurrency = "USD"
			actualCNY = actual.Div(runtime.QuotaUSDPerCNY)
		}
		item := PublicPricingItem{
			Key:                 spec.Key,
			Unit:                spec.Unit,
			TierLabel:           spec.TierLabel,
			MinContextTokens:    spec.MinContextTokens,
			MaxContextTokens:    spec.MaxContextTokens,
			SystemBasePrice:     formatPricingDecimal(basePrice, 8),
			SystemBaseCurrency:  baseCurrency,
			EffectiveMultiplier: formatPricingDecimal(decimal.NewFromFloat(effectiveRate), 6),
			MultiplierSource:    multiplierSource,
			ActualCNYPrice:      formatPricingDecimal(actualCNY, 8),
			ComparisonStatus:    pricingComparisonUnavailable,
		}

		if official != nil {
			if officialPrice, ok := officialPriceForSpec(official, spec); ok {
				formatted := formatPricingDecimal(officialPrice, 8)
				item.OfficialPrice = &formatted
				item.OfficialCurrency = official.Currency
				var officialCNY decimal.Decimal
				canCompare := false
				if official.Currency == "CNY" && nativeCNY {
					officialCNY = officialPrice
					canCompare = true
				} else if official.Currency == "USD" && !nativeCNY && runtime.HasOfficialCNYComparison() {
					officialCNY = officialPrice.Mul(*runtime.USDCNYReference)
					canCompare = true
				}
				if canCompare {
					officialCNYFormatted := formatPricingDecimal(officialCNY, 8)
					item.OfficialCNYPrice = &officialCNYFormatted
					savings := officialCNY.Sub(actualCNY)
					savingsFormatted := formatPricingDecimal(savings, 8)
					percent := decimal.Zero
					if !officialCNY.IsZero() {
						percent = savings.Div(officialCNY).Mul(decimal.NewFromInt(100))
					}
					percentFormatted := formatPricingDecimal(percent, 2)
					item.SavingsCNY = &savingsFormatted
					item.SavingsPercent = &percentFormatted
					item.ComparisonStatus = pricingComparisonExact
				} else {
					item.ComparisonStatus = "fx_unavailable"
				}
			}
		}
		items = append(items, item)
	}

	return PublicPricingGroupPrice{
		GroupID:     group.ID,
		BillingMode: normalizedPublicBillingMode(resolved.Mode),
		PriceSource: resolved.Source,
		Items:       items,
	}, nil
}

type publicPricingItemSpec struct {
	Key              string
	Kind             string
	Unit             string
	TierLabel        string
	MinContextTokens int
	MaxContextTokens *int
	SampleContext    int
	SizeTier         string
	ApplyPeak        bool
	LongContext      bool
}

func publicPricingItemSpecs(resolved *ResolvedPricing) []publicPricingItemSpec {
	if resolved == nil {
		return nil
	}
	if resolved.Mode == BillingModePerRequest || resolved.Mode == BillingModeImage {
		items := make([]publicPricingItemSpec, 0, len(resolved.RequestTiers)+1)
		for _, tier := range resolved.RequestTiers {
			if tier.PerRequestPrice == nil {
				continue
			}
			label := strings.TrimSpace(tier.TierLabel)
			key := "request"
			if label != "" {
				key += ":" + strings.ToLower(label)
			} else {
				key += ":" + publicPricingRangeKey(tier.MinTokens, tier.MaxTokens)
			}
			items = append(items, publicPricingItemSpec{
				Key: key, Kind: "request", Unit: "request", TierLabel: label,
				MinContextTokens: tier.MinTokens, MaxContextTokens: tier.MaxTokens,
				SampleContext: publicPricingSampleContext(tier.MinTokens), SizeTier: label,
			})
		}
		if resolved.DefaultPerRequestPrice > 0 {
			items = append(items, publicPricingItemSpec{Key: "request", Kind: "request", Unit: "request"})
		}
		return items
	}

	if len(resolved.Intervals) > 0 {
		items := make([]publicPricingItemSpec, 0, len(resolved.Intervals)*4)
		for _, interval := range resolved.Intervals {
			suffix := ":interval:" + publicPricingRangeKey(interval.MinTokens, interval.MaxTokens)
			sample := publicPricingSampleContext(interval.MinTokens)
			appendTokenSpec := func(kind string, present bool) {
				if !present {
					return
				}
				items = append(items, publicPricingItemSpec{
					Key: kind + suffix, Kind: kind, Unit: "1M_tokens", TierLabel: interval.TierLabel,
					MinContextTokens: interval.MinTokens, MaxContextTokens: interval.MaxTokens,
					SampleContext: sample, ApplyPeak: true,
				})
			}
			appendTokenSpec("input", interval.InputPrice != nil)
			appendTokenSpec("output", interval.OutputPrice != nil)
			appendTokenSpec("cache_write", interval.CacheWritePrice != nil)
			appendTokenSpec("cache_read", interval.CacheReadPrice != nil)
		}
		return items
	}

	p := resolved.BasePricing
	if p == nil {
		return nil
	}
	items := make([]publicPricingItemSpec, 0, 12)
	appendFlat := func(key, kind string, present bool) {
		if present {
			items = append(items, publicPricingItemSpec{Key: key, Kind: kind, Unit: "1M_tokens", ApplyPeak: true, SampleContext: 1})
		}
	}
	appendFlat("input", "input", p.InputPricePerToken > 0)
	appendFlat("image_input", "image_input", p.ImageInputPricePerToken > 0)
	appendFlat("output", "output", p.OutputPricePerToken > 0)
	appendFlat("image_output", "image_output", p.ImageOutputPricePerToken > 0 || p.ImageOutputPriceExplicit)
	if p.SupportsCacheBreakdown && (p.CacheCreation5mPrice > 0 || p.CacheCreation1hPrice > 0) {
		appendFlat("cache_write_5m", "cache_write_5m", p.CacheCreation5mPrice > 0)
		appendFlat("cache_write_1h", "cache_write_1h", p.CacheCreation1hPrice > 0)
	} else {
		appendFlat("cache_write", "cache_write", p.CacheCreationPricePerToken > 0 || p.CacheCreationPriceExplicit)
	}
	appendFlat("cache_read", "cache_read", p.CacheReadPricePerToken > 0)

	if p.LongContextInputThreshold > 0 && p.LongContextInputMultiplier > 0 {
		threshold := p.LongContextInputThreshold + 1
		appendLong := func(key, kind string, present bool) {
			if present {
				items = append(items, publicPricingItemSpec{
					Key: key + ":long_context", Kind: kind, Unit: "1M_tokens", MinContextTokens: threshold,
					SampleContext: threshold, ApplyPeak: true, LongContext: true,
				})
			}
		}
		appendLong("input", "input", p.InputPricePerToken > 0)
		appendLong("output", "output", p.OutputPricePerToken > 0 && p.LongContextOutputMultiplier > 0)
		appendLong("cache_read", "cache_read", p.CacheReadPricePerToken > 0)
	}
	return items
}

func (s *PublicPricingCatalogService) quoteItem(ctx context.Context, model string, groupID int64, resolved *ResolvedPricing, spec publicPricingItemSpec, rate float64) (decimal.Decimal, error) {
	input := CostInput{
		Ctx:            ctx,
		Model:          model,
		GroupID:        &groupID,
		RateMultiplier: rate,
		Resolver:       s.resolver,
		Resolved:       resolved,
		SizeTier:       spec.SizeTier,
	}

	if spec.Kind == "request" {
		input.RequestCount = 1
		input.Tokens.InputTokens = spec.SampleContext
		breakdown, err := s.billing.CalculateCostUnified(input)
		if err != nil {
			return decimal.Zero, err
		}
		return decimal.NewFromFloat(breakdown.ActualCost), nil
	}

	sample := spec.SampleContext
	if sample <= 0 {
		sample = 1
	}
	baseline := CostInput(input)
	var divisor int64 = 1
	switch spec.Kind {
	case "input":
		input.Tokens.InputTokens = sample
		divisor = int64(sample)
	case "image_input":
		input.Tokens.InputTokens = 1
		input.Tokens.ImageInputTokens = 1
	case "output", "image_output":
		if spec.LongContext || strings.Contains(spec.Key, ":interval:") {
			input.Tokens.InputTokens = sample
			baseline.Tokens.InputTokens = sample
		}
		input.Tokens.OutputTokens = 1
		if spec.Kind == "image_output" {
			input.Tokens.ImageOutputTokens = 1
		}
	case "cache_read":
		input.Tokens.CacheReadTokens = sample
		divisor = int64(sample)
	case "cache_write", "cache_write_5m", "cache_write_1h":
		input.Tokens.CacheCreationTokens = sample
		if spec.Kind == "cache_write_5m" {
			input.Tokens.CacheCreation5mTokens = sample
		}
		if spec.Kind == "cache_write_1h" {
			input.Tokens.CacheCreation1hTokens = sample
		}
		divisor = int64(sample)
	default:
		return decimal.Zero, fmt.Errorf("unsupported pricing item kind %q", spec.Kind)
	}

	breakdown, err := s.billing.CalculateCostUnified(input)
	if err != nil {
		return decimal.Zero, err
	}
	amount := decimal.NewFromFloat(breakdown.ActualCost)
	if (spec.Kind == "output" || spec.Kind == "image_output") && (spec.LongContext || strings.Contains(spec.Key, ":interval:")) {
		baseBreakdown, baseErr := s.billing.CalculateCostUnified(baseline)
		if baseErr != nil {
			return decimal.Zero, baseErr
		}
		amount = amount.Sub(decimal.NewFromFloat(baseBreakdown.ActualCost))
	}
	return amount.Div(decimal.NewFromInt(divisor)).Mul(decimal.NewFromInt(1_000_000)), nil
}

func officialPriceForSpec(official *officialPricingVersion, spec publicPricingItemSpec) (decimal.Decimal, bool) {
	if official == nil {
		return decimal.Zero, false
	}
	if price, ok := official.Items[spec.Key]; ok {
		return price, true
	}
	if strings.Contains(spec.Key, ":interval:") {
		// A channel interval is not automatically equivalent to the provider's
		// standard or long-context SKU. Without an exact catalog key, suppress
		// savings instead of guessing a comparison price.
		return decimal.Zero, false
	}
	return decimal.Zero, false
}

func publicPricingSampleContext(min int) int {
	if min > 0 {
		// PricingInterval.Matches assigns the exact boundary to the preceding
		// interval: (MinTokens, MaxTokens]. Quote one token above the lower
		// boundary so the catalog uses the same tier as settlement.
		return min + 1
	}
	return 1
}

func publicPricingRangeKey(min int, max *int) string {
	end := "inf"
	if max != nil {
		end = strconv.Itoa(*max)
	}
	return strconv.Itoa(min) + "-" + end
}

func normalizedPublicBillingMode(mode BillingMode) string {
	if mode == "" {
		return string(BillingModeToken)
	}
	return string(mode)
}

func providerForPricingModel(model string) string {
	model = strings.ToLower(model)
	switch {
	case strings.HasPrefix(model, "gpt-image"):
		return "image"
	case strings.HasPrefix(model, "gpt-"):
		return "openai"
	case strings.HasPrefix(model, "claude-"):
		return "anthropic"
	case strings.HasPrefix(model, "gemini-"):
		return "gemini"
	case strings.HasPrefix(model, "grok-"):
		return "grok"
	default:
		return "cn"
	}
}

func formatPricingDecimal(value decimal.Decimal, places int32) string {
	raw := value.StringFixed(places)
	raw = strings.TrimRight(raw, "0")
	raw = strings.TrimRight(raw, ".")
	if raw == "" || raw == "-0" {
		return "0"
	}
	return raw
}

func pricingCatalogVersion(catalog *PublicPricingCatalog) string {
	clone := *catalog
	clone.GeneratedAt = ""
	clone.DataVersion = ""
	encoded, _ := json.Marshal(clone)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:8])
}

func (s *PublicPricingCatalogService) setLastPublic(catalog *PublicPricingCatalog) {
	s.lastPublicMu.Lock()
	defer s.lastPublicMu.Unlock()
	s.lastPublic = clonePublicPricingCatalog(catalog)
}

func (s *PublicPricingCatalogService) getLastPublic() *PublicPricingCatalog {
	s.lastPublicMu.RLock()
	defer s.lastPublicMu.RUnlock()
	return clonePublicPricingCatalog(s.lastPublic)
}

func clonePublicPricingCatalog(catalog *PublicPricingCatalog) *PublicPricingCatalog {
	if catalog == nil {
		return nil
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		return nil
	}
	var clone PublicPricingCatalog
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil
	}
	return &clone
}

func sortedPricingGroupIDs(groups []PublicPricingGroup) []int64 {
	ids := make([]int64, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
