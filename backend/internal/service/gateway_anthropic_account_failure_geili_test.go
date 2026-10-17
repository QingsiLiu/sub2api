package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const anthropicRelayQuotaBodyGeili = `{"error":{"message":"Provider API error: You have reached your specified API usage limits. You will regain access on 2026-11-01 at 00:00 UTC.","type":"<nil>"},"type":"error"}`

func TestClassifyAnthropicAccountScopedFailureGeili(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		matched bool
		reason  GatewayFailureReason
	}{
		{"relay usage limits", 400, anthropicRelayQuotaBodyGeili, true, geminiSupplierCapacityReasonGeili},
		{"workspace usage limits", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"You have reached your specified workspace API usage limits. You will regain access on 2026-11-01 at 00:00 UTC."}}`, true, geminiSupplierCapacityReasonGeili},
		{"credit balance", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"Provider API error: Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits."}}`, true, geminiSupplierCapacityReasonGeili},
		{"credit balance 402", 402, `{"type":"error","error":{"type":"billing_error","message":"Your credit balance is too low to access the Anthropic API."}}`, true, geminiSupplierCapacityReasonGeili},
		{"relay model_not_found", 404, `{"error":{"message":"Model \"claude-opus-4-5\" is not supported by any configured account in this group","type":"model_not_found"},"type":"error"}`, true, ""},
		{"relay model_not_found 400", 400, `{"error":{"code":"model_not_found","message":"no channel"}}`, true, ""},
		{"official unknown model", 404, `{"type":"error","error":{"type":"not_found_error","message":"model: claude-x"}}`, true, ""},
		{"ordinary invalid request", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: must be greater than 0"}}`, false, ""},
		{"other not found", 404, `{"type":"error","error":{"type":"not_found_error","message":"File not found"}}`, false, ""},
		{"quota text only in echoed prompt", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"messages.0: bad role"},"echo":"You have reached your specified API usage limits"}`, false, ""},
		{"plain text body", 400, `You have reached your specified API usage limits`, false, ""},
		{"quota on 404 is not model failure", 404, anthropicRelayQuotaBodyGeili, false, ""},
		{"status outside scope", 401, anthropicRelayQuotaBodyGeili, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, matched := classifyAnthropicAccountScopedFailureGeili(tc.status, []byte(tc.body))
			require.Equal(t, tc.matched, matched)
			require.Equal(t, tc.reason, reason)
		})
	}
}

func newAnthropicAccountScopedForwardGeili(t *testing.T, status int, payload string) (*GatewayService, *gin.Context, *httptest.ResponseRecorder, *ParsedRequest) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	body := []byte(`{"model":"claude-3-5-sonnet-latest","max_tokens":16,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"rid-quota"}}, Body: io.NopCloser(strings.NewReader(payload))}}
	svc := newForwardPartialUsageServiceForTest(upstream)
	svc.settingService = NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{}}, svc.cfg)
	return svc, c, rec, parsed
}

func TestAnthropicForwardRelayQuotaFailsOverWithoutWritingGeili(t *testing.T) {
	svc, c, rec, parsed := newAnthropicAccountScopedForwardGeili(t, http.StatusBadRequest, anthropicRelayQuotaBodyGeili)
	account := newAnthropicAPIKeyAccountForTest()
	delete(account.Extra, "anthropic_passthrough")

	_, err := svc.Forward(context.Background(), c, account, parsed)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, http.StatusBadRequest, failover.StatusCode)
	require.True(t, failover.ShouldRetryNextAccount())
	require.False(t, failover.RetryableOnSameAccount, "switching accounts must not trigger the same-account temp ban")
	require.True(t, IsGeminiSupplierUnavailableGeili(failover), "exhaustion uses the shared 503 upstream_unavailable contract")
	require.False(t, failover.ShouldReportAccountScheduleFailure())
	require.Equal(t, http.StatusServiceUnavailable, failover.ClientStatusCode)
	require.Equal(t, anthropicRelayQuotaBodyGeili, string(failover.ResponseBody))
	require.Empty(t, rec.Body.String(), "nothing reaches the client before failover")

	raw, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events := raw.([]*OpsUpstreamErrorEvent)
	require.Len(t, events, 1)
	require.Equal(t, "failover", events[0].Kind)
	require.Equal(t, int64(account.ID), events[0].AccountID)
	require.Equal(t, "rid-quota", events[0].UpstreamRequestID)
}

func TestAnthropicForwardRelayModelNotFoundFailsOverGeili(t *testing.T) {
	payload := `{"error":{"message":"Model \"claude-haiku-4-5\" is not supported by any configured account in this group","type":"model_not_found"},"type":"error"}`
	svc, c, rec, parsed := newAnthropicAccountScopedForwardGeili(t, http.StatusNotFound, payload)
	account := newAnthropicAPIKeyAccountForTest()
	delete(account.Extra, "anthropic_passthrough")

	_, err := svc.Forward(context.Background(), c, account, parsed)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, http.StatusNotFound, failover.StatusCode)
	require.False(t, IsGeminiSupplierUnavailableGeili(failover), "an unsupported model keeps the original exhaustion mapping")
	require.Empty(t, rec.Body.String())
}

func TestAnthropicForwardOrdinary400StaysFinalGeili(t *testing.T) {
	payload := `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: must be greater than 0"}}`
	svc, c, rec, parsed := newAnthropicAccountScopedForwardGeili(t, http.StatusBadRequest, payload)
	account := newAnthropicAPIKeyAccountForTest()
	delete(account.Extra, "anthropic_passthrough")

	_, err := svc.Forward(context.Background(), c, account, parsed)
	require.Error(t, err)
	var failover *UpstreamFailoverError
	require.NotErrorAs(t, err, &failover)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "max_tokens")
}

func TestStrictAnthropicRelayQuotaMovesNativeBytesGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(anthropicRelayQuotaBodyGeili))}}
	svc := newForwardPartialUsageServiceForTest(upstream)
	svc.rateLimitService = nil
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	account := newAnthropicAPIKeyAccountForTest()

	_, err := svc.forwardAnthropicAPIKeyPassthrough(context.Background(), c, account, []byte(strictAnthropicBody), "claude-fable-5-1", "claude-fable-5-1", false, time.Now())
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.True(t, failover.StrictPassthrough)
	require.Equal(t, anthropicRelayQuotaBodyGeili, string(failover.ResponseBody))
	require.Empty(t, rec.Body.String())
	require.True(t, WriteStrictAnthropicFailoverGeili(c, failover, false))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, anthropicRelayQuotaBodyGeili, rec.Body.String())
}
