package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestEncryptedRecoveryPolicyPreservesReadableContextGeili(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","reasoning":{"effort":"max"},"nonce":9007199254740993,"input":[{"type":"reasoning","id":"rs_old","encrypted_content":"cipher","summary":[{"type":"summary_text","text":"kept"}],"unknown":{"preserve":true}},{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"result"},{"type":"message","role":"user","content":"continue"}]}`)
	copy := bytes.Clone(body)
	retry, changed, reason := PrepareOpenAIEncryptedReasoningRecoveryGeili(body)
	require.True(t, changed, reason)
	require.Equal(t, copy, body, "canonical client body must remain immutable")
	require.False(t, gjson.GetBytes(retry, "input.0.encrypted_content").Exists())
	require.False(t, gjson.GetBytes(retry, "input.0.id").Exists())
	require.Equal(t, "kept", gjson.GetBytes(retry, "input.0.summary.0.text").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(retry, "nonce").Raw)
	require.Equal(t, gjson.GetBytes(body, "reasoning").Raw, gjson.GetBytes(retry, "reasoning").Raw)
	for i := 1; i < 4; i++ {
		require.Equal(t, gjson.GetBytes(body, "input").Array()[i].Raw, gjson.GetBytes(retry, "input").Array()[i].Raw)
	}
	_, again, _ := PrepareOpenAIEncryptedReasoningRecoveryGeili(retry)
	require.False(t, again)
}

func TestEncryptedRecoveryPolicyRejectsOpaqueStateGeili(t *testing.T) {
	for _, body := range []string{
		`{"previous_response_id":"resp_old","input":[{"type":"reasoning","encrypted_content":"cipher"}]}`,
		`{"input":[{"type":"reasoning","encrypted_content":"cipher"},{"type":"compaction","encrypted_content":"history"}]}`,
		`{"input":[{"type":"reasoning","encrypted_content":"cipher"},{"type":"compaction_trigger"}]}`,
		`{"input":[{"type":"reasoning","encrypted_content":"cipher"},{"type":"function_call_output","output":[{"type":"encrypted_content","encrypted_content":"tool"}]}]}`,
		`{"input":[{"type":"reasoning","encrypted_content":"cipher"},{"type":"agent_message","content":[{"type":"encrypted_content","content":"tool"}]}]}`,
		`{"input":[{"type":"reasoning","encrypted_content":"cipher","content":[{"encrypted_output":"tool"}]}]}`,
		`{"input":[{"type":"future_history","encrypted_content":"history"},{"type":"reasoning","encrypted_content":"cipher"}]}`,
		`{"input":[{"type":"reasoning","encrypted_content":"cipher","future":{"encrypted_content":"nested"}}]}`,
		`{"input":[{"type":"message","role":"user","content":"continue"}]}`,
		`{"tools":[{"type":"web_search"}],"input":[{"type":"reasoning","encrypted_content":"cipher"}]}`,
		`{"input":[{"type":"reasoning","encrypted_content":"cipher"},"rs_server_history"]}`,
		`invalid-json`,
	} {
		t.Run(body, func(t *testing.T) {
			retry, changed, _ := PrepareOpenAIEncryptedReasoningRecoveryGeili([]byte(body))
			require.False(t, changed)
			require.Equal(t, body, string(retry))
		})
	}
}

func TestEncryptedRecoveryPolicyHandlesWSAndObjectInputGeili(t *testing.T) {
	for _, body := range []string{
		`{"input":{"type":"reasoning","id":"rs_test","encrypted_content":"cipher","summary":[]}}`,
		`{"type":"response.create","response":{"model":"gpt-6-astra","input":[{"type":"reasoning","id":"rs_test","encrypted_content":"cipher","summary":[]}]}}`,
	} {
		retry, changed, _ := PrepareOpenAIEncryptedReasoningRecoveryGeili([]byte(body))
		require.True(t, changed)
		require.NotContains(t, string(retry), "cipher")
		require.NotContains(t, string(retry), "rs_test")
		require.Contains(t, string(retry), "summary")
	}
}

