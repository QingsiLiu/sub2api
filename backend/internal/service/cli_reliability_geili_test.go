package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const instantQuotaFixtureGeili = `{"error":{"message":"Insufficient quota available for instant inference.","type":"upstream_error"}}`

func TestInstantQuotaGeiliHTTPAndSSERecovery(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"pool_mode": true}}
	svc := newOpenAIStreamFailedTestService(0)
	for _, status := range []int{400, 429, 502} {
		require.True(t, svc.shouldFailoverOpenAIUpstreamResponse(account, status, "", []byte(instantQuotaFixtureGeili)))
		require.True(t, shouldFailoverOpenAIPassthroughResponse(account, status, []byte(instantQuotaFixtureGeili)))
		failure := newOpenAIUpstreamFailoverError(status, nil, []byte(instantQuotaFixtureGeili), "", true)
		require.Equal(t, 429, failure.StatusCode)
		require.False(t, failure.RetryableOnSameAccount)
		require.False(t, failure.IsCredentialFailure())
		require.Equal(t, 429, failure.ClientStatusCode)
	}
	require.True(t, isOpenAIWSRateLimitError("", "upstream_error", "Insufficient quota available for instant inference."))
	for _, passthrough := range []bool{false, true} {
		for _, terminal := range []string{"error", "response.failed"} {
			t.Run(fmt.Sprintf("passthrough=%v/event=%s", passthrough, terminal), func(t *testing.T) {
				payload := instantQuotaFixtureGeili
				if terminal == "response.failed" {
					payload = `{"type":"response.failed","response":{"status":"failed",` + strings.TrimPrefix(payload, "{") + `}`
				}
				c, rec := newOpenAIStreamFailedTestContext()
				resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("event: " + terminal + "\ndata: " + payload + "\n\n"))}
				var err error
				if passthrough {
					_, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-6-astra", "gpt-6-astra")
				} else {
					_, err = svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-6-astra", "gpt-6-astra")
				}
				var failure *UpstreamFailoverError
				require.True(t, errors.As(err, &failure), "%v", err)
				require.Equal(t, 429, failure.StatusCode)
				require.False(t, failure.RetryableOnSameAccount)
				require.False(t, IsResponseCommitted(c))
				require.Empty(t, rec.Body.String(), "pre-output quota failure must not leak before switching")
			})
		}
	}
}

