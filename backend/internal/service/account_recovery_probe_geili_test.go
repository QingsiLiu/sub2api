//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountRecoveryProbeGeili_StrictCompletion(t *testing.T) {
	frames := func(parts ...string) string { return anthropicCompletionFixtureGeili(parts...) }
	cases := []struct {
		name, path, body string
		want             bool
	}{
		{"messages complete", "/v1/messages", frames(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText, geiliAnthropicClosed, geiliAnthropicReason, geiliAnthropicStop), true},
		{"messages validated EOF", "/v1/messages", frames(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText, geiliAnthropicClosed, geiliAnthropicReason), true},
		{"messages truncated", "/v1/messages", frames(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText), false},
		{"messages empty terminal", "/v1/messages", frames(geiliAnthropicStart, geiliAnthropicReason, geiliAnthropicStop), false},
		{"messages valid empty tool", "/v1/messages", anthropicToolFixtureGeili(`{}`, []string{""}, true), true},
		{"messages broken tool", "/v1/messages", anthropicToolFixtureGeili(`{}`, []string{`{"bad":`}, true), false},
		{"responses complete", "/v1/responses", frames(`{"type":"response.output_text.delta","delta":"OK"}`, `{"type":"response.completed","response":{"status":"completed","output":[]}}`), true},
		{"responses first packet", "/v1/responses", frames(`{"type":"response.output_text.delta","delta":"OK"}`), false},
		{"responses empty", "/v1/responses", frames(`{"type":"response.completed","response":{"status":"completed","output":[]}}`), false},
		{"responses failed", "/v1/responses", frames(`{"type":"response.output_text.delta","delta":"OK"}`, `{"type":"response.failed"}`), false},
		{"responses complete tool", "/v1/responses", frames(`{"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","arguments":"{}"}]}}`), true},
		{"responses bad tool", "/v1/responses", frames(`{"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","arguments":"{bad"}]}}`), false},
		{"responses streamed bad tool cannot be repaired by terminal", "/v1/responses", frames(`{"type":"response.function_call_arguments.delta","item_id":"call_1","delta":"{bad"}`, `{"type":"response.completed","response":{"status":"completed","output":[{"id":"call_1","type":"function_call","arguments":"{}"}]}}`), false},
		{"responses orphan streamed tool", "/v1/responses", frames(`{"type":"response.function_call_arguments.delta","item_id":"call_1","delta":"{}"}`, `{"type":"response.completed","response":{"status":"completed","output":[]}}`), false},
		{"responses matching streamed tool", "/v1/responses", frames(`{"type":"response.function_call_arguments.delta","item_id":"call_1","delta":"{}"}`, `{"type":"response.completed","response":{"status":"completed","output":[{"id":"call_1","type":"function_call","arguments":"{}"}]}}`), true},
		{"chat complete", "/v1/chat/completions", frames(`{"choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":null}]}`, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, "[DONE]"), true},
		{"chat first packet", "/v1/chat/completions", frames(`{"choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":null}]}`, "[DONE]"), false},
		{"chat empty terminal", "/v1/chat/completions", frames(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`), false},
		{"chat complete tool", "/v1/chat/completions", frames(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`), true},
		{"chat bad tool", "/v1/chat/completions", frames(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{bad"}}]},"finish_reason":"tool_calls"}]}`), false},
		{"chat bad legacy tool", "/v1/chat/completions", frames(`{"choices":[{"index":0,"delta":{"content":"OK","function_call":{"arguments":"{bad"}},"finish_reason":"function_call"}]}`), false},
		{"gemini complete", "/v1beta/models/gemini-test:streamGenerateContent", frames(`{"candidates":[{"index":0,"content":{"parts":[{"text":"OK"}]}}]}`, `{"candidates":[{"index":0,"finishReason":"STOP"}]}`), true},
		{"gemini first packet", "/v1beta/models/gemini-test:streamGenerateContent", frames(`{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}`), false},
		{"gemini thought only", "/v1beta/models/gemini-test:streamGenerateContent", frames(`{"candidates":[{"content":{"parts":[{"text":"thinking","thought":true}]},"finishReason":"STOP"}]}`), false},
		{"gemini truncated other candidate", "/v1beta/models/gemini-test:streamGenerateContent", frames(`{"candidates":[{"index":0,"content":{"parts":[{"text":"OK"}]},"finishReason":"STOP"},{"index":1,"content":{"parts":[{"text":"other"}]}}]}`), false},
		{"gemini invalid finish", "/v1beta/models/gemini-test:streamGenerateContent", frames(`{"candidates":[{"content":{"parts":[{"text":"OK"}]},"finishReason":"MAX_TOKENS"}]}`), false},
		{"HTTP only", "/v1/messages", "", false},
		{"unsupported media", "/v1/images/generations", `{"data":[{"url":"https://example.invalid/image"}]}`, false},
		{"long synthetic output", "/v1/responses", frames(`{"type":"response.output_text.delta","delta":"`+strings.Repeat("X", 2049)+`"}`, `{"type":"response.completed","response":{"status":"completed","output":[]}}`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, accountRecoveryProbeCompleteGeili(accountRecoveryProbeCaptureGeili{path: tc.path, status: 200, body: []byte(tc.body)}))
		})
	}
}

type accountRecoveryProbeHTTPGeili struct {
	response *http.Response
	req      *http.Request
	body     []byte
	calls    int
}

func (u *accountRecoveryProbeHTTPGeili) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	u.req = req
	u.body, _ = io.ReadAll(req.Body)
	return u.response, nil
}
func (u *accountRecoveryProbeHTTPGeili) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