func TestEncryptedFailureClassificationGeili(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"invalid_encrypted_content","message":"rejected"}}`,
		`{"type":"response.failed","response":{"error":{"code":"invalid_encrypted_content","message":"rejected"}}}`,
		`{"error":{"code":"upstream_error","message":"Encrypted function output content could not be decrypted or decoded."}}`,
		`{"error":{"code":"invalid_request_error","message":"The encrypted content could not be verified. Encrypted content could not be decrypted or parsed."}}`,
	} {
		require.True(t, ClassifyOpenAIEncryptedFailureGeili([]byte(body)), body)
	}
	for _, body := range []string{
		`{"error":{"code":"context_length_exceeded","message":"The encrypted content could not be verified. Encrypted content could not be decrypted or parsed."}}`,
		`{"error":{"message":"encrypted content unsupported"}}`,
		`{"error":{"message":"Our servers are overloaded"}}`,
		`{"output":[{"type":"message","content":"invalid_encrypted_content"}]}`,
	} {
		require.False(t, ClassifyOpenAIEncryptedFailureGeili([]byte(body)), body)
	}
}

func TestEncryptedStreamFailurePolicyGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, tc := range []struct {
			name                           string
			protected, enabled, sideEffect bool
			tokens                         int
			retryable                      bool
		}{
			{name: "protected enabled", protected: true, enabled: true, retryable: true},
			{name: "nonzero usage", protected: true, enabled: true, tokens: 7},
			{name: "recovery disabled", protected: true},
			{name: "operation observed", protected: true, enabled: true, sideEffect: true},
			{name: "nonprotect"},
		} {
			t.Run(fmt.Sprintf("passthrough=%v/%s", passthrough, tc.name), func(t *testing.T) {
				svc, account := encryptedRecoveryFixtureGeili(nil)
				c, rec := encryptedRecoveryContextGeili("policy-test")
				ctx := WithRequestRecovery(context.Background(), time.Second)
				ctx = context.WithValue(ctx, openAIEncryptedRouteContextKeyGeili{}, &openAIEncryptedRouteStateGeili{protected: tc.protected, recovery: tc.enabled})
				c.Request = c.Request.WithContext(ctx)
				if tc.sideEffect {
					forbidRequestReplayGeili(ctx)
				}
				failure := fmt.Sprintf(`{"type":"response.failed","response":{"status":"failed","error":{"code":"invalid_encrypted_content","message":"bad cipher"},"usage":{"input_tokens":%d,"output_tokens":0}}}`, tc.tokens)
				resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: " + failure + "\n\n"))}
				var err error
				var usage *OpenAIUsage
				if passthrough {
					result, failureErr := svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "gpt-6-astra", "gpt-6-astra")
					err = failureErr
					if result != nil {
						usage = result.usage
					}
				} else {
					result, failureErr := svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "gpt-6-astra", "gpt-6-astra")
					err = failureErr
					if result != nil {
						usage = result.usage
					}
				}
				require.Error(t, err)
				if tc.protected {
					var typed *OpenAIEncryptedRecoveryErrorGeili
					require.ErrorAs(t, err, &typed)
					require.Equal(t, tc.retryable, typed.Retryable)
					require.Empty(t, rec.Body.String(), "failed attempts cannot leak their identity into a recovered response")
					require.NotNil(t, usage)
					require.Equal(t, tc.tokens, usage.InputTokens)
				} else {
					var failover *UpstreamFailoverError
					require.ErrorAs(t, err, &failover, "ordinary users retain original error handling")
				}
			})
		}
	}
}

func TestEncryptedSynchronousSSEFailureRetainsMeteringGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%v", passthrough), func(t *testing.T) {
			svc, account := encryptedRecoveryFixtureGeili(nil)
			c, _ := encryptedRecoveryContextGeili("sync-policy-test")
			ctx := context.WithValue(context.Background(), openAIEncryptedRouteContextKeyGeili{}, &openAIEncryptedRouteStateGeili{protected: true, recovery: true})
			c.Request = c.Request.WithContext(ctx)
			body := []byte("data: " + `{"type":"response.failed","response":{"error":{"code":"invalid_encrypted_content"},"usage":{"input_tokens":7,"output_tokens":0}}}` + "\n\n")
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}
			var err error
			var usage *OpenAIUsage
			if passthrough {
				result, failureErr := svc.handlePassthroughSSEToJSON(resp, c, account, body, "model", "model")
				err = failureErr
				if result != nil {
					usage = result.usage
				}
			} else {
				result, failureErr := svc.handleSSEToJSON(resp, c, account, body, "model", "model")
				err = failureErr
				if result != nil {
					usage = result.usage
				}
			}
			var typed *OpenAIEncryptedRecoveryErrorGeili
			require.ErrorAs(t, err, &typed)
			require.False(t, typed.Retryable)
			require.NotNil(t, usage)
			require.Equal(t, 7, usage.InputTokens)
		})
	}
}

func TestEncryptedPassthroughHTTPRecoveryGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, input string
		status      int
		recover     bool
	}{
		{name: "wrapped502", status: 502, input: `[{"type":"reasoning","id":"rs_1","encrypted_content":"old-cipher","summary":[{"type":"summary_text","text":"progress"}]}]`, recover: true},
		{name: "wrapped400", status: 400, input: `[{"type":"reasoning","id":"rs_1","encrypted_content":"old-cipher","summary":[]}]`, recover: true},
		{name: "opaque tool", status: 502, input: `[{"type":"function_call_output","call_id":"call_1","output":[{"type":"encrypted_content","encrypted_content":"opaque"}]}]`},
		{name: "compaction", status: 502, input: `[{"type":"compaction","encrypted_content":"opaque"},{"type":"reasoning","encrypted_content":"old-cipher"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responses := []*http.Response{encryptedRecoveryResponseGeili(tc.status, `{"error":{"code":"invalid_encrypted_content","message":"bad cipher"}}`)}
			if tc.recover {
				responses = append(responses, encryptedRecoveryResponseGeili(200, `{"id":"resp_success","object":"response","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2}}`))
			}
			upstream := &httpUpstreamRecorder{responses: responses}
			svc, account := encryptedRecoveryFixtureGeili(upstream)
			c, rec := encryptedRecoveryContextGeili("passthrough-recover")
			ctx := WithRequestRecovery(context.Background(), time.Second)
			ctx = context.WithValue(ctx, openAIEncryptedRouteContextKeyGeili{}, &openAIEncryptedRouteStateGeili{protected: true, recovery: true})
			c.Request = c.Request.WithContext(ctx)
			body := []byte(`{"model":"gpt-6-astra","stream":false,"nonce":9007199254740993,"input":` + tc.input + `}`)
			result, err := svc.forwardOpenAIPassthrough(ctx, c, account, body, body, "gpt-6-astra", false, nil, false, time.Now())
			if tc.recover {
				require.NoError(t, err)
				require.Len(t, upstream.bodies, 2)
				require.Equal(t, "9007199254740993", gjson.GetBytes(upstream.bodies[1], "nonce").Raw)
				require.NotContains(t, string(upstream.bodies[1]), "old-cipher")
				require.NotContains(t, string(upstream.bodies[1]), "rs_1")
				require.NotNil(t, result)
				require.Equal(t, 3, result.Usage.InputTokens)
				require.Equal(t, 200, rec.Code)
			} else {
				require.Error(t, err)
				require.Len(t, upstream.bodies, 1)
				require.Equal(t, tc.input, gjson.GetBytes(upstream.bodies[0], "input").Raw)
				require.Equal(t, 400, rec.Code)
				require.Equal(t, "invalid_encrypted_content", gjson.Get(rec.Body.String(), "error.code").String())
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover))
			}
		})
	}
}

