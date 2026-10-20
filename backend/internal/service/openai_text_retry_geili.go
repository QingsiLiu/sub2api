package service

import (
	"context"
	"strings"
	"time"
)

const openAITextModelUnavailableCooldownGeili = 5 * time.Second

// A relay's model-not-found response can be transient even for an explicitly
// configured model. Keep a short backoff instead of removing text capacity for
// half an hour. OAuth plan gates and media retain their existing policies.
func openAITextModelUnavailableGeili(a *Account, model, reason string) bool {
	return a != nil && a.Platform == PlatformOpenAI && a.Type == AccountTypeAPIKey &&
		strings.TrimSpace(model) != "" && !isOpenAIImageGenerationModel(model) &&
		!strings.Contains(strings.ToLower(model), "sora") &&
		(reason == upstreamModelNotFoundReason || reason == upstreamModelNotFound401Reason)
}

func openAITextModelCooldownResetGeili(a *Account, scope string, state map[string]any, reset time.Time) time.Time {
	reason, _ := state["reason"].(string)
	if !openAITextModelUnavailableGeili(a, scope, reason) {
		return reset
	}
	raw, _ := state["rate_limited_at"].(string)
	started, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return reset
	}
	if short := started.Add(openAITextModelUnavailableCooldownGeili); short.Before(reset) {
		return short
	}
	return reset
}

// Only failures safe for a later inference attempt may re-enter a spent pool.
// Credential, billing, bound-state and explicitly final failures remain final.
func CanRetryOpenAITextFailureGeili(a *Account, failure *UpstreamFailoverError) bool {
	if a == nil || a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey || failure == nil ||
		!failure.ShouldRetryNextAccount() || failure.IsCredentialFailure() || failure.StrictPassthrough || failure.BoundUpstreamPassthrough {
		return false
	}
	body := strings.ToLower(string(failure.ResponseBody))
	for _, token := range []string{"insufficient_quota", "usage_limit_reached", "insufficient_balance", "invalid_api_key", "billing_hard_limit", "credit balance", "余额不足", "欠费"} {
		if strings.Contains(body, token) {
			return false
		}
	}
	switch failure.StatusCode {
	case 429, 500, 502, 503, 504, 520, 521, 522, 523, 524:
		return true
	case 404:
		return isUpstreamModelNotFoundError(failure.StatusCode, failure.ResponseBody)
	default:
		return false
	}
}

type OpenAITextRetryPlanGeili struct {
	Delay      time.Duration
	AccountIDs []int64
}

// OpenAITextRetryPlan is an error-path lookup. It never admits an account:
// after the wait the ordinary scheduler rechecks every gate and owns selection.
// Exclusions from permanent failures, profit vetoes or protocol mismatches stay
// excluded. Normal successful requests do not perform this lookup.
func (s *OpenAIGatewayService) OpenAITextRetryPlanGeili(ctx context.Context, groupID *int64, model string, excluded, retryable map[int64]struct{}, capability OpenAIEndpointCapability) OpenAITextRetryPlanGeili {
	plan := OpenAITextRetryPlanGeili{}
	if s == nil || s.accountRepo == nil || requestRecoveryFromContext(ctx) == nil ||
		!RequestRecoveryAllowed(ctx) || !AccountRecoveryScopeAllowedGeili(ctx) ||
		OpenAIEncryptedRoutePinnedGeili(ctx) || OpenAIImageGenerationIntentFromContext(ctx) ||
		strings.TrimSpace(model) == "" || isOpenAIImageGenerationModel(model) {
		return plan
	}
	if target, ok := CompositeTargetGroupIDFromContext(ctx); ok {
		groupID = &target
	}
	ctx = s.withOpenAIProfitControlGate(ctx, groupID)
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	if s.checkChannelPricingRestriction(ctx, groupID, model) {
		return plan
	}
	queryGroup, includeGrouped := groupID, false
	if s.cfg != nil && s.cfg.RunMode == "simple" {
		queryGroup, includeGrouped = nil, true
	}
	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(ctx, queryGroup, []string{PlatformOpenAI}, includeGrouped)
	if err != nil {
		return plan
	}
	requirePrivacy := s.openAIGroupRequiresPrivacySet(ctx, groupID)
	now := time.Now()
	for i := range accounts {
		a := &accounts[i]
		if a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey || a.IsShadow() || encryptedRouteAccountUnavailableGeili(a) ||
			requirePrivacy && !a.IsPrivacySet() {
			continue
		}
		if _, failed := excluded[a.ID]; failed {
			if _, safe := retryable[a.ID]; !safe {
				continue
			}
		}
		until := s.encryptedRouteCooldownUntilGeili(ctx, a, model)
		// Genuine long quota windows and operator-specified long pauses are not
		// treated as short relay faults. Never clear their authoritative state.
		if until.Sub(now) > time.Minute {
			continue
		}
		copy := *a
		copy.RateLimitResetAt, copy.OverloadUntil, copy.TempUnschedulableUntil = nil, nil, nil
		copy.Extra = make(map[string]any, len(a.Extra))
		for k, v := range a.Extra {
			if k != modelRateLimitsKey {
				copy.Extra[k] = v
			}
		}
		if !isOpenAICompatibleAccountEligibleForRequest(ctx, &copy, PlatformOpenAI, model, false, capability) ||
			s.recoveryUpstreamChannelRestrictedGeili(ctx, groupID, &copy, model) {
			continue
		}
		delay := until.Sub(now) + 25*time.Millisecond
		if delay < time.Second {
			delay = time.Second
		}
		if plan.Delay == 0 || delay < plan.Delay {
			plan.Delay = delay
		}
		if _, failed := excluded[a.ID]; failed {
			plan.AccountIDs = append(plan.AccountIDs, a.ID)
		}
	}
	return plan
}