type accountRecoveryProbeStateRepoGeili struct {
	AccountRepository
	writes int
}

func (r *accountRecoveryProbeStateRepoGeili) SetError(context.Context, int64, string) error {
	r.writes++
	return nil
}
func (r *accountRecoveryProbeStateRepoGeili) SetRateLimited(context.Context, int64, time.Time) error {
	r.writes++
	return nil
}
func (r *accountRecoveryProbeStateRepoGeili) ClearError(context.Context, int64) error {
	r.writes++
	return nil
}

func TestAccountRecoveryProbeGeili_SyntheticIndependentBoundedRequest(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		complete   bool
	}{
		{"complete", anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText, geiliAnthropicClosed, geiliAnthropicReason, geiliAnthropicStop), 200, true},
		{"truncated even though admin tester passes EOF", anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText), 200, false},
		{"balance", `{"error":{"message":"insufficient balance"}}`, 403, false},
		{"stream balance error stays generic", anthropicCompletionFixtureGeili(geiliAnthropicStart, `{"type":"error","error":{"type":"invalid_request_error","code":"insufficient_balance","message":"credit balance is too low"}}`), 200, false},
		{"forbidden has no state mutation", `{"error":{"message":"forbidden PRIVATE_TOKEN"}}`, 403, false},
		{"oversized response", strings.Repeat("X", accountRecoveryProbeMaxBytesGeili+1), 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &accountRecoveryProbeHTTPGeili{response: &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.body))}}
			repo := &accountRecoveryProbeStateRepoGeili{}
			svc := NewAccountTestService(repo, nil, nil, nil, nil, upstream, &config.Config{}, nil)
			account := &Account{ID: 901, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "PROBE_ONLY_TOKEN", "base_url": "https://probe.example.invalid"}, Extra: map[string]any{"original": true}}
			probeCtx, observation := WithAccountRecoveryProbeObservationGeili(context.Background())
			complete, err := svc.ProbeAccountRecoveryGeili(probeCtx, account, "claude-opus-5")
			require.Equal(t, tc.status, observation.HTTPStatus)
			require.Nil(t, observation.TimeToFirstTokenMs, "no synthetic zero or unmeasured TTFB")
			require.Equal(t, tc.complete, complete)
			if tc.complete {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "PRIVATE_TOKEN")
			}
			require.NotErrorIs(t, err, ErrAccountBalanceBlockedGeili, "supplier responses cannot create manual debt state")
			require.Zero(t, repo.writes)
			require.Equal(t, 1, upstream.calls)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(upstream.body, &payload))
			require.Equal(t, float64(64), payload["max_tokens"])
			require.Contains(t, string(upstream.body), accountRecoveryProbePromptGeili)
			require.NotContains(t, string(upstream.body), "PROBE_ONLY_TOKEN")
			require.Contains(t, string(upstream.body), "system", "Claude authentication system prompt is preserved")
			deadline, ok := upstream.req.Context().Deadline()
			require.True(t, ok)
			require.LessOrEqual(t, time.Until(deadline), 25*time.Second)
			require.Equal(t, map[string]any{"original": true}, account.Extra)
		})
	}
}

