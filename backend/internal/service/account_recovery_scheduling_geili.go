package service

import (
	"context"
	"errors"
	"strings"
)

func recoverySelectionAllowedGeili(ctx context.Context, err error) bool {
	used, _ := ctx.Value(accountRecoverySelectionContextGeili{}).(bool)
	return !used && !isAccountRecoveryProbeGeili(ctx) && errors.Is(err, ErrNoAvailableAccounts) && !strings.Contains(err.Error(), "pricing restriction") && RequestRecoveryAllowed(ctx)
}

func (s *GatewayService) SelectAccountWithLoadAwareness(ctx context.Context, groupID *int64, sessionHash, model string, excluded map[int64]struct{}, metadata string, userID int64) (*AccountSelectionResult, error) {
	selection, err := s.selectAccountWithLoadAwarenessGeiliBase(ctx, groupID, sessionHash, model, excluded, metadata, userID)
	r := s.rateLimitService.AccountRecoveryGeili()
	if r == nil || groupID == nil || !recoverySelectionAllowedGeili(ctx, err) {
		return selection, err
	}
	group, resolved, e := s.checkClaudeCodeRestriction(ctx, groupID)
	if e != nil || group == nil || resolved == nil {
		return selection, err
	}
	platform, forced, e := s.resolvePlatform(ctx, resolved, group, model)
	if e != nil {
		return selection, err
	}
	if group.Platform == PlatformComposite {
		decision, ok, routeErr := s.resolveCompositeRouteDecision(ctx, group, model, CompositeRouteEndpointAny)
		if routeErr != nil || !ok {
			return selection, err
		}
		ctx = WithCompositeRouteDecision(ctx, decision)
		if target, ok := CompositeTargetGroupIDFromContext(ctx); ok {
			resolved = &target
		}
	}
	mixed := !forced && (platform == PlatformAnthropic || platform == PlatformGemini)
	ctx = s.withGatewayProfitControlGate(s.withGroupContext(ctx, group), resolved)
	if s.checkChannelPricingRestriction(ctx, resolved, model) {
		return selection, err
	}
	eligible := func(ctx context.Context, a *Account) bool {
		if isPlatformFilteredForSelection(a, platform, mixed) {
			return false
		}
		copy := *a
		copy.Status, copy.Schedulable, copy.TempUnschedulableUntil, copy.RuntimeState = StatusActive, true, nil, nil
		copy.OverloadUntil = nil
		return s.isModelSupportedByAccountWithContext(ctx, &copy, model) && s.isAccountSchedulableForModelSelection(ctx, &copy, model) && s.isGatewayAccountProfitEligible(ctx, &copy) && !s.recoveryUpstreamChannelRestrictedGeili(ctx, resolved, &copy, model)
	}
	if !r.RecoverGroupGeili(ctx, *resolved, model, eligible) {
		return selection, err
	}
	return s.selectAccountWithLoadAwarenessGeiliBase(context.WithValue(ctx, accountRecoverySelectionContextGeili{}, true), resolved, sessionHash, model, recoveredAccountExclusionsGeili(ctx, excluded), metadata, userID)
}

func (s *OpenAIGatewayService) SelectAccountWithSchedulerForCapability(ctx context.Context, groupID *int64, previous, session, model string, excluded map[int64]struct{}, transport OpenAIUpstreamTransport, capability OpenAIEndpointCapability, compact, movable, useCost bool, override ...string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	selection, decision, err := s.selectAccountWithSchedulerForCapabilityGeiliBase(ctx, groupID, previous, session, model, excluded, transport, capability, compact, movable, useCost, override...)
	r := s.rateLimitService.AccountRecoveryGeili()
	if r == nil || groupID == nil || !recoverySelectionAllowedGeili(ctx, err) || compact || previous != "" && !movable || capability == OpenAIEndpointCapabilityEmbeddings || capability == OpenAIEndpointCapabilityGrokMediaGeneration {
		return selection, decision, err
	}
	if target, ok := CompositeTargetGroupIDFromContext(ctx); ok {
		groupID = &target
	}
	ctx = s.withOpenAIProfitControlGate(ctx, groupID)
	if s.checkChannelPricingRestriction(ctx, groupID, model) {
		return selection, decision, err
	}
	platform := PlatformOpenAI
	if len(override) > 0 {
		platform = override[0]
	}
	eligible := func(ctx context.Context, a *Account) bool {
		copy := *a
		copy.Status, copy.Schedulable, copy.TempUnschedulableUntil, copy.RuntimeState = StatusActive, true, nil, nil
		copy.OverloadUntil = nil
		if !s.isOpenAIAccountTransportCompatible(&copy, transport) {
			return false
		}
		if !isOpenAICompatibleAccountEligibleForRequestBeforeProfit(ctx, &copy, platform, model, false, capability) {
			return false
		}
		veto, _ := openAIProfitControlVetoReason(ctx, &copy)
		return !veto && !s.recoveryUpstreamChannelRestrictedGeili(ctx, groupID, &copy, model)
	}
	if !r.RecoverGroupGeili(ctx, *groupID, model, eligible) {
		return selection, decision, err
	}
	return s.selectAccountWithSchedulerForCapabilityGeiliBase(context.WithValue(ctx, accountRecoverySelectionContextGeili{}, true), groupID, previous, session, model, recoveredAccountExclusionsGeili(ctx, excluded), transport, capability, compact, movable, useCost, override...)
}

// Match the normal scheduler's billing-model gate: requested/channel pricing
// was checked before the search; only upstream billing needs an account mapping.
func (s *GatewayService) recoveryUpstreamChannelRestrictedGeili(ctx context.Context, group *int64, account *Account, model string) bool {
	return s.needsUpstreamChannelRestrictionCheck(ctx, group) && s.isUpstreamModelRestrictedByChannel(ctx, *group, account, model)
}
func (s *OpenAIGatewayService) recoveryUpstreamChannelRestrictedGeili(ctx context.Context, group *int64, account *Account, model string) bool {
	return s.needsUpstreamChannelRestrictionCheck(ctx, group) && s.isUpstreamModelRestrictedByChannel(ctx, *group, account, model, false)
}