func TestEncryptedFailureRenderingAfterHeartbeatGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := encryptedRecoveryContextGeili("heartbeat")
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	_, err := c.Writer.WriteString(": keepalive\n\n")
	require.NoError(t, err)
	c.Writer.Flush()
	err = RenderOpenAIEncryptedFailureGeili(c, []byte(`{"error":{"code":"invalid_encrypted_content"}}`))
	require.Error(t, err)
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), "event: response.failed")
	require.Contains(t, rec.Body.String(), `"code":"invalid_encrypted_content"`)
	require.NotContains(t, rec.Body.String(), "response.completed")
}

func TestEncryptedPassthroughRecoveryIsRequestWideOnceGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	failure := `{"error":{"code":"invalid_encrypted_content","message":"bad cipher"}}`
	upstream := &httpUpstreamRecorder{responses: []*http.Response{encryptedRecoveryResponseGeili(502, failure), encryptedRecoveryResponseGeili(502, failure)}}
	svc, account := encryptedRecoveryFixtureGeili(upstream)
	c, rec := encryptedRecoveryContextGeili("once")
	ctx := WithRequestRecovery(context.Background(), time.Second)
	ctx = context.WithValue(ctx, openAIEncryptedRouteContextKeyGeili{}, &openAIEncryptedRouteStateGeili{protected: true, recovery: true})
	c.Request = c.Request.WithContext(ctx)
	body := []byte(`{"model":"gpt-6-astra","input":[{"type":"reasoning","id":"rs_test","encrypted_content":"cipher","summary":[]}]}`)
	_, err := svc.forwardOpenAIPassthrough(ctx, c, account, body, body, "gpt-6-astra", false, nil, false, time.Now())
	require.Error(t, err)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, 400, rec.Code)
	require.Equal(t, "invalid_encrypted_content", gjson.Get(rec.Body.String(), "error.code").String())
	require.False(t, ClaimOpenAIEncryptedRecoveryGeili(ctx))
}