func TestAccountRecoveryProbeGeili_CodexOmitsUnsupportedBudget(t *testing.T) {
	upstream := &accountRecoveryProbeHTTPGeili{response: &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`))}}
	wrapped := &accountRecoveryProbeUpstreamGeili{HTTPUpstream: upstream}
	req, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"input":"old","thinking":{},"reasoning":{},"max_output_tokens":1024}`))
	require.NoError(t, err)
	_, err = wrapped.Do(req, "", 901, 1)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(upstream.body, &payload))
	require.NotContains(t, payload, "max_output_tokens")
	require.NotContains(t, payload, "thinking")
	require.NotContains(t, payload, "reasoning")
	require.Contains(t, string(upstream.body), accountRecoveryProbePromptGeili)
	require.True(t, accountRecoveryProbeCompleteGeili(wrapped.captures[0]))
}

func TestAccountRecoveryProbeGeili_RejectsUnsupportedRoutesBeforeNetwork(t *testing.T) {
	for _, model := range []string{"gpt-image-2", "gemini-image", "grok-video", "grok-voice", "embedding-model"} {
		upstream := &accountRecoveryProbeHTTPGeili{}
		svc := &AccountTestService{httpUpstream: upstream}
		complete, err := svc.ProbeAccountRecoveryGeili(context.Background(), &Account{ID: 901, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, model)
		require.False(t, complete)
		require.ErrorIs(t, err, errAccountRecoveryProbeUnsupportedGeili)
		require.Zero(t, upstream.calls)
	}
}

func TestAccountRecoveryProbeGeili_BedrockBudgetBeforeSigning(t *testing.T) {
	for _, auth := range []string{"apikey", "iam"} {
		for _, complete := range []bool{false, true} {
			t.Run(auth+"/complete="+strconv.FormatBool(complete), func(t *testing.T) {
				response := `{"id":"msg_probe","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`
				if !complete {
					response = `{"id":"msg_probe","type":"message","content":[{"type":"text","text":"OK"}],"stop_reason":null}`
				}
				upstream := &accountRecoveryProbeHTTPGeili{response: &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response))}}
				account := &Account{ID: 905, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Credentials: map[string]any{"auth_mode": auth, "api_key": "PROBE_ONLY_TOKEN", "aws_region": "us-east-1", "aws_access_key_id": "PROBE_AKID", "aws_secret_access_key": "PROBE_SECRET"}}
				svc := NewAccountTestService(nil, nil, nil, nil, nil, upstream, &config.Config{}, nil)
				ok, err := svc.ProbeAccountRecoveryGeili(context.Background(), account, "claude-sonnet-5-5")
				require.Equal(t, complete, ok)
				if complete {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
				require.Equal(t, 1, upstream.calls)
				require.Equal(t, int64(64), gjson.GetBytes(upstream.body, "max_tokens").Int())
				require.Equal(t, accountRecoveryProbePromptGeili, gjson.GetBytes(upstream.body, "messages.0.content.0.text").String())
				require.Contains(t, upstream.req.URL.Path, "global.anthropic.claude-sonnet-5-5")
				if auth == "iam" {
					expectedSignature := upstream.req.Header.Get("Authorization")
					require.Contains(t, expectedSignature, "AWS4-HMAC-SHA256")
					signer, signerErr := NewBedrockSignerFromAccount(account)
					require.NoError(t, signerErr)
					signedTime, timeErr := time.Parse("20060102T150405Z", upstream.req.Header.Get("X-Amz-Date"))
					require.NoError(t, timeErr)
					resigned := upstream.req.Clone(context.Background())
					resigned.Header.Del("Authorization")
					require.NoError(t, signer.signer.SignHTTP(context.Background(), signer.credentials, resigned, sha256Hash(upstream.body), "bedrock", signer.region, signedTime))
					require.Equal(t, expectedSignature, resigned.Header.Get("Authorization"), "capture must not alter the signed payload")
				}
			})
		}
	}
}

