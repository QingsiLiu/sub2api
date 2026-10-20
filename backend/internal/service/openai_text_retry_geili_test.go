package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type textRetryRepoGeili struct {
	AccountRepository
	accounts []Account
}

func (r textRetryRepoGeili) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]Account, error) {
	return r.accounts, nil
}

func textRetryAccountGeili(id int64) Account {
	return Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}}
}

func TestOpenAITextRetryGeiliLegacyModelCooldownExpiresQuickly(t *testing.T) {
	for _, reason := range []string{upstreamModelNotFoundReason, upstreamModelNotFound401Reason} {
		a := textRetryAccountGeili(1)
		now := time.Now()
		setAccountModelRateLimitSnapshot(&a, "gpt-6-astra", now.Add(30*time.Minute), reason, now.Add(-10*time.Second))
		require.Zero(t, a.GetModelRateLimitRemainingTime("gpt-6-astra"))
		require.True(t, a.IsSchedulableForModelWithContext(context.Background(), "gpt-6-astra"))
		// Read-time compatibility never rewrites stored operational evidence.
		require.Equal(t, now.Add(30*time.Minute).UTC().Format(time.RFC3339), a.Extra[modelRateLimitsKey].(map[string]any)["gpt-6-astra"].(map[string]any)["rate_limit_reset_at"])
	}
}

func TestOpenAITextRetryGeiliPreservesOAuthMediaAndActualRateLimits(t *testing.T) {
	for _, tc := range []struct{ platform, kind, model, reason string }{
		{PlatformOpenAI, AccountTypeOAuth, "gpt-6-astra", upstreamModelNotFoundReason},
		{PlatformOpenAI, AccountTypeAPIKey, "gpt-image-2", upstreamModelNotFoundReason},
		{PlatformOpenAI, AccountTypeAPIKey, "gpt-6-astra", "upstream_429"},
		{PlatformOpenAI, AccountTypeAPIKey, "gpt-6-astra", upstreamCodexPlanGatedModelReason},
		{PlatformAnthropic, AccountTypeAPIKey, "claude-opus-5", upstreamModelNotFoundReason},
	} {
		a := textRetryAccountGeili(1)
		a.Platform, a.Type = tc.platform, tc.kind
		setAccountModelRateLimitSnapshot(&a, tc.model, time.Now().Add(30*time.Minute), tc.reason, time.Now().Add(-10*time.Second))
		require.Greater(t, a.GetModelRateLimitRemainingTime(tc.model), 29*time.Minute, tc.model)
	}
}

func TestOpenAITextRetryGeiliTextModelWithImageToolRetainsMediaCooldown(t *testing.T) {
	a := textRetryAccountGeili(1)
	setAccountModelRateLimitSnapshot(&a, "gpt-6-astra", time.Now().Add(30*time.Minute), upstreamModelNotFoundReason, time.Now().Add(-10*time.Second))
	ctx := WithOpenAIImageGenerationIntent(context.Background())
	require.Greater(t, a.GetModelRateLimitRemainingTimeWithContext(ctx, "gpt-6-astra"), 29*time.Minute)
	require.False(t, a.IsSchedulableForModelWithContext(ctx, "gpt-6-astra"))
	require.Zero(t, a.GetModelRateLimitRemainingTime("gpt-6-astra"), "ordinary text can recover without changing the stored media record")
}

func TestOpenAITextRetryGeiliPlanRetainsPermanentExclusionsAndGates(t *testing.T) {
	a, b, c := textRetryAccountGeili(1), textRetryAccountGeili(2), textRetryAccountGeili(3)
	until := time.Now().Add(150 * time.Millisecond)
	a.RateLimitResetAt = &until
	b.Schedulable = false
	c.Credentials = map[string]any{"model_mapping": map[string]any{"gpt-5.6-sol": "gpt-5.6-sol"}}
	s := &OpenAIGatewayService{accountRepo: textRetryRepoGeili{accounts: []Account{a, b, c}}, cfg: &config.Config{RunMode: config.RunModeSimple}}
	ctx := WithRequestRecovery(context.Background(), 5*time.Second)
	excluded := map[int64]struct{}{1: {}, 2: {}}
	require.Zero(t, s.OpenAITextRetryPlanGeili(ctx, nil, "gpt-6-astra", excluded, nil, "").Delay)
	plan := s.OpenAITextRetryPlanGeili(ctx, nil, "gpt-6-astra", excluded, map[int64]struct{}{1: {}, 2: {}}, "")
	require.GreaterOrEqual(t, plan.Delay, time.Second)
	require.Equal(t, []int64{1}, plan.AccountIDs)
	require.Len(t, excluded, 2, "planning must not mutate exclusions or admission state")
	require.Zero(t, s.OpenAITextRetryPlanGeili(WithHTTPAccountRecoveryScopeGeili(ctx, false), nil, "gpt-6-astra", excluded, map[int64]struct{}{1: {}}, "").Delay)
	long := time.Now().Add(time.Hour)
	s.accountRepo = textRetryRepoGeili{accounts: []Account{{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, RateLimitResetAt: &long}}}
	require.Zero(t, s.OpenAITextRetryPlanGeili(ctx, nil, "gpt-6-astra", nil, nil, "").Delay)
}

func TestOpenAITextRetryGeiliFailureClassification(t *testing.T) {
	a := textRetryAccountGeili(1)
	for _, code := range []int{429, 500, 502, 503, 504} {
		require.True(t, CanRetryOpenAITextFailureGeili(&a, &UpstreamFailoverError{StatusCode: code}))
	}
	for _, failure := range []*UpstreamFailoverError{
		{StatusCode: http.StatusUnauthorized}, {StatusCode: http.StatusForbidden},
		{StatusCode: 429, ResponseBody: []byte(`{"error":{"code":"insufficient_quota"}}`)},
		{StatusCode: 502, NextAccountAction: NextAccountStop},
		{StatusCode: 502, Stage: GatewayFailureStageAccountAuth},
		{StatusCode: 502, StrictPassthrough: true}, {StatusCode: 502, BoundUpstreamPassthrough: true},
	} {
		require.False(t, CanRetryOpenAITextFailureGeili(&a, failure))
	}
}