func TestInstantQuotaGeiliDoesNotMatchBalanceOrPrompt(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"Insufficient account balance","type":"upstream_error"}}`,
		`{"input":"Insufficient quota available for instant inference.","error":{"message":"Invalid input"}}`,
		`{"error":{"message":"Exceeded maximum number of images (50) allowed in the request.","type":"invalid_request_error"}}`,
	} {
		require.False(t, isOpenAIInstantInferenceQuotaError("", []byte(body)))
	}
}

func TestAPIKeyResponsesGeiliBareErrorAfterOutputHasFailedTerminal(t *testing.T) {
	svc := newOpenAIStreamFailedTestService(0)
	c, rec := newOpenAIStreamFailedTestContext()
	stream := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
		"event: error\ndata: " + instantQuotaFixtureGeili + "\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(stream))}
	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, time.Now(), "gpt-6-astra", "gpt-6-astra")
	require.Error(t, err)
	var failure *UpstreamFailoverError
	require.False(t, errors.As(err, &failure), "partial output must not be replayed on another account")
	frames := parseSSETestFrames(t, rec.Body.String())
	require.Len(t, frames, 2)
	require.Equal(t, "response.output_text.delta", frames[0].event)
	require.Equal(t, "response.failed", frames[1].event)
	require.Contains(t, gjson.Get(frames[1].data, "response.error.message").String(), "instant inference")
}

func TestClaude55SignatureGeiliRepairKeepsToolsAndAdaptiveThinking(t *testing.T) {
	body := []byte(`{"model":"claude-opus-5-5","thinking":{"type":"adaptive"},"output_config":{"effort":"max"},"tools":[{"name":"read","input_schema":{"type":"object"}}],"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private thought","signature":"bad"},{"type":"tool_use","id":"call1","name":"read","input":{"nonce":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call1","content":"result"}]},{"role":"assistant","content":[{"type":"redacted_thinking","data":"hidden"},{"type":"thinking","thinking":"dependent","signature":"later"},{"type":"text","text":"answer"}]}]}`)
	repaired := FilterThinkingBlocksForRetry(body, "claude-opus-5-5")
	require.NotContains(t, string(repaired), "private thought")
	require.NotContains(t, string(repaired), "dependent")
	require.NotContains(t, string(repaired), "redacted_thinking")
	require.Equal(t, "9007199254740993", gjson.GetBytes(repaired, "messages.0.content.0.input.nonce").Raw)
	require.Equal(t, "tool_use", gjson.GetBytes(repaired, "messages.0.content.0.type").String())
	require.Equal(t, gjson.GetBytes(body, "messages.1").Raw, gjson.GetBytes(repaired, "messages.1").Raw)
	for _, path := range []string{"tools", "thinking", "output_config"} {
		require.JSONEq(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(repaired, path).Raw)
	}
	require.NoError(t, validateClaude55Request(repaired, "claude-opus-5-5"))
}

func TestClaudeSignatureGeiliDefaultAndExplicitOptOut(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		want           bool
	}{
		{"missing settings", "", true},
		{"legacy partial settings", `{"enabled":true}`, true},
		{"explicit off", `{"enabled":true,"apikey_signature_enabled":false}`, false},
		{"enabled", `{"enabled":true,"apikey_signature_enabled":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &betaPolicySettingRepoStub{values: map[string]string{}}
			if tc.settings != "" {
				repo.values[SettingKeyRectifierSettings] = tc.settings
			}
			svc := &GatewayService{settingService: NewSettingService(repo, &config.Config{})}
			account := &Account{Type: AccountTypeAPIKey}
			body := []byte("{\"error\":{\"message\":\"InvokeModelWithResponseStream: ValidationException: ***.***.content.1: Invalid `signature` in `thinking` block\"}}")
			require.Equal(t, tc.want, svc.shouldRectifySignatureError(context.Background(), account, body, "claude-opus-5-5"))
			require.False(t, svc.shouldRectifySignatureError(context.Background(), account, []byte(`{"error":{"message":"AWS authentication signature does not match"}}`), "claude-opus-5-5"))
			require.False(t, svc.shouldRectifySignatureError(context.Background(), account, body, "deepseek-reasoner"))
		})
	}
}

func TestLongThinkingGeiliBudgetAndCompletion(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 180, LongThinkingStreamDataIntervalTimeout: 600}}
	require.Equal(t, 600*time.Second, longThinkingStreamInterval(cfg, "claude-opus-5-5", 180*time.Second))
	require.Equal(t, 600*time.Second, longThinkingStreamInterval(cfg, "openai/gpt-6-astra-2026-09-01", 180*time.Second))
	require.Equal(t, 180*time.Second, longThinkingStreamInterval(cfg, "gpt-6-sol", 180*time.Second))
	require.Zero(t, longThinkingStreamInterval(cfg, "claude-opus-5-5", 0))
	cfg.Gateway.LongThinkingStreamDataIntervalTimeout = 0
	require.Equal(t, 180*time.Second, longThinkingStreamInterval(cfg, "claude-opus-5-5", 180*time.Second))
	svc := newOpenAIStreamFailedTestService(1)
	svc.cfg.Gateway.LongThinkingStreamDataIntervalTimeout = 3
	c, rec := newOpenAIStreamFailedTestContext()
	pr, pw := io.Pipe()
	defer func() { require.NoError(t, pr.Close()) }()
	defer func() { require.NoError(t, pw.Close()) }()
	go func() {
		time.Sleep(1200 * time.Millisecond)
		_, _ = io.WriteString(pw, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n\n")
		_ = pw.Close()
	}()
	_, err := svc.handleStreamingResponse(c.Request.Context(), &http.Response{StatusCode: 200, Header: http.Header{}, Body: pr}, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, time.Now(), "gpt-6-astra", "gpt-6-astra")
	require.NoError(t, err)
	require.Contains(t, rec.Body.String(), "response.completed")
	require.NotContains(t, rec.Body.String(), "response.failed")
}

func TestClaude55SignatureGeiliForwardRecoversOnce(t *testing.T) {
	for _, succeeds := range []bool{true, false} {
		t.Run(fmt.Sprintf("succeeds=%v", succeeds), func(t *testing.T) {
			response := func(status int, body string) *http.Response {
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
			}
			signature := "{\"error\":{\"message\":\"Invalid `signature` in `thinking` block\"}}"
			retry := response(400, signature)
			if succeeds {
				retry = response(200, `{"id":"msg_ok","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`)
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{response(400, signature), retry}}
			cfg := &config.Config{}
			svc := &GatewayService{cfg: cfg, httpUpstream: upstream, settingService: NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{}}, cfg), rateLimitService: &RateLimitService{}, deferredService: &DeferredService{}}
			account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "synthetic-key", "base_url": "https://example.com"}}
			body := []byte(`{"model":"claude-opus-5-5","thinking":{"type":"adaptive"},"max_tokens":1024,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"old","signature":"bad"},{"type":"text","text":"answer"}]},{"role":"user","content":"continue"}]}`)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			c, rec := newOpenAIStreamFailedTestContext()
			result, err := svc.Forward(context.Background(), c, account, parsed)
			require.Len(t, upstream.bodies, 2, "signature repair must be bounded to one extra request")
			require.True(t, gjson.GetBytes(upstream.bodies[0], "messages.0.content.#(type==thinking)").Exists())
			require.False(t, gjson.GetBytes(upstream.bodies[1], "messages.0.content.#(type==thinking)").Exists())
			require.Equal(t, "adaptive", gjson.GetBytes(upstream.bodies[1], "thinking.type").String())
			if succeeds {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 200, rec.Code)
				require.NotContains(t, rec.Body.String(), "signature")
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestAPIKeyResponsesGeiliBareErrorGraceIgnoresKeepalives(t *testing.T) {
	svc := newOpenAIStreamFailedTestService(600)
	c, rec := newOpenAIStreamFailedTestContext()
	pr, pw := io.Pipe()
	defer func() { require.NoError(t, pr.Close()) }()
	defer func() { require.NoError(t, pw.Close()) }()
	go func() {
		_, _ = io.WriteString(pw, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\ndata: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"Invalid input\"}}\n\n")
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := io.WriteString(pw, ": keepalive\n\n"); err != nil {
				return
			}
		}
	}()
	started := time.Now()
	_, err := svc.handleStreamingResponse(c.Request.Context(), &http.Response{StatusCode: 200, Header: http.Header{}, Body: pr}, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, started, "gpt-6-astra", "gpt-6-astra")
	require.Error(t, err)
	require.Less(t, time.Since(started), 3*time.Second)
	require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.failed"`))
}

