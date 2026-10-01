package service

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// NormalizeModelPlazaConfig validates and canonicalizes the presentation-only
// model selection stored on a group.
func NormalizeModelPlazaConfig(cfg GroupModelPlazaConfig) (GroupModelPlazaConfig, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = GroupModelPlazaModeAll
	}
	if mode != GroupModelPlazaModeAll && mode != GroupModelPlazaModeSelected {
		return GroupModelPlazaConfig{}, infraerrors.New(http.StatusBadRequest, "INVALID_MODEL_PLAZA_CONFIG", "model plaza mode must be all or selected")
	}

	seen := make(map[string]struct{}, len(cfg.Models))
	models := make([]string, 0, len(cfg.Models))
	for _, model := range cfg.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		models = append(models, model)
	}
	if mode == GroupModelPlazaModeSelected && len(models) == 0 {
		return GroupModelPlazaConfig{}, infraerrors.New(http.StatusBadRequest, "INVALID_MODEL_PLAZA_CONFIG", "selected model plaza mode requires at least one model")
	}
	if mode == GroupModelPlazaModeAll {
		models = nil
	}
	return GroupModelPlazaConfig{Mode: mode, Models: models}, nil
}

// FilterModelPlazaModels filters a discovered model list according to
// presentation settings while preserving discovery order.
func FilterModelPlazaModels(c GroupModelPlazaConfig, source []string) []string {
	if strings.ToLower(strings.TrimSpace(c.Mode)) != GroupModelPlazaModeSelected {
		return source
	}
	allowed := make(map[string]struct{}, len(c.Models))
	for _, model := range c.Models {
		allowed[strings.ToLower(strings.TrimSpace(model))] = struct{}{}
	}
	out := make([]string, 0, len(source))
	for _, model := range source {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(model))]; ok {
			out = append(out, model)
		}
	}
	return out
}

const (
	ModelPlazaCurrencyUSD = "usd"
	ModelPlazaCurrencyCNY = "cny"

	defaultModelPlazaUSDCNYRate    = 6.8
	defaultModelPlazaQuotaUSDPerCN = 1.0
	maxModelPlazaConfigIDs         = 200
	maxModelPlazaOfficialOverrides = 200
	maxModelPlazaOverrideNoteRunes = 40
)

// ModelPlazaOfficialOverride is an admin-entered official reference price for one
// model. Prices are per million tokens in Currency; nil fields fall back to the
// pulled official value (USD only). Display-only: it never affects billing.
type ModelPlazaOfficialOverride struct {
	Model      string   `json:"model"`
	Currency   string   `json:"currency"`
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
	Note       string   `json:"note,omitempty"`
}

// ModelPlazaGeiliConfig is the single JSON setting behind the public plaza's
// Geili additions: which groups are listed, which are priced natively in CNY,
// the CNY comparison rates and manual official-price overrides.
type ModelPlazaGeiliConfig struct {
	// GroupWhitelist empty means the public plaza lists no group (fail-closed).
	GroupWhitelist    []int64                      `json:"group_whitelist"`
	CNYGroupIDs       []int64                      `json:"cny_group_ids"`
	USDCNYRate        float64                      `json:"usd_cny_rate"`
	QuotaUSDPerCNY    float64                      `json:"quota_usd_per_cny"`
	OfficialOverrides []ModelPlazaOfficialOverride `json:"official_overrides"`
}

// DefaultModelPlazaGeiliConfig is the fail-closed default: no group is listed.
func DefaultModelPlazaGeiliConfig() ModelPlazaGeiliConfig {
	return ModelPlazaGeiliConfig{
		GroupWhitelist:    []int64{},
		CNYGroupIDs:       []int64{},
		USDCNYRate:        defaultModelPlazaUSDCNYRate,
		QuotaUSDPerCNY:    defaultModelPlazaQuotaUSDPerCN,
		OfficialOverrides: []ModelPlazaOfficialOverride{},
	}
}