func TestEncryptedForwardRecoveryProtocolsGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name                string
		stream, sse, opaque bool
		tokens              int
		recover             bool
	}{
		{name: "HTTP502 reasoning", recover: true},
		{name: "SSE streaming reasoning", stream: true, sse: true, recover: true},
		{name: "SSE buffered reasoning", sse: true, recover: true},
		{name: "SSE streaming metered", stream: true, sse: true, tokens: 7},
		{name: "SSE buffered metered", sse: true, tokens: 7},
		{name: "HTTP502 opaque", opaque: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failureBody := `{"error":{"code":"invalid_encrypted_content","message":"bad cipher"}}`
			first := encryptedRecoveryResponseGeili(502, failureBody)
			if tc.sse {
				failureBody = fmt.Sprintf(`{"type":"response.failed","response":{"error":{"code":"invalid_encrypted_content","message":"bad cipher"},"usage":{"input_tokens":%d,"output_tokens":0}}}`, tc.tokens)
				first = encryptedRecoveryResponseGeili(200, "data: "+failureBody+"\n\n")
				first.Header.Set("Content-Type", "text/event-stream")
			}
			responses := []*http.Response{first}
			if tc.recover {
				success := `{"id":"resp_ok","object":"response","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2}}`
				next := encryptedRecoveryResponseGeili(200, success)
				if tc.stream {
					next = encryptedRecoveryResponseGeili(200, "data: "+`{"type":"response.completed","response":`+success+`}`+"\n\n")
					next.Header.Set("Content-Type", "text/event-stream")
				}
				responses = append(responses, next)
			}
			upstream := &httpUpstreamRecorder{responses: responses}
			svc, account := encryptedRecoveryFixtureGeili(upstream)
			c, rec := encryptedRecoveryContextGeili("forward-recover")
			ctx := WithRequestRecovery(context.Background(), time.Second)
			ctx = context.WithValue(ctx, openAIEncryptedRouteContextKeyGeili{}, &openAIEncryptedRouteStateGeili{protected: true, recovery: true})
			c.Request = c.Request.WithContext(ctx)
			input := `[{"type":"reasoning","id":"rs_test","encrypted_content":"cipher","summary":[]}]`
			if tc.opaque {
				input = `[{"type":"compaction","encrypted_content":"opaque"},{"type":"reasoning","encrypted_content":"cipher"}]`
			}
			body := []byte(fmt.Sprintf(`{"model":"gpt-6-astra","stream":%v,"nonce":9007199254740993,"input":%s}`, tc.stream, input))
			result, err := svc.Forward(ctx, c, account, body)
			if tc.recover {
				require.NoError(t, err)
				require.Len(t, upstream.bodies, 2)
				require.NotContains(t, string(upstream.bodies[1]), `"encrypted_content"`)
				require.Equal(t, "9007199254740993", gjson.GetBytes(upstream.bodies[1], "nonce").Raw)
				require.NotNil(t, result)
				require.Equal(t, 3, result.Usage.InputTokens)
			} else {
				require.Error(t, err)
				require.Len(t, upstream.bodies, 1)
				require.Equal(t, 400, rec.Code)
				require.Equal(t, "invalid_encrypted_content", gjson.Get(rec.Body.String(), "error.code").String())
				if tc.tokens > 0 {
					require.NotNil(t, result)
					require.Equal(t, tc.tokens, result.Usage.InputTokens)
				}
				if tc.opaque {
					require.Contains(t, string(upstream.bodies[0]), `"encrypted_content":"opaque"`)
				}
			}
		})
	}
}

