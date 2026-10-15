package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Includes the exact shapes that used to be removed: implicit thinking,
// redacted data, empty text, deferred cache markers and beta-gated fields.
const strictAnthropicBody = `{
 "model":"claude-fable-5-1", "max_tokens":4096,
 "messages":[{"role":"user","content":[{"type":"text","text":""},{"type":"text","text":"hi"}]},{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"opaque_cc_ses_signature"},{"type":"redacted_thinking","data":"opaque_cc_sess_data"},{"type":"tool_use","id":"tool_1","name":"cc_sess_audit","input":{"text":"cc_ses_marker","number":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":[{"type":"text","text":""},{"type":"text","text":"ok"}]}]}],
 "tools":[{"name":"cc_sess_audit","defer_loading":true,"cache_control":{"type":"ephemeral"},"input_schema":{"type":"object"}}],
 "context_management":{"future":"keep"},"fallbacks":["future"],"fallback_credit_token":"opaque","future_extension":{"keep":true}
}`

func TestStrictAnthropicRequestBytesHeadersAndQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"/messages", "/messages/count_tokens"} {
		t.Run(endpoint, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1"+endpoint+"?beta=false&future=a%2Fb&future=two", nil)
			c.Request.Header.Set("Authorization", "Bearer caller-secret")
			c.Request.Header.Set("Accept-Encoding", "gzip, br")
			c.Request.Header.Set("Cookie", "caller-session")
			c.Request.Header.Set("Connection", "X-Connection-Only")
			c.Request.Header.Set("X-Connection-Only", "remove")
			c.Request.Header["X-Future-Extension"] = []string{"one", "two"}
			c.Request.Header.Set("Anthropic-Beta", "unknown-beta-must-reach-provider")
			svc := &GatewayService{cfg: &config.Config{}}
			account := newAnthropicAPIKeyAccountForTest()
			account.Credentials[credKeyHeaderOverrideEnabled] = true
			account.Credentials[credKeyHeaderOverrides] = map[string]any{"anthropic-beta": "must-not-override", "x-future-extension": "must-not-override"}
			var req *http.Request
			var err error
			if endpoint == "/messages" {
				var wire []byte
				req, wire, err = svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, []byte(strictAnthropicBody), "upstream-secret")
				require.Equal(t, strictAnthropicBody, string(wire))
			} else {
				req, err = svc.buildCountTokensRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, []byte(strictAnthropicBody), "upstream-secret")
			}
			require.NoError(t, err)
			wire, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, strictAnthropicBody, string(wire))
			require.Equal(t, c.Request.URL.RawQuery, req.URL.RawQuery)
			require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
			require.Equal(t, "/v1"+endpoint, req.URL.Path)
			require.Equal(t, "identity", req.Header.Get("Accept-Encoding"))
			require.Equal(t, []string{"one", "two"}, req.Header.Values("X-Future-Extension"))
			require.Equal(t, "unknown-beta-must-reach-provider", req.Header.Get("Anthropic-Beta"))
			require.Equal(t, "upstream-secret", getHeaderRaw(req.Header, "x-api-key"))
			require.Empty(t, req.Header.Get("Authorization"))
			require.Empty(t, req.Header.Get("Cookie"))
			require.Empty(t, req.Header.Get("X-Connection-Only"))
		})
	}
}