func TestLongThinkingGeiliConvertedAnthropicStream(t *testing.T) {
	svc := newOpenAIStreamFailedTestService(1)
	svc.cfg.Gateway.LongThinkingStreamDataIntervalTimeout = 3
	c, rec := newOpenAIStreamFailedTestContext()
	pr, pw := io.Pipe()
	defer func() { require.NoError(t, pr.Close()) }()
	defer func() { require.NoError(t, pw.Close()) }()
	go func() {
		time.Sleep(1200 * time.Millisecond)
		_, _ = io.WriteString(pw, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"model\":\"claude-opus-5-5\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":2}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		_ = pw.Close()
	}()
	result, err := svc.handleResponsesStreamingFromNativeAnthropic(&http.Response{StatusCode: 200, Header: http.Header{}, Body: pr}, c, "claude-opus-5-5", "claude-opus-5-5", "claude-opus-5-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), "response.completed")
}

func TestInstantQuotaGeiliHTTPToWSRecoversBeforeOutput(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%v", partial), func(t *testing.T) {
			cfg := newOpenAIWSV2TestConfig()
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			events := [][]byte{}
			if partial {
				events = append(events, []byte(`{"type":"response.output_text.delta","response_id":"resp_test","delta":"hi"}`))
			}
			events = append(events, []byte(`{"type":"error","error":{"type":"upstream_error","message":"Insufficient quota available for instant inference."}}`))
			conn := &openAIWSCaptureConn{events: events}
			pool := newOpenAIWSConnPool(cfg)
			defer pool.Close()
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: conn})
			svc := &OpenAIGatewayService{cfg: cfg, cache: &stubGatewayCache{}, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "synthetic-key"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
			c, rec := newOpenAIStreamFailedTestContext()
			_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-6-astra","stream":true,"input":[{"type":"input_text","text":"hi"}]}`))
			require.Error(t, err)
			var failure *UpstreamFailoverError
			if partial {
				require.False(t, errors.As(err, &failure))
				require.Contains(t, rec.Body.String(), "response.failed")
				require.NotContains(t, rec.Body.String(), `"type":"error"`)
			} else {
				require.True(t, errors.As(err, &failure), "%v", err)
				require.Equal(t, 429, failure.StatusCode)
				require.False(t, failure.RetryableOnSameAccount)
				require.Empty(t, rec.Body.String())
			}
		})
	}
	require.Equal(t, 429, openAIWSErrorHTTPStatus([]byte(instantQuotaFixtureGeili)))
}

func TestLongThinkingGeiliIdleBoundStartsAtLastRead(t *testing.T) {
	svc := newOpenAIStreamFailedTestService(1)
	svc.cfg.Gateway.LongThinkingStreamDataIntervalTimeout = 3
	c, _ := newOpenAIStreamFailedTestContext()
	pr, pw := io.Pipe()
	defer func() { require.NoError(t, pr.Close()) }()
	defer func() { require.NoError(t, pw.Close()) }()
	go func() {
		time.Sleep(800 * time.Millisecond)
		_, _ = io.WriteString(pw, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_wait\"}}\n\n")
	}()
	started := time.Now()
	_, err := svc.handleStreamingResponse(c.Request.Context(), &http.Response{StatusCode: 200, Header: http.Header{}, Body: pr}, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, started, "gpt-6-astra", "gpt-6-astra")
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.True(t, failover.SafeToFailoverAfterWrite)
	require.Contains(t, string(failover.ResponseBody), "data interval timeout")
	require.Greater(t, time.Since(started), 3*time.Second)
	require.Less(t, time.Since(started), 5*time.Second, "watchdog must not wait a second full thinking interval")
}
