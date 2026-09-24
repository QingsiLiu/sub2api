package service

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// mappedAccountModelIDs is shared with GetAvailableModels. Nil retains the
// gateway's established platform-default fallback semantics.
func mappedAccountModelIDs(accounts []Account, platform string) []string {
	// Filter by platform if specified. Mixed scheduling (a gemini group routing
	// to antigravity accounts) is honoured here as well, so the advertised list
	// stays in sync with what the request path can actually serve.
	if platform != "" {
		filtered := make([]Account, 0)
		for _, acc := range accounts {
			if acc.Platform == platform || mixedListingAccountAllowed(platform, &acc) {
				filtered = append(filtered, acc)
			}
		}
		accounts = filtered
	}

	// Collect unique models from all accounts
	modelSet := make(map[string]struct{})
	hasAnyMapping := false

	for _, acc := range accounts {
		// Passthrough routing accepts models independently of model_mapping. A stale
		// mapping on any eligible passthrough account therefore cannot define the
		// public whitelist; return nil so the handler uses its default model set.
		if platform == PlatformOpenAI && acc.IsOpenAIPassthroughEnabled() {
			return nil
		}

		mapping := acc.GetModelMapping()
		for model := range mapping {
			// Accounts pulled in through mixed scheduling only contribute the
			// models that belong to the listing platform (e.g. an antigravity
			// account's claude-* mappings must not surface on a gemini group).
			if platform != "" && acc.Platform != platform && !mixedListingModelAllowed(platform, model) {
				continue
			}
			modelSet[model] = struct{}{}
			hasAnyMapping = true
		}
	}

	// If no account has model_mapping, return nil (use default)
	if !hasAnyMapping {
		return nil
	}

	// Convert to slice
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)

	if platform == PlatformOpenAI {
		models = supplementUnmappedOpenAIModels(accounts, models)
	}

	return models
}

// ModelListingSource is the gateway's allowlist source, shared with the plaza.
func ModelListingSource(platform string, available, defaults []string) []string {
	if len(available) == 0 {
		return defaults
	}
	if platform == PlatformAnthropic {
		return dedupeAndSortModelIDs(append(append([]string(nil), available...), defaults...))
	}
	return available
}

type GroupCatalogModel struct {
	Name     string
	Platform string
}

type GroupModelCatalog struct {
	accounts AccountRepository
	openai   *OpenAIGatewayService
}

func NewGroupModelCatalog(accounts AccountRepository, openai *OpenAIGatewayService) *GroupModelCatalog {
	return &GroupModelCatalog{accounts: accounts, openai: openai}
}

// List reads the current schedulable accounts rather than caching a second
// catalog. The same mapper/default/allowlist rules are used by /v1/models.
func (s *GroupModelCatalog) List(ctx context.Context, g *Group) ([]GroupCatalogModel, error) {
	if g.Platform == PlatformOpenAI && g.CodexModelsManifestConfig.Enabled && s.openai != nil {
		response, _, err := s.openai.FetchPinnedOpenAIModelsList(ctx, g, 3, "")
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body, &envelope); err != nil {
			return nil, err
		}
		out := make([]GroupCatalogModel, 0, len(envelope.Data))
		for _, model := range envelope.Data {
			out = append(out, GroupCatalogModel{Name: model.ID, Platform: g.Platform})
		}
		return out, nil
	}
	accounts, err := s.accounts.ListSchedulableByGroupID(ctx, g.ID)
	if err != nil {
		return nil, err
	}
	platforms := []string{g.Platform}
	if g.Platform == PlatformComposite {
		platforms = []string{}
		seen := map[string]bool{}
		for _, a := range accounts {
			if isConcreteRequestPlatform(a.Platform) && !seen[a.Platform] {
				platforms = append(platforms, a.Platform)
				seen[a.Platform] = true
			}
		}
		sort.Strings(platforms)
	}
	out := make([]GroupCatalogModel, 0)
	for _, platform := range platforms {
		models := mappedAccountModelIDs(accounts, platform)
		defaults := defaultModelsListCandidateIDs(platform)
		if g.Platform == PlatformComposite && IsMultiProtocolAPIKeyProvider(platform) {
			defaults = nil
		}
		if g.ModelAllowlist.Enabled {
			models = g.ModelAllowlist.FilterForListing(ModelListingSource(platform, models, defaults))
		} else if len(models) == 0 {
			models = defaults
		}
		for _, model := range models {
			model = strings.TrimSpace(model)
			if model != "" && !strings.Contains(model, "*") {
				out = append(out, GroupCatalogModel{Name: model, Platform: platform})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Platform < out[j].Platform
	})
	return out, nil
}