func TestStrictAnthropicForwardPreservesImplicitThinkingAndResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			payload := `{"type":"message","content":[{"type":"thinking","thinking":"","signature":"cc_ses_opaque"},{"type":"tool_use","name":"cc_sess_audit","input":{"text":"cc_ses_marker"}}],"stop_reason":"tool_use","usage":{"input_tokens":11,"output_tokens":7}}`
			contentType := "application/json"
			if stream {
				contentType = "text/event-stream"
				payload = ": keep this comment\r\nid: 7\r\nevent: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11}}}\r\n\r\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\r\n\r\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"cc_ses_opaque\"}}\r\n\r\ndata: {\"type\":\"content_block_stop\",\"index\":0}\r\n\r\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\r\n\r\ndata: {\"type\":\"message_stop\"}"
			}
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}, "X-Future-Response": {"preserve"}, "X-Request-Id": {"provider-first", "provider-second"}}, Body: io.NopCloser(strings.NewReader(payload))}}
			svc := newForwardPartialUsageServiceForTest(upstream)
			account := newAnthropicAPIKeyAccountForTest()
			account.Credentials["model_mapping"] = map[string]any{"claude-fable-5-1": "changed-model"}
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(strictAnthropicBody)), PlatformAnthropic)
			require.NoError(t, err)
			parsed.Stream = stream
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			result, err := svc.Forward(context.Background(), c, account, parsed)
			require.NoError(t, err)
			require.Equal(t, strictAnthropicBody, string(upstream.lastBody))
			require.Equal(t, payload, rec.Body.String())
			require.Equal(t, "preserve", rec.Header().Get("X-Future-Response"))
			require.Equal(t, []string{"provider-first", "provider-second"}, rec.Header().Values("X-Request-Id"))
			require.Equal(t, 11, result.Usage.InputTokens)
			require.Equal(t, 7, result.Usage.OutputTokens)
		})
	}
}

func TestStrictAnthropicErrorsRemainProviderErrors(t *testing.T) {
	statuses := []int{301, 307, 400, 401, 403, 404, 429, 500, 529}
	codes := make([]any, 0, len(statuses))
	for _, status := range statuses {
		codes = append(codes, float64(status))
	}
	for _, status := range statuses {
		for _, count := range []bool{false, true} {
			payload := "provider error cc_ses_marker\r\n"
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/plain"}, "Retry-After": {"17"}}, Body: io.NopCloser(strings.NewReader(payload))}}
			svc := newForwardPartialUsageServiceForTest(upstream)
			svc.rateLimitService = nil
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			account := newAnthropicAPIKeyAccountForTest()
			// Configured codes skip same-account retries and keep this table fast.
			account.Credentials["custom_error_codes_enabled"] = true
			account.Credentials["custom_error_codes"] = codes
			if count {
				err := svc.forwardCountTokensAnthropicAPIKeyPassthrough(context.Background(), c, account, []byte(strictAnthropicBody), "claude-fable-5-1")
				if status >= 400 {
					require.Error(t, err, "native error is written and still reported to the handler")
				} else {
					require.NoError(t, err)
				}
			} else {
				_, err := svc.forwardAnthropicAPIKeyPassthrough(context.Background(), c, account, []byte(strictAnthropicBody), "claude-fable-5-1", "claude-fable-5-1", false, time.Now())
				require.Error(t, err)
				var failover *UpstreamFailoverError
				if svc.shouldFailoverUpstreamError(status) {
					// geili: account-level failures move the same bytes to another account
					// first; exhaustion writes the last native error unchanged.
					require.ErrorAs(t, err, &failover)
					require.True(t, failover.StrictPassthrough)
					require.Equal(t, status, failover.StatusCode)
					require.Equal(t, payload, string(failover.ResponseBody))
					require.Empty(t, rec.Body.String())
					require.True(t, WriteStrictAnthropicFailoverGeili(c, failover, false))
				} else {
					require.NotErrorAs(t, err, &failover)
				}
			}
			require.Equal(t, status, rec.Code)
			require.Equal(t, payload, rec.Body.String())
			require.Equal(t, "17", rec.Header().Get("Retry-After"))
		}
	}
}