// ParseModelPlazaGeiliConfig reads the stored JSON. Anything unreadable yields
// the fail-closed default rather than an error, so a bad row never breaks the
// settings page or exposes extra groups.
func ParseModelPlazaGeiliConfig(raw string) ModelPlazaGeiliConfig {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultModelPlazaGeiliConfig()
	}
	var cfg ModelPlazaGeiliConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return DefaultModelPlazaGeiliConfig()
	}
	normalized, err := NormalizeModelPlazaGeiliConfig(cfg)
	if err != nil {
		return DefaultModelPlazaGeiliConfig()
	}
	return normalized
}

// MarshalModelPlazaGeiliConfig serializes a normalized config for storage.
func MarshalModelPlazaGeiliConfig(cfg ModelPlazaGeiliConfig) (string, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func invalidModelPlazaGeiliConfig(msg string) error {
	return infraerrors.New(http.StatusBadRequest, "INVALID_MODEL_PLAZA_GEILI_CONFIG", msg)
}

func normalizePlazaGroupIDs(ids []int64, field string) ([]int64, error) {
	if len(ids) > maxModelPlazaConfigIDs {
		return nil, invalidModelPlazaGeiliConfig(field + " has too many groups")
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, invalidModelPlazaGeiliConfig(field + " contains an invalid group id")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func normalizePlazaOverridePrice(v *float64, model string) (*float64, error) {
	if v == nil {
		return nil, nil
	}
	if math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 {
		return nil, invalidModelPlazaGeiliConfig("official price for " + model + " must be a non-negative number")
	}
	out := *v
	return &out, nil
}

// NormalizeModelPlazaGeiliConfig validates and canonicalizes the config.
func NormalizeModelPlazaGeiliConfig(cfg ModelPlazaGeiliConfig) (ModelPlazaGeiliConfig, error) {
	var err error
	out := DefaultModelPlazaGeiliConfig()
	if out.GroupWhitelist, err = normalizePlazaGroupIDs(cfg.GroupWhitelist, "group_whitelist"); err != nil {
		return ModelPlazaGeiliConfig{}, err
	}
	if out.CNYGroupIDs, err = normalizePlazaGroupIDs(cfg.CNYGroupIDs, "cny_group_ids"); err != nil {
		return ModelPlazaGeiliConfig{}, err
	}

	out.USDCNYRate = cfg.USDCNYRate
	if out.USDCNYRate == 0 {
		out.USDCNYRate = defaultModelPlazaUSDCNYRate
	}
	out.QuotaUSDPerCNY = cfg.QuotaUSDPerCNY
	if out.QuotaUSDPerCNY == 0 {
		out.QuotaUSDPerCNY = defaultModelPlazaQuotaUSDPerCN
	}
	for _, rate := range []float64{out.USDCNYRate, out.QuotaUSDPerCNY} {
		if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
			return ModelPlazaGeiliConfig{}, invalidModelPlazaGeiliConfig("exchange rates must be positive numbers")
		}
	}

	if len(cfg.OfficialOverrides) > maxModelPlazaOfficialOverrides {
		return ModelPlazaGeiliConfig{}, invalidModelPlazaGeiliConfig("too many official price overrides")
	}
	seen := make(map[string]struct{}, len(cfg.OfficialOverrides))
	for _, o := range cfg.OfficialOverrides {
		model := strings.TrimSpace(o.Model)
		if model == "" {
			return ModelPlazaGeiliConfig{}, invalidModelPlazaGeiliConfig("official price override requires a model name")
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			return ModelPlazaGeiliConfig{}, invalidModelPlazaGeiliConfig("duplicate official price override for " + model)
		}
		seen[key] = struct{}{}

		currency := strings.ToLower(strings.TrimSpace(o.Currency))
		if currency == "" {
			currency = ModelPlazaCurrencyUSD
		}
		if currency != ModelPlazaCurrencyUSD && currency != ModelPlazaCurrencyCNY {
			return ModelPlazaGeiliConfig{}, invalidModelPlazaGeiliConfig("official price currency must be usd or cny")
		}
		note := strings.TrimSpace(o.Note)
		if utf8.RuneCountInString(note) > maxModelPlazaOverrideNoteRunes {
			return ModelPlazaGeiliConfig{}, invalidModelPlazaGeiliConfig("official price note is too long")
		}
		item := ModelPlazaOfficialOverride{Model: model, Currency: currency, Note: note}
		if item.Input, err = normalizePlazaOverridePrice(o.Input, model); err != nil {
			return ModelPlazaGeiliConfig{}, err
		}
		if item.Output, err = normalizePlazaOverridePrice(o.Output, model); err != nil {
			return ModelPlazaGeiliConfig{}, err
		}
		if item.CacheRead, err = normalizePlazaOverridePrice(o.CacheRead, model); err != nil {
			return ModelPlazaGeiliConfig{}, err
		}
		if item.CacheWrite, err = normalizePlazaOverridePrice(o.CacheWrite, model); err != nil {
			return ModelPlazaGeiliConfig{}, err
		}
		out.OfficialOverrides = append(out.OfficialOverrides, item)
	}
	return out, nil
}

// PlazaGroupCurrency returns the display currency of a group's paid prices.
func (c ModelPlazaGeiliConfig) PlazaGroupCurrency(groupID int64) string {
	for _, id := range c.CNYGroupIDs {
		if id == groupID {
			return ModelPlazaCurrencyCNY
		}
	}
	return ModelPlazaCurrencyUSD
}

// whitelistSet is the lookup used to filter public plaza groups.
func (c ModelPlazaGeiliConfig) whitelistSet() map[int64]struct{} {
	set := make(map[int64]struct{}, len(c.GroupWhitelist))
	for _, id := range c.GroupWhitelist {
		set[id] = struct{}{}
	}
	return set
}

// overrideIndex keys overrides by lower-cased model name.
func (c ModelPlazaGeiliConfig) overrideIndex() map[string]ModelPlazaOfficialOverride {
	idx := make(map[string]ModelPlazaOfficialOverride, len(c.OfficialOverrides))
	for _, o := range c.OfficialOverrides {
		idx[strings.ToLower(o.Model)] = o
	}
	return idx
}

const plazaPerMillion = 1_000_000.0

func perTokenPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	out := *v / plazaPerMillion
	return &out
}

// applyPlazaOfficialOverride merges an admin override (per-million prices) into
// the pulled official price (per-token USD). A USD override patches only the
// fields it sets, so unset fields keep the pulled value. A CNY override replaces
// the whole reference: pulled USD values and tiers would be meaningless next to
// CNY numbers. The base is cloned and never mutated (it is memoized).
func applyPlazaOfficialOverride(base *PlazaOfficialPricing, o ModelPlazaOfficialOverride) *PlazaOfficialPricing {
	if o.Currency == ModelPlazaCurrencyCNY {
		return &PlazaOfficialPricing{
			InputPrice:      perTokenPtr(o.Input),
			OutputPrice:     perTokenPtr(o.Output),
			CacheReadPrice:  perTokenPtr(o.CacheRead),
			CacheWritePrice: perTokenPtr(o.CacheWrite),
			Currency:        ModelPlazaCurrencyCNY,
			Note:            o.Note,
		}
	}
	out := PlazaOfficialPricing{}
	if base != nil {
		out = *base
	}
	if o.Input != nil {
		out.InputPrice = perTokenPtr(o.Input)
	}
	if o.Output != nil {
		out.OutputPrice = perTokenPtr(o.Output)
	}
	if o.CacheRead != nil {
		out.CacheReadPrice = perTokenPtr(o.CacheRead)
	}
	if o.CacheWrite != nil {
		out.CacheWritePrice = perTokenPtr(o.CacheWrite)
		// A manual write price supersedes the pulled 5m/1h split.
		out.CacheWrite1hPrice = nil
	}
	out.Note = o.Note
	return &out
}