func TestEncryptedStreamSourceOnlyAfterDeliveryGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, tc := range []struct {
			name, event string
			hold        bool
			writes      int
		}{
			{name: "done then failure", event: "response.output_item.done", writes: 1},
			{name: "added never authority", event: "response.output_item.added"},
			{name: "done held by compaction guard", event: "response.output_item.done", hold: true},
		} {
			t.Run(fmt.Sprintf("passthrough=%v/%s", passthrough, tc.name), func(t *testing.T) {
				svc, account := encryptedRecoveryFixtureGeili(nil)
				cache := &encryptedRouteCacheFixtureGeili{}
				svc.cache = cache
				ctx, _, err := svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), &APIKey{ID: 10, UserID: 3885}, []byte(`{"input":"hello"}`))
				require.NoError(t, err)
				group := int64(131)
				ctx = WithOpenAIEncryptedOutputOriginGeili(ctx, &group, account, "gpt-6-astra")
				c, rec := encryptedRecoveryContextGeili("source-delivery")
				c.Request = c.Request.WithContext(ctx)
				if tc.hold {
					c.Set(openAINativeCompactionV2Key, true)
				}
				finish := BeginTextForwardGuard(c, true)
				done := fmt.Sprintf(`{"type":%q,"output_index":0,"item":{"type":"reasoning","encrypted_content":"new-output-cipher","summary":[]}}`, tc.event)
				failure := `{"type":"response.failed","response":{"status":"failed","error":{"code":"invalid_encrypted_content","message":"bad cipher"},"usage":{"input_tokens":3,"output_tokens":2}}}`
				resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: " + done + "\n\ndata: " + failure + "\n\n"))}
				if passthrough {
					_, err = svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "model", "model")
				} else {
					_, err = svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "model", "model")
				}
				err = finish(err)
				require.Error(t, err)
				require.Equal(t, tc.writes, cache.writes)
				if tc.writes > 0 {
					require.Contains(t, rec.Body.String(), "new-output-cipher")
					require.Equal(t, account.ID, cache.routes[3885][openAIEncryptedContentDigest("new-output-cipher")].AccountID)
				}
				if tc.hold {
					require.NotContains(t, rec.Body.String(), "new-output-cipher")
				}
			})
		}
	}
}

func TestEncryptedFailureRenderingAfterGuardedHeartbeatGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := encryptedRecoveryContextGeili("guarded-heartbeat")
	finish := BeginTextForwardGuard(c, true)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	_, err := c.Writer.WriteString(": keepalive\n\n")
	require.NoError(t, err)
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), ": keepalive")
	err = RenderOpenAIEncryptedFailureGeili(c, []byte(`{"error":{"code":"invalid_encrypted_content"}}`))
	err = finish(err)
	require.Error(t, err)
	require.Contains(t, rec.Body.String(), "event: response.failed")
	require.Contains(t, rec.Body.String(), `"code":"invalid_encrypted_content"`)
	require.NotContains(t, rec.Body.String(), "response.completed")
	require.NotContains(t, rec.Body.String(), "\n\n{\"error\"")
}

