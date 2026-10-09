//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestInstantQuotaGeiliWrappedHTTPHasRecoverableCooldown(t *testing.T) {
	for _, status := range []int{400, 502} {
		for _, typ := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, pool := range []bool{false, true} {
				repo := &oauth429RateLimitRepo{}
				svc := &OpenAIGatewayService{rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil)}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: typ, Extra: map[string]any{"pool_mode": pool}}
				svc.rateLimitService.runtimeBlocker = svc
				start := time.Now()
				disabled := svc.handleOpenAIAccountUpstreamError(context.Background(), account, status, nil, []byte(instantQuotaFixtureGeili), "gpt-6-astra")
				require.False(t, disabled, "instant quota does not invalidate credentials")
				require.Equal(t, 1, repo.setRateLimitedCalls)
				require.True(t, repo.lastRateLimitedUntil.After(start))
				require.Less(t, repo.lastRateLimitedUntil.Sub(start), time.Minute, "missing reset uses bounded temporary fallback")
				require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
				require.False(t, svc.ShouldRetryOpenAIOAuth429(account, nil, []byte(instantQuotaFixtureGeili)))
			}
		}
	}
}

type instantQuotaRuleRepoGeili struct {
	oauth429RateLimitRepo
	modelCalls int
	model      string
	until      time.Time
}

func (r *instantQuotaRuleRepoGeili) SetModelRateLimit(_ context.Context, _ int64, model string, until time.Time, _ ...string) error {
	r.modelCalls++
	r.model, r.until = model, until
	return nil
}

func TestInstantQuotaGeiliOriginalHTTPRulePrecedesFallback(t *testing.T) {
	for _, status := range []int{400, 502} {
		for _, typ := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, pool := range []bool{false, true} {
				repo := &instantQuotaRuleRepoGeili{}
				svc := &OpenAIGatewayService{rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil)}
				svc.rateLimitService.runtimeBlocker = svc
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: typ,
					Extra: map[string]any{"pool_mode": pool}, Credentials: map[string]any{
						"temp_unschedulable_enabled": true,
						"temp_unschedulable_rules": []any{map[string]any{"error_code": status,
							"keywords": []any{"Insufficient quota available for instant inference."}, "duration_minutes": 1}}}}
				start := time.Now()
				require.True(t, svc.handleOpenAIAccountUpstreamError(context.Background(), account, status, nil, []byte(instantQuotaFixtureGeili), "gpt-6-astra"))
				require.Equal(t, 1, repo.modelCalls, "the explicit original-status rule must match")
				require.Equal(t, "gpt-6-astra", repo.model)
				require.InDelta(t, time.Minute.Seconds(), repo.until.Sub(start).Seconds(), 2)
				require.Zero(t, repo.setRateLimitedCalls, "fallback must not override the explicit rule")
				require.False(t, svc.isOpenAIAccountRuntimeBlocked(account), "the explicit known-model rule stays model scoped")
			}
		}
	}
}

func TestAPIKeyResponsesGeiliBareErrorKeepsPassthroughRules(t *testing.T) {
	for _, suffix := range []string{"", "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"type\":\"invalid_request_error\",\"message\":\"Invalid input\"}}}\n\n"} {
		c, rec := newOpenAIStreamFailedTestContext()
		bindPassthroughRule(c, PlatformOpenAI, []string{"Invalid input"}, http.StatusTeapot)
		body := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"Invalid input\"}}\n\n" + suffix
		svc := newOpenAIStreamFailedTestService(0)
		_, err := svc.handleStreamingResponse(c.Request.Context(), &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, time.Now(), "gpt-6-astra", "gpt-6-astra")
		require.ErrorContains(t, err, "passthrough")
		require.Equal(t, http.StatusTeapot, rec.Code)
		require.NotContains(t, rec.Body.String(), "response.failed")
		require.Contains(t, rec.Body.String(), "Invalid input")
	}
}