func TestStrictAnthropicToolRestorationOnlyTouchesMappedNameFields(t *testing.T) {
	body := []byte(`{"content":[{"type":"tool_use","name":"cc_sess_audit","input":{"text":"cc_ses_marker","name":"cc_sess_audit","number":9007199254740993}},{"type":"text","text":"cc_sess_audit"},{"type":"thinking","thinking":"cc_sess_audit","signature":"cc_sess_audit"},{"type":"redacted_thinking","data":"cc_sess_audit"}]}`)
	require.Equal(t, body, restoreToolNamesInBytes(body, nil))
	rw := &ToolNameRewrite{Reverse: map[string]string{"cc_sess_audit": "sessions_audit"}}
	out := restoreToolNamesInBytes(body, rw)
	require.Equal(t, "sessions_audit", gjson.GetBytes(out, "content.0.name").String())
	for _, path := range []string{"content.0.input", "content.1", "content.2", "content.3"} {
		require.Equal(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(out, path).Raw)
	}
	line := []byte("data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"name\":\"cc_sess_audit\",\"input\":{\"text\":\"cc_sess_audit\"}}}\r\n")
	want := strings.Replace(string(line), `"name":"cc_sess_audit"`, `"name":"sessions_audit"`, 1)
	require.Equal(t, want, string(restoreToolNamesInBytes(line, rw)))
}

func TestStrictAnthropicAuxiliaryPreservesMultipartAndBinary(t *testing.T) {
	for _, endpoint := range []string{"/models", "/models/claude-fable-5-1", "/files", "/files/file_test/content"} {
		payload := "\x00\xffcc_ses_marker\r\n"
		request := "--boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"test.txt\"\r\n\r\ncc_sess_payload\r\n--boundary--\r\n"
		upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/octet-stream"}}, Body: io.NopCloser(strings.NewReader(payload))}}
		svc := newForwardPartialUsageServiceForTest(upstream)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1"+endpoint+"?after_id=a%2Fb", strings.NewReader(request))
		c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
		err := svc.ForwardAnthropicAuxiliary(context.Background(), c, newAnthropicAPIKeyAccountForTest(), endpoint)
		require.NoError(t, err)
		require.Equal(t, request, string(upstream.lastBody))
		require.Equal(t, payload, rec.Body.String())
		require.Equal(t, "after_id=a%2Fb", upstream.lastReq.URL.RawQuery)
		require.Equal(t, "multipart/form-data; boundary=boundary", upstream.lastReq.Header.Get("Content-Type"))
	}
}

type strictConfiguredAccounts struct {
	AccountRepository
	accounts []Account
}

func (r *strictConfiguredAccounts) ListByGroup(context.Context, int64) ([]Account, error) {
	return r.accounts, nil
}
func TestStrictAnthropicAuxiliaryDoesNotSwitchNamespacesOnCooldown(t *testing.T) {
	id := int64(1)
	account := newAnthropicAPIKeyAccountForTest()
	second := *account
	second.ID++
	second.Schedulable = false
	repo := &strictConfiguredAccounts{accounts: []Account{*account, second}}
	svc := &GatewayService{accountRepo: repo}
	selected, err := svc.SingleAnthropicPassthroughAccount(context.Background(), &id)
	require.NoError(t, err)
	require.Nil(t, selected)
	repo.accounts = repo.accounts[:1]
	selected, err = svc.SingleAnthropicPassthroughAccount(context.Background(), &id)
	require.NoError(t, err)
	require.Equal(t, account.ID, selected.ID)
	repo.accounts[0].Schedulable = false
	_, err = svc.SingleAnthropicPassthroughAccount(context.Background(), &id)
	require.Error(t, err)
}

func TestStrictAnthropicHeadersReplaceCaseInsensitiveLocalValues(t *testing.T) {
	dst := http.Header{"X-Request-Id": {"local"}, "X-REQUEST-ID": {"stale"}, "X-Multi": {"local"}}
	src := http.Header{"x-request-id": {"provider"}, "x-multi": {"one", "two"}}
	copyAnthropicEndToEndHeaders(dst, src, false)
	require.Equal(t, []string{"provider"}, dst.Values("X-Request-ID"))
	require.Equal(t, []string{"one", "two"}, dst.Values("X-Multi"))
	require.Len(t, dst, 2)
}