func TestEncryptedBareErrorWaitsForFailedMeteringGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, paired := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%v/paired=%v", passthrough, paired), func(t *testing.T) {
				stream := "data: " + `{"type":"error","error":{"code":"invalid_encrypted_content","message":"bad cipher"}}` + "\n\n"
				if paired {
					stream += "data: " + `{"type":"response.failed","response":{"error":{"code":"invalid_encrypted_content","message":"bad cipher"},"usage":{"input_tokens":7,"output_tokens":0}}}` + "\n\n"
				}
				first := encryptedRecoveryResponseGeili(200, stream)
				first.Header.Set("Content-Type", "text/event-stream")
				upstream := &httpUpstreamRecorder{responses: []*http.Response{first}}
				svc, account := encryptedRecoveryFixtureGeili(upstream)
				if passthrough {
					account.Extra["openai_passthrough"] = true
				}
				c, rec := encryptedRecoveryContextGeili("paired-metering")
				ctx := WithRequestRecovery(context.Background(), time.Second)
				ctx = context.WithValue(ctx, openAIEncryptedRouteContextKeyGeili{}, &openAIEncryptedRouteStateGeili{protected: true, recovery: true})
				c.Request = c.Request.WithContext(ctx)
				result, err := svc.Forward(ctx, c, account, []byte(`{"model":"gpt-6-astra","stream":true,"input":[{"type":"reasoning","id":"rs_test","encrypted_content":"cipher","summary":[]}]}`))
				require.Error(t, err)
				require.Len(t, upstream.bodies, 1, "bare prelude cannot authorize a second paid request")
				require.Equal(t, 400, rec.Code)
				require.Equal(t, "invalid_encrypted_content", gjson.Get(rec.Body.String(), "error.code").String())
				if paired {
					require.NotNil(t, result)
					require.Equal(t, 7, result.Usage.InputTokens)
				}
			})
		}
	}
}

func TestEncryptedOAuthHTTPPreservesPreviousResponseGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{encryptedRecoveryResponseGeili(502, `{"error":{"code":"invalid_encrypted_content","message":"bad cipher"}}`)}}
	svc, account := encryptedRecoveryFixtureGeili(upstream)
	account.Type = AccountTypeOAuth
	account.Credentials = map[string]any{"access_token": "test-oauth-token", "chatgpt_account_id": "synthetic-account"}
	account.Extra = map[string]any{"openai_passthrough": false, "openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff}
	cache := &encryptedRouteCacheFixtureGeili{}
	svc.cache = cache
	key := &APIKey{ID: 10, UserID: 3885}
	group := int64(131)
	ctx, _, err := svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), key, []byte(`{"input":"hello"}`))
	require.NoError(t, err)
	ctx = WithOpenAIEncryptedOutputOriginGeili(ctx, &group, account, "gpt-6-astra")
	ObserveOpenAIEncryptedOutputGeili(ctx, []byte(`{"type":"response.output_item.done","item":{"type":"reasoning","encrypted_content":"original-cipher","summary":[]}}`))
	body := []byte(`{"model":"gpt-6-astra","previous_response_id":"resp_original","nonce":9007199254740993,"input":[{"type":"reasoning","encrypted_content":"original-cipher","summary":[]},{"type":"message","role":"user","content":"continue"}]}`)
	ctx, owner, err := svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), key, body)
	require.NoError(t, err)
	require.NotNil(t, owner)
	c, rec := encryptedRecoveryContextGeili("oauth-preserve-prev")
	c.Request = c.Request.WithContext(ctx)
	_, err = svc.Forward(ctx, c, account, body)
	require.Error(t, err)
	require.Len(t, upstream.bodies, 1, "server-history dependencies cannot be repaired by deleting them")
	require.Equal(t, "resp_original", gjson.GetBytes(upstream.bodies[0], "previous_response_id").String())
	require.Equal(t, "original-cipher", gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(upstream.bodies[0], "nonce").Raw)
	require.Equal(t, 400, rec.Code)
	require.Equal(t, "invalid_encrypted_content", gjson.Get(rec.Body.String(), "error.code").String())
}

func TestEncryptedProtectedCompactCannotSwitchModelsGeili(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAICompactModel: "compact-alternate"}}}
	c, _ := encryptedRecoveryContextGeili("compact-model-protection")
	c.Request.URL.Path = "/v1/responses/compact"
	ctx := context.WithValue(context.Background(), openAIEncryptedRouteContextKeyGeili{}, &openAIEncryptedRouteStateGeili{protected: true, recovery: true})
	c.Request = c.Request.WithContext(ctx)
	body := []byte(`{"model":"gpt-6-astra","input":[{"type":"compaction","encrypted_content":"opaque"}]}`)
	next, model, retry := svc.prepareOpenAICompactFallbackRetry(c, nil, "gpt-6-astra", body, 400, "", []byte(`{"response":{"status":"failed","error":null}}`), false)
	require.False(t, retry)
	require.Empty(t, model)
	require.Equal(t, body, next)
}