func TestAccountRecoveryProbeGeili_AntigravityRawNoRetriesOrState(t *testing.T) {
	for _, target := range []string{"gemini-3.1-pro-high", "claude-sonnet-4-5"} {
		for _, complete := range []bool{false, true} {
			t.Run(target+"/complete="+strconv.FormatBool(complete), func(t *testing.T) {
				response := anthropicCompletionFixtureGeili(`{"response":{"candidates":[{"index":0,"content":{"parts":[{"text":"OK"}]},"finishReason":"STOP"}]}}`)
				if !complete {
					response = anthropicCompletionFixtureGeili(`{"response":{"candidates":[{"index":0,"content":{"parts":[{"text":"OK"}]}}]}}`)
				}
				upstream := &accountRecoveryProbeHTTPGeili{response: &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response))}}
				repo := &accountRecoveryProbeStateRepoGeili{}
				gateway := NewAntigravityGatewayService(repo, nil, nil, nil, nil, upstream, nil, nil)
				svc := NewAccountTestService(repo, nil, nil, nil, gateway, upstream, &config.Config{}, nil)
				account := &Account{ID: 906, Platform: PlatformAntigravity, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "PROBE_ONLY_TOKEN", "project_id": "probe_project", "model_mapping": map[string]any{"public-probe": target}}}
				ok, err := svc.ProbeAccountRecoveryGeili(context.Background(), account, "public-probe")
				require.Equal(t, complete, ok)
				if complete {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
				require.Equal(t, 1, upstream.calls, "raw probe has no smart retries, URL fallback or credits overages")
				require.Zero(t, repo.writes)
				require.Equal(t, target, gjson.GetBytes(upstream.body, "model").String())
				require.Equal(t, int64(64), gjson.GetBytes(upstream.body, "request.generationConfig.maxOutputTokens").Int())
				require.Equal(t, accountRecoveryProbePromptGeili, gjson.GetBytes(upstream.body, "request.contents.0.parts.0.text").String())
				require.Equal(t, "Bearer PROBE_ONLY_TOKEN", upstream.req.Header.Get("Authorization"))
			})
		}
	}
}

func TestAccountRecoveryProbeGeili_GeminiOAuthCanonicalModelPath(t *testing.T) {
	upstream := &accountRecoveryProbeHTTPGeili{response: &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(anthropicCompletionFixtureGeili(`{"candidates":[{"content":{"parts":[{"text":"OK"}]},"finishReason":"STOP"}]}`)))}}
	svc := NewAccountTestService(nil, NewGeminiTokenProvider(nil, nil, nil), nil, nil, nil, upstream, &config.Config{}, nil)
	account := &Account{ID: 907, Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "PROBE_ONLY_TOKEN", "base_url": "https://probe.example.invalid", "expires_at": time.Now().Add(time.Hour).Unix(), "model_mapping": map[string]any{"public-probe": "projects/p/locations/global/publishers/google/models/gemini-2.5-flash"}}}
	complete, err := svc.ProbeAccountRecoveryGeili(context.Background(), account, "public-probe")
	require.NoError(t, err)
	require.True(t, complete)
	require.Equal(t, "/v1beta/models/gemini-2.5-flash:streamGenerateContent", upstream.req.URL.Path)
	require.Equal(t, int64(64), gjson.GetBytes(upstream.body, "generationConfig.maxOutputTokens").Int())
	require.Equal(t, accountRecoveryProbePromptGeili, gjson.GetBytes(upstream.body, "contents.0.parts.0.text").String())
}

func TestAccountRecoveryProbeGeili_ModelMismatchCannotGrant(t *testing.T) {
	upstream := &accountRecoveryProbeHTTPGeili{response: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText, geiliAnthropicClosed, geiliAnthropicReason, geiliAnthropicStop)))}}
	account := &Account{ID: 909, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "PROBE_ONLY_TOKEN", "model_mapping": map[string]any{"public-probe": "claude-sonnet-5-5"}}}
	svc := NewAccountTestService(nil, nil, nil, nil, nil, upstream, &config.Config{}, nil)
	complete, err := svc.ProbeAccountRecoveryGeili(context.Background(), account, "public-probe")
	require.False(t, complete)
	require.Error(t, err)
	require.Equal(t, "public-probe", gjson.GetBytes(upstream.body, "model").String(), "legacy OAuth tester does not map this alias")
}

func TestAccountRecoveryProbeGeili_NormalBedrockTestBudgetUnchanged(t *testing.T) {
	upstream := &accountRecoveryProbeHTTPGeili{response: &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"type":"message","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`))}}
	account := &Account{ID: 910, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Credentials: map[string]any{"auth_mode": "apikey", "api_key": "PROBE_ONLY_TOKEN", "aws_region": "us-east-1"}}
	repo := &accountRecoveryProbeRepositoryGeili{account: account}
	svc := NewAccountTestService(repo, nil, nil, nil, nil, upstream, &config.Config{}, nil)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/admin/test", nil)
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "claude-sonnet-5-5", "", AccountTestModeDefault))
	require.Equal(t, int64(256), gjson.GetBytes(upstream.body, "max_tokens").Int())
	require.Equal(t, "hi", gjson.GetBytes(upstream.body, "messages.0.content.0.text").String())
}