func TestEncryptedCompactFallbackRecordsActualModelGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%v", passthrough), func(t *testing.T) {
			initial := encryptedRecoveryResponseGeili(400, `{"error":{"code":"model_not_found","message":"model not found"}}`)
			final := encryptedRecoveryResponseGeili(200, `{"id":"compact_done","object":"response.compaction","status":"completed","output":[{"type":"compaction","encrypted_content":"fresh-compact-cipher"}],"usage":{"input_tokens":3,"output_tokens":2}}`)
			upstream := &httpUpstreamRecorder{responses: []*http.Response{initial, final}}
			svc, account := encryptedRecoveryFixtureGeili(upstream)
			svc.cfg.Gateway.OpenAICompactModel = "compact-alternate"
			account.Extra["openai_passthrough"] = passthrough
			cache := &encryptedRouteCacheFixtureGeili{}
			svc.cache = cache
			body := []byte(`{"model":"gpt-6-astra","input":[{"type":"message","role":"user","content":"hello"},{"type":"compaction_trigger"}]}`)
			ctx, _, err := svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), &APIKey{ID: 10, UserID: 3885}, body)
			require.NoError(t, err)
			group := int64(131)
			ctx = WithOpenAIEncryptedOutputOriginGeili(ctx, &group, account, "gpt-6-astra")
			c, _ := encryptedRecoveryContextGeili("fallback-origin")
			c.Request = c.Request.WithContext(ctx)
			_, err = svc.Forward(ctx, c, account, body)
			require.NoError(t, err)
			require.Len(t, upstream.bodies, 2)
			require.Equal(t, "compact-alternate", gjson.GetBytes(upstream.bodies[1], "model").String())
			require.Equal(t, "compact-alternate", cache.routes[3885][openAIEncryptedContentDigest("fresh-compact-cipher")].Model)
		})
	}
}

func TestEncryptedActualOriginDoesNotRemapModelChainGeili(t *testing.T) {
	svc, account := encryptedRecoveryFixtureGeili(nil)
	account.Credentials["model_mapping"] = map[string]any{"client-model": "actual-model", "actual-model": "wrong-double-map"}
	cache := &encryptedRouteCacheFixtureGeili{}
	svc.cache = cache
	ctx, _, err := svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), &APIKey{ID: 10, UserID: 3885}, []byte(`{"input":"hello"}`))
	require.NoError(t, err)
	group := int64(131)
	ctx = WithOpenAIEncryptedActualOutputOriginGeili(ctx, &group, account, "actual-model")
	ObserveOpenAIEncryptedOutputGeili(ctx, []byte(`{"type":"response.output_item.done","item":{"type":"reasoning","encrypted_content":"model-chain-cipher","summary":[]}}`))
	require.Equal(t, "actual-model", cache.routes[3885][openAIEncryptedContentDigest("model-chain-cipher")].Model)
}

func TestEncryptedProtectedCompactPreservesInitialModelGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%v", passthrough), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{responses: []*http.Response{encryptedRecoveryResponseGeili(502, `{"error":{"code":"invalid_encrypted_content","message":"bad cipher"}}`)}}
			svc, account := encryptedRecoveryFixtureGeili(upstream)
			svc.cfg.Gateway.OpenAICompactModel = "compact-alternate"
			account.Extra["openai_passthrough"] = passthrough
			c, rec := encryptedRecoveryContextGeili("initial-compact-model")
			c.Request.URL.Path = "/v1/responses/compact"
			ctx := context.WithValue(context.Background(), openAIEncryptedRouteContextKeyGeili{}, &openAIEncryptedRouteStateGeili{protected: true})
			c.Request = c.Request.WithContext(ctx)
			_, err := svc.Forward(ctx, c, account, []byte(`{"model":"gpt-6-astra","input":[{"type":"reasoning","encrypted_content":"old-cipher","summary":[]}]}`))
			require.Error(t, err)
			require.Len(t, upstream.bodies, 1)
			require.Equal(t, "gpt-6-astra", gjson.GetBytes(upstream.bodies[0], "model").String())
			require.Equal(t, 400, rec.Code)
		})
	}
}
