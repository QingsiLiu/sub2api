//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
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

func TestGeminiOpenAIRequestNativeParameters(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		chat       bool
		wantBudget int
		stop       string
	}{
		{"responses budget one", `{"model":"gemini-alias","input":"hello","max_output_tokens":1}`, false, 1, ""},
		{"responses default", `{"model":"gemini-alias","input":"hello"}`, false, 8192, ""},
		{"chat precedence", `{"model":"gemini-alias","messages":[{"role":"user","content":"hello"}],"max_tokens":200,"max_completion_tokens":1,"stop":"END"}`, true, 1, "END"},
		{"chat stop array", `{"model":"gemini-alias","messages":[{"role":"user","content":"hello"}],"stop":["STOP"]}`, true, 8192, "STOP"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := parseGeminiOpenAIRequestGeili([]byte(tc.body), tc.chat, "gemini-3.1-pro")
			require.NoError(t, err)
			require.Equal(t, int64(tc.wantBudget), gjson.GetBytes(r.body, "generationConfig.maxOutputTokens").Int())
			require.Equal(t, tc.stop, gjson.GetBytes(r.body, "generationConfig.stopSequences.0").String())
			require.False(t, gjson.GetBytes(r.body, "messages").Exists())
			require.False(t, gjson.GetBytes(r.body, "anthropic_version").Exists())
		})
	}
	r, err := parseGeminiOpenAIRequestGeili([]byte(`{"model":"alias","input":"hello","reasoning":{"effort":"high"},"text":{"format":{"type":"json_schema","name":"answer","strict":true,"schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}}}`), false, "gemini-3.1-pro")
	require.NoError(t, err)
	require.Equal(t, "HIGH", gjson.GetBytes(r.body, "generationConfig.thinkingConfig.thinkingLevel").String())
	require.Equal(t, "application/json", gjson.GetBytes(r.body, "generationConfig.responseMimeType").String())
	require.True(t, gjson.GetBytes(r.body, "generationConfig.responseJsonSchema").Exists())
	r, err = parseGeminiOpenAIRequestGeili([]byte(`{"model":"alias","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`), true, "gemini-3.1-pro")
	require.NoError(t, err)
	require.Equal(t, "application/json", gjson.GetBytes(r.body, "generationConfig.responseMimeType").String())
}
func TestGeminiOpenAIRequestRejectsUnsupportedBeforeForward(t *testing.T) {
	for _, fragment := range []string{`"max_output_tokens":0`, `"max_output_tokens":-1`, `"max_output_tokens":1.5`, `"store":true`, `"previous_response_id":"resp_old"`, `"include":["reasoning.encrypted_content"]`, `"background":true`, `"parallel_tool_calls":false`, `"tools":[{"type":"web_search"}]`, `"text":{"format":{"type":"json_schema","schema":{"type":"object","unevaluatedProperties":false}}}`} {
		body := `{"model":"gemini-test","input":"hi",` + fragment + `}`
		_, err := parseGeminiOpenAIRequestGeili([]byte(body), false, "gemini-3.1-pro")
		require.Error(t, err, body)
	}
	for _, budget := range []string{"0", "-1"} {
		body := []byte(`{"model":"gemini-test","messages":[{"role":"user","content":"hi"}],"max_tokens":` + budget + `}`)
		_, err := parseGeminiOpenAIRequestGeili(body, true, "gemini-3.1-pro")
		require.Error(t, err)
	}
	_, err := parseGeminiOpenAIRequestGeili([]byte(`{"model":"alias","input":"hi","reasoning":{"effort":"high"}}`), false, "argon")
	require.Error(t, err)
}
func TestGeminiOpenAIRequestConversationToolsImagesAndSignature(t *testing.T) {
	body := []byte(`{"model":"gemini-test","instructions":"system","tools":[{"type":"function","name":"read","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}],"tool_choice":{"type":"function","name":"read"},"input":[{"role":"developer","content":"rules"},{"role":"user","content":[{"type":"input_text","text":"look"},{"type":"input_image","image_url":"data:image/png;base64,aW1hZ2U="}]},{"type":"function_call","call_id":"call_one","name":"read","arguments":"{\"path\":\"a\"}","gemini_thought_signature":"signed-provider-history"},{"type":"function_call_output","call_id":"call_one","output":"result"}]}`)
	r, err := parseGeminiOpenAIRequestGeili(body, false, "gemini-3.1-pro")
	require.NoError(t, err)
	require.Equal(t, "signed-provider-history", gjson.GetBytes(r.body, "contents.1.parts.0.thoughtSignature").String())
	require.Equal(t, "image/png", gjson.GetBytes(r.body, "contents.0.parts.1.inlineData.mimeType").String())
	require.Equal(t, "result", gjson.GetBytes(r.body, "contents.2.parts.0.functionResponse.response.output").String())
	require.Equal(t, "ANY", gjson.GetBytes(r.body, "toolConfig.functionCallingConfig.mode").String())
	require.Equal(t, "read", gjson.GetBytes(r.body, "toolConfig.functionCallingConfig.allowedFunctionNames.0").String())
}
func TestGeminiOpenAIRequestCodexToolLowering(t *testing.T) {
	body := []byte(`{"model":"gemini-test","tools":[{"type":"custom","name":"apply_patch","format":{"type":"text"}}],"input":"edit"}`)
	r, err := parseGeminiOpenAIRequestGeili(body, false, "gemini-3.1-pro")
	require.NoError(t, err)
	require.True(t, r.mapping.CustomTools["apply_patch"])
	require.Equal(t, "apply_patch", gjson.GetBytes(r.body, "tools.0.functionDeclarations.0.name").String())
	require.Equal(t, "string", gjson.GetBytes(r.body, "tools.0.functionDeclarations.0.parametersJsonSchema.properties.input.type").String())
	out := &apicompat.ResponsesResponse{ID: "resp_test", Output: []apicompat.ResponsesOutput{{Type: "function_call", ID: "fc_test", CallID: "call_test", Name: "apply_patch", Arguments: `{"input":"*** patch ***"}`}}}
	b, _ := json.Marshal(out)
	restored, _, err := apicompat.RestoreResponsesClientToolPayload(b, r.mapping)
	require.NoError(t, err)
	require.Equal(t, "custom_tool_call", gjson.GetBytes(restored, "output.0.type").String())
	require.Equal(t, "*** patch ***", gjson.GetBytes(restored, "output.0.input").String())
}
func TestGeminiOpenAIOutputLifecycleToolsUsageAndTruncation(t *testing.T) {
	var events []apicompat.ResponsesStreamEvent
	out := newGeminiOpenAIOutputGeili("gemini-alias", time.Now(), func(e apicompat.ResponsesStreamEvent) error { events = append(events, e); return nil })
	require.NoError(t, out.begin())
	require.NoError(t, out.consume([]byte(`{"candidates":[{"content":{"parts":[{"text":"hello "}]}}]}`)))
	require.NoError(t, out.consume([]byte(`{"candidates":[{"content":{"parts":[{"text":"world"},{"functionCall":{"name":"read","args":{"path":"a"}},"thoughtSignature":"sig_a"},{"functionCall":{"name":"read","args":{"path":"b"}},"thoughtSignature":"sig_b"}]},"finishReason":"MAX_TOKENS"}],"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":4,"candidatesTokenCount":2,"thoughtsTokenCount":3}}`)))
	require.NoError(t, out.finalize())
	require.Equal(t, "incomplete", out.response.Status)
	require.Equal(t, "max_output_tokens", out.response.IncompleteDetails.Reason)
	require.Len(t, out.response.Output, 3)
	require.NotEqual(t, out.response.Output[1].CallID, out.response.Output[2].CallID)
	require.Equal(t, 6, out.usage.InputTokens)
	require.Equal(t, 4, out.usage.CacheReadInputTokens)
	require.Equal(t, 5, out.usage.OutputTokens)
	require.Equal(t, 10, out.response.Usage.InputTokens)
	for i, e := range events {
		require.Equal(t, i, e.SequenceNumber)
	}
	require.Equal(t, "response.incomplete", events[len(events)-1].Type)
	b, err := geminiOpenAISignedPayloadGeili(out.response, out.signatures)
	require.NoError(t, err)
	require.Equal(t, "sig_b", gjson.GetBytes(b, "output.2.gemini_thought_signature").String())
}
func TestGeminiOpenAIOutputInvalidEmptyAndMissingTermination(t *testing.T) {
	for _, body := range []string{`{"candidates":[{"content":{"parts":[{"text":"part"}]}}]}`, `{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}]}`, `{"candidates":[{"finishReason":"MALFORMED_FUNCTION_CALL"}]}`} {
		out := newGeminiOpenAIOutputGeili("model", time.Now(), nil)
		require.NoError(t, out.consume([]byte(body)))
		require.Error(t, out.finalize())
	}
	out := newGeminiOpenAIOutputGeili("model", time.Now(), nil)
	require.Error(t, out.consume([]byte(`{"error":{"code":503,"message":"failure","status":"UNAVAILABLE"}}`)))
	require.Error(t, out.consume([]byte(`broken`)))
}
func TestGeminiOpenAIForwardNativeBothProtocols(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(map[bool]string{false: "responses", true: "chat"}[chat]+map[bool]string{false: " buffered", true: " stream"}[stream], func(t *testing.T) {
				body := map[string]any{"model": "gemini-test", "stream": stream, "max_output_tokens": 1}
				path := "/v1/responses"
				if chat {
					body["messages"] = []any{map[string]any{"role": "user", "content": "hi"}}
					delete(body, "max_output_tokens")
					body["max_completion_tokens"] = 1
					path = "/v1/chat/completions"
				} else {
					body["input"] = "hi"
				}
				b, _ := json.Marshal(body)
				native := `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":2}}`
				if stream {
					native = "data: " + native + "\n\n"
				}
				upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(native))}}}
				svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
				c, w := newAntigravityCompatContext(http.MethodPost, path, b)
				result, err := svc.forwardOpenAINativeGeili(context.Background(), c, geminiSignalTestAccount(), b, chat)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 200, w.Code)
				require.Contains(t, w.Body.String(), "ok")
				require.Len(t, upstream.requestBodies, 1)
				require.Equal(t, int64(1), gjson.GetBytes(upstream.requestBodies[0], "generationConfig.maxOutputTokens").Int())
				require.False(t, gjson.GetBytes(upstream.requestBodies[0], "messages").Exists())
				if stream && !chat {
					require.Contains(t, w.Body.String(), "event: response.completed")
				}
				if stream && chat {
					require.Contains(t, w.Body.String(), "data: [DONE]")
				}
			})
		}
	}
}
func TestGeminiOpenAIForwardInvalidDoesNotCallUpstream(t *testing.T) {
	upstream := &queuedHTTPUpstreamStub{}
	svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
	body := []byte(`{"model":"gemini-test","input":"hi","max_output_tokens":0}`)
	c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
	result, err := svc.ForwardAsResponsesGeili(context.Background(), c, geminiSignalTestAccount(), body)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, 400, w.Code)
	require.Empty(t, upstream.requestBodies)
}
func TestGeminiOpenAIForwardMissingFinishSafeBeforeOutput(t *testing.T) {
	body := []byte(`{"model":"gemini-test","input":"hi","stream":true}`)
	for _, native := range []string{"data: {}\n\n", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]}}]}\n\n"} {
		upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(native))}}}
		svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
		c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
		result, err := svc.ForwardAsResponsesGeili(context.Background(), c, geminiSignalTestAccount(), body)
		require.Error(t, err)
		var failover *UpstreamFailoverError
		if strings.Contains(native, "partial") {
			require.NotNil(t, result)
			require.Contains(t, w.Body.String(), "response.failed")
			require.False(t, errors.As(err, &failover))
		} else {
			require.Nil(t, result)
			require.ErrorAs(t, err, &failover)
			require.True(t, failover.SafeToFailoverAfterWrite)
			require.NotContains(t, w.Body.String(), "resp_")
		}
	}
}

func TestGeminiOpenAIChatNativeHistoryAndLegacyTools(t *testing.T) {
	body := []byte(`{"model":"alias","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1hZ2U="}}]},{"role":"assistant","reasoning_content":"plan","content":null,"tool_calls":[{"id":"call_a","type":"function","function":{"name":"read","arguments":"{}"},"gemini_thought_signature":"original-sig"}]},{"role":"tool","tool_call_id":"call_a","content":[{"type":"text","text":"result"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1hZ2U="}}]}],"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}]}`)
	r, err := parseGeminiOpenAIRequestGeili(body, true, "gemini-3.1-pro")
	require.NoError(t, err)
	require.NotContains(t, string(r.body), "<thinking>")
	require.True(t, gjson.GetBytes(r.body, "contents.1.parts.0.thought").Bool())
	require.Equal(t, "original-sig", gjson.GetBytes(r.body, "contents.1.parts.1.thoughtSignature").String())
	require.Equal(t, "result", gjson.GetBytes(r.body, "contents.2.parts.0.functionResponse.response.output").String())
	require.Equal(t, "image/png", gjson.GetBytes(r.body, "contents.2.parts.0.functionResponse.parts.0.inlineData.mimeType").String())
	legacy := []byte(`{"model":"alias","messages":[{"role":"user","content":"read"},{"role":"assistant","function_call":{"name":"read","arguments":"{}"}},{"role":"function","name":"read","content":"done"}],"functions":[{"name":"read","parameters":{"type":"object"}}],"function_call":{"name":"read"}}`)
	r, err = parseGeminiOpenAIRequestGeili(legacy, true, "gemini-3.1-pro")
	require.NoError(t, err)
	require.Equal(t, "read", gjson.GetBytes(r.body, "toolConfig.functionCallingConfig.allowedFunctionNames.0").String())
	require.Equal(t, "done", gjson.GetBytes(r.body, "contents.2.parts.0.functionResponse.response.output").String())
}
func TestGeminiOpenAIPartialArgumentsAndRepeatedSameNameCalls(t *testing.T) {
	out := newGeminiOpenAIOutputGeili("gemini-test", time.Now(), nil)
	require.NoError(t, out.consume([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"tool","args":"{\"a\":"}}]}}]}`)))
	require.NoError(t, out.consume([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"tool","args":"1}"},"thoughtSignature":"sig"}]}}]}`)))
	require.NoError(t, out.consume([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"tool","args":{"a":2}}}]},"finishReason":"STOP"}]}`)))
	require.NoError(t, out.finalize())
	require.Len(t, out.response.Output, 2)
	require.JSONEq(t, `{"a":1}`, out.response.Output[0].Arguments)
	require.JSONEq(t, `{"a":2}`, out.response.Output[1].Arguments)
}
func TestGeminiOpenAIForwardProviderFailuresNeverRetrySameAccount(t *testing.T) {
	for _, status := range []int{400, 403} {
		msg := supplierCapacityMessageGeili
		if status == 403 {
			msg = supplierPrechargeMessageGeili
		}
		upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: status, Header: http.Header{"X-Request-Id": []string{"provider-id"}}, Body: io.NopCloser(strings.NewReader(string(supplierMessageBodyGeili(msg))))}}}
		svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
		body := []byte(`{"model":"gemini-test","input":"hi","stream":true}`)
		c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
		result, err := svc.ForwardAsResponsesGeili(context.Background(), c, geminiPoolModeAPIKeyAccount(), body)
		require.Nil(t, result)
		var f *UpstreamFailoverError
		require.ErrorAs(t, err, &f)
		require.True(t, IsGeminiSupplierUnavailableGeili(f))
		require.False(t, f.RetryableOnSameAccount)
		require.Equal(t, 503, f.ClientStatusCode)
		require.Len(t, upstream.requestBodies, 1)
		require.NotContains(t, w.Body.String(), "预扣费")
	}
}
func TestGeminiOpenAIForwardSignatureInBothProtocolFinalAndStream(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			body := map[string]any{"model": "gemini-test", "stream": stream}
			if chat {
				body["messages"] = []any{map[string]any{"role": "user", "content": "hi"}}
			} else {
				body["input"] = "hi"
			}
			b, _ := json.Marshal(body)
			native := `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read","args":{"path":"a"}},"thoughtSignature":"provider-signature"}]},"finishReason":"STOP"}]}`
			if stream {
				native = "data: " + native + "\n\n"
			}
			upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(native))}}}
			svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
			c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", b)
			_, err := svc.forwardOpenAINativeGeili(context.Background(), c, geminiSignalTestAccount(), b, chat)
			require.NoError(t, err)
			require.Contains(t, w.Body.String(), `"gemini_thought_signature":"provider-signature"`)
		}
	}
}
func TestGeminiOpenAIForwardOAuthWrappedAndMappedModel(t *testing.T) {
	body := []byte(`{"model":"alias","input":"hi","reasoning":{"effort":"high"}}`)
	account := geminiSignalTestAccount()
	account.Credentials["model_mapping"] = map[string]any{"alias": "gemini-3.1-pro"}
	var path string
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))}}, onCall: func(req *http.Request, _ *queuedHTTPUpstreamStub) { path = req.URL.Path }}
	svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
	c, _ := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
	result, err := svc.ForwardAsResponsesGeili(context.Background(), c, account, body)
	require.NoError(t, err)
	require.Equal(t, "gemini-3.1-pro", result.UpstreamModel)
	require.Contains(t, path, "gemini-3.1-pro:generateContent")
	require.Equal(t, "HIGH", gjson.GetBytes(upstream.requestBodies[0], "generationConfig.thinkingConfig.thinkingLevel").String())
	oauth := geminiSignalTestAccount()
	oauth.Type = AccountTypeOAuth
	oauth.Credentials["access_token"] = "test-token"
	oauth.Credentials["project_id"] = "test-project"
	oauth.Credentials["expires_at"] = time.Now().Add(time.Hour).Format(time.RFC3339)
	wrapped := "data: " + `{"response":{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}}` + "\n\n"
	upstream = &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(wrapped))}}}
	svc = &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}, tokenProvider: &GeminiTokenProvider{}}
	body = []byte(`{"model":"gemini-3.1-pro","input":"hi"}`)
	c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
	result, err = svc.ForwardAsResponsesGeili(context.Background(), c, oauth, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, w.Body.String(), "ok")
	require.True(t, gjson.GetBytes(upstream.requestBodies[0], "request.contents").Exists())
}
func TestGeminiOpenAIForwardCancellationClosesReader(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: reader}}}
	svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
	body := []byte(`{"model":"gemini-test","input":"hi","stream":true}`)
	c, _ := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := svc.ForwardAsResponsesGeili(ctx, c, geminiSignalTestAccount(), body); done <- err }()
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled forward did not return")
	}
	_, err := writer.Write([]byte("data: {}\n\n"))
	require.Error(t, err)
}

func TestGeminiOpenAIRejectRemoteToolImageBeforeForward(t *testing.T) {
	body := []byte(`{"model":"gemini-test","input":[{"type":"function_call","name":"read","call_id":"call_a","arguments":"{}"},{"type":"function_call_output","call_id":"call_a","output":[{"type":"input_image","image_url":"https://example.com/result.png"}]}]}`)
	_, err := parseGeminiOpenAIRequestGeili(body, false, "gemini-3.1-pro")
	require.ErrorContains(t, err, "inline base64")
}
func TestGeminiOpenAIThinkingModelCapabilities(t *testing.T) {
	for _, tc := range []struct {
		model, effort string
		ok            bool
		level         string
		budget        int64
	}{
		{"gemini-3-pro-preview", "medium", false, "", 0}, {"gemini-3-pro-preview", "high", true, "HIGH", 0}, {"gemini-3.1-pro-preview", "medium", true, "MEDIUM", 0}, {"gemini-3-flash-preview", "minimal", true, "MINIMAL", 0}, {"gemini-4-argon", "medium", false, "", 0}, {"argon", "high", false, "", 0}, {"gemini-2.5-pro", "none", false, "", 0}, {"gemini-2.5-pro", "medium", true, "", 4096},
	} {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			cfg, err := geminiOpenAIThinkingGeili(tc.model, tc.effort)
			if !tc.ok {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			b, _ := json.Marshal(cfg)
			require.Equal(t, tc.level, gjson.GetBytes(b, "thinkingLevel").String())
			require.Equal(t, tc.budget, gjson.GetBytes(b, "thinkingBudget").Int())
		})
	}
}

func TestGeminiOpenAIProviderCallIDsRoundTripSameNameReverseResults(t *testing.T) {
	out := newGeminiOpenAIOutputGeili("gemini-test", time.Now(), nil)
	require.NoError(t, out.consume([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"id":"provider_a","name":"read","args":{"path":"a"}}},{"functionCall":{"id":"provider_b","name":"read","args":{"path":"b"}}}]},"finishReason":"STOP"}]}`)))
	require.NoError(t, out.finalize())
	require.Equal(t, "provider_a", out.response.Output[0].CallID)
	require.Equal(t, "provider_b", out.response.Output[1].CallID)
	history := []any{out.response.Output[0], out.response.Output[1], map[string]any{"type": "function_call_output", "call_id": "provider_b", "output": "B"}, map[string]any{"type": "function_call_output", "call_id": "provider_a", "output": "A"}}
	body, _ := json.Marshal(map[string]any{"model": "gemini-test", "input": history})
	r, err := parseGeminiOpenAIRequestGeili(body, false, "gemini-3.1-pro")
	require.NoError(t, err)
	require.Equal(t, "provider_a", gjson.GetBytes(r.body, "contents.0.parts.0.functionCall.id").String())
	require.Equal(t, "provider_b", gjson.GetBytes(r.body, "contents.0.parts.1.functionCall.id").String())
	require.Equal(t, "provider_b", gjson.GetBytes(r.body, "contents.1.parts.0.functionResponse.id").String())
	require.Equal(t, "B", gjson.GetBytes(r.body, "contents.1.parts.0.functionResponse.response.output").String())
	require.Equal(t, "provider_a", gjson.GetBytes(r.body, "contents.1.parts.1.functionResponse.id").String())
}

func TestGeminiOpenAIForwardAllPreContentFailuresRemainReplayable(t *testing.T) {
	for _, payload := range []string{`not JSON`, `{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}]}`, `{"error":{"code":503,"status":"UNAVAILABLE","message":"temporarily unavailable"}}`} {
		upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: " + payload + "\n\n"))}}}
		svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
		body := []byte(`{"model":"gemini-test","input":"hi","stream":true}`)
		c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
		result, err := svc.ForwardAsResponsesGeili(context.Background(), c, geminiSignalTestAccount(), body)
		require.Nil(t, result)
		var f *UpstreamFailoverError
		require.ErrorAs(t, err, &f, payload)
		require.True(t, f.SafeToFailoverAfterWrite)
		require.NotContains(t, w.Body.String(), "resp_")
	}
}
func TestGeminiOpenAIForwardPreContentIdleAndFirstDeadline(t *testing.T) {
	for _, sendData := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "first content"}[sendData], func(t *testing.T) {
			reader, writer := io.Pipe()
			defer writer.Close()
			upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: reader}}}
			svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}}
			body := []byte(`{"model":"gemini-test","input":"hi","stream":true}`)
			c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
			done := make(chan struct{})
			defer close(done)
			if sendData {
				go func() {
					ticker := time.NewTicker(20 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-done:
							return
						case <-ticker.C:
							if _, err := io.WriteString(writer, "data: {}\n\n"); err != nil {
								return
							}
						}
					}
				}()
			}
			result, err := svc.ForwardAsResponsesGeili(context.Background(), c, geminiSignalTestAccount(), body)
			require.Nil(t, result)
			var f *UpstreamFailoverError
			require.ErrorAs(t, err, &f)
			require.True(t, f.SafeToFailoverAfterWrite)
			require.NotContains(t, w.Body.String(), "resp_")
		})
	}
}

func TestGeminiOpenAIStrictSchemaRejectsUnsupportedConstraints(t *testing.T) {
	for _, constraint := range []map[string]any{{"pattern": "^[A-Z]+$"}, {"minLength": float64(5)}, {"maxLength": float64(5)}, {"const": "fixed"}, {"exclusiveMinimum": float64(1)}, {"default": "value"}, {"definitions": map[string]any{}}, {"oneOf": []any{map[string]any{"type": "string"}}}} {
		schema := map[string]any{"type": "string"}
		for k, v := range constraint {
			schema[k] = v
		}
		body, _ := json.Marshal(map[string]any{"model": "gemini-test", "input": "hi", "text": map[string]any{"format": map[string]any{"type": "json_schema", "strict": true, "schema": schema}}})
		_, err := parseGeminiOpenAIRequestGeili(body, false, "gemini-3.1-pro")
		require.Error(t, err, string(body))
	}
}

func TestGeminiOpenAIForwardBufferedIncompleteGenerationNeverSettlesPartial(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, finish := range []string{"", "MALFORMED_FUNCTION_CALL"} {
			body := map[string]any{"model": "gemini-test"}
			if chat {
				body["messages"] = []any{map[string]any{"role": "user", "content": "hi"}}
			} else {
				body["input"] = "hi"
			}
			b, _ := json.Marshal(body)
			native, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "partial"}}}, "finishReason": finish}}, "usageMetadata": map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 2}})
			upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(native)))}}}
			svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
			c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", b)
			result, err := svc.forwardOpenAINativeGeili(context.Background(), c, geminiSignalTestAccount(), b, chat)
			require.Nil(t, result)
			var f *UpstreamFailoverError
			require.ErrorAs(t, err, &f)
			require.True(t, f.SafeToFailoverAfterWrite)
			require.Empty(t, w.Body.String())
		}
	}
}
func TestGeminiOpenAIToolArgumentsBudgetTruncationRetainsIncomplete(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			body := map[string]any{"model": "gemini-test", "stream": stream}
			if chat {
				body["messages"] = []any{map[string]any{"role": "user", "content": "hi"}}
			} else {
				body["input"] = "hi"
			}
			b, _ := json.Marshal(body)
			native := `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read","args":"{\"path\":"}}]},"finishReason":"MAX_TOKENS"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2}}`
			if stream {
				native = "data: " + native + "\n\n"
			}
			upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(native))}}}
			svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
			c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", b)
			result, err := svc.forwardOpenAINativeGeili(context.Background(), c, geminiSignalTestAccount(), b, chat)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotContains(t, w.Body.String(), "response.failed")
			if chat {
				require.Contains(t, w.Body.String(), `"finish_reason":"length"`)
			} else {
				require.Contains(t, w.Body.String(), `"status":"incomplete"`)
				require.Contains(t, w.Body.String(), `"reason":"max_output_tokens"`)
			}
		}
	}
	_, err := parseGeminiOpenAIRequestGeili([]byte(`{"model":"gemini-test","messages":[{"role":"user","content":"hi"}],"seed":0}`), true, "gemini-3.1-pro")
	require.ErrorContains(t, err, "seed")
}

func TestGeminiOpenAIOutputCumulativeTextAndThoughts(t *testing.T) {
	out := newGeminiOpenAIOutputGeili("gemini-test", time.Now(), nil)
	for _, part := range []string{`{"text":"hel"}`, `{"text":"hello"}`, `{"text":"hello"}`, `{"text":"hel"}`, `{"text":" world"}`, `{"text":"think","thought":true}`, `{"text":"thinking","thought":true}`, `{"text":"thinking","thought":true}`} {
		require.NoError(t, out.consume([]byte(`{"candidates":[{"content":{"parts":[`+part+`]}}]}`)))
	}
	require.NoError(t, out.consume([]byte(`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"thoughtsTokenCount":3}}`)))
	require.NoError(t, out.finalize())
	require.Equal(t, "hello world", out.response.Output[0].Content[0].Text)
	require.Equal(t, "thinking", out.response.Output[1].Summary[0].Text)
	require.Equal(t, 3, out.response.Usage.OutputTokensDetails.ReasoningTokens)
}
func TestGeminiOpenAIOutputRepeatedCompleteToolDoesNotDuplicate(t *testing.T) {
	out := newGeminiOpenAIOutputGeili("gemini-test", time.Now(), nil)
	frame := []byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read","args":{"path":"a"}}}]}}]}`)
	require.NoError(t, out.consume(frame))
	require.NoError(t, out.consume(frame))
	require.NoError(t, out.consume([]byte(`{"candidates":[{"finishReason":"STOP"}]}`)))
	require.NoError(t, out.finalize())
	require.Len(t, out.response.Output, 1)
}
func TestGeminiOpenAIImageExplicitDetailIsNotSilentlyDiscarded(t *testing.T) {
	for _, chat := range []bool{false, true} {
		body := `{"model":"gemini-test","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,aW1hZ2U=","detail":"high"}]}]}`
		if chat {
			body = `{"model":"gemini-test","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1hZ2U=","detail":"low"}}]}]}`
		}
		_, err := parseGeminiOpenAIRequestGeili([]byte(body), chat, "gemini-3.1-pro")
		require.ErrorContains(t, err, "image detail")
	}
}

func TestGeminiOpenAICodexNamespaceSearchAndCustomStream(t *testing.T) {
	body := []byte(`{"model":"gemini-test","stream":true,"tools":[{"type":"custom","name":"apply_patch","format":{"type":"text"}},{"type":"tool_search"},{"type":"namespace","name":"team","tools":[{"type":"function","name":"send","parameters":{"type":"object"}}]}],"input":"work"}`)
	native := `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"apply_patch","args":{"input":"*** patch ***"}}},{"functionCall":{"name":"tool_search","args":{"query":"repo"}}},{"functionCall":{"name":"team__send","args":{}}}]},"finishReason":"STOP"}]}`
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: " + native + "\n\n"))}}}
	svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
	c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
	result, err := svc.ForwardAsResponsesGeili(context.Background(), c, geminiSignalTestAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	text := w.Body.String()
	require.Contains(t, text, `"type":"custom_tool_call"`)
	require.Contains(t, text, `"input":"*** patch ***"`)
	require.Contains(t, text, `"type":"tool_search_call"`)
	require.Contains(t, text, `"execution":"client"`)
	require.Contains(t, text, `"namespace":"team"`)
	require.Contains(t, text, `"name":"send"`)
	require.NotContains(t, text, `"name":"team__send"`)
}

func TestGeminiOpenAIStrictFunctionCallingModes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		choice any
		mode   string
	}{{"default", nil, "VALIDATED"}, {"auto", "auto", "VALIDATED"}, {"required", "required", "ANY"}, {"named", map[string]any{"type": "function", "name": "read"}, "ANY"}, {"none", "none", "NONE"}} {
		t.Run(tc.name, func(t *testing.T) {
			tools := []any{map[string]any{"type": "function", "name": "read", "strict": true, "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "pattern": "^[A-Z]+$", "minLength": float64(1), "maxLength": float64(64)}}, "additionalProperties": false}}, map[string]any{"type": "function", "name": "write", "strict": false, "parameters": map[string]any{"type": "object"}}}
			req := map[string]any{"model": "gemini-test", "input": "hi", "tools": tools}
			if tc.choice != nil {
				req["tool_choice"] = tc.choice
			}
			body, _ := json.Marshal(req)
			r, err := parseGeminiOpenAIRequestGeili(body, false, "gemini-3.1-pro")
			require.NoError(t, err)
			require.Equal(t, tc.mode, gjson.GetBytes(r.body, "toolConfig.functionCallingConfig.mode").String())
			require.Equal(t, "^[A-Z]+$", gjson.GetBytes(r.body, "tools.0.functionDeclarations.0.parametersJsonSchema.properties.path.pattern").String())
			require.Equal(t, int64(64), gjson.GetBytes(r.body, "tools.0.functionDeclarations.0.parametersJsonSchema.properties.path.maxLength").Int())
			if tc.name == "named" {
				require.Equal(t, "read", gjson.GetBytes(r.body, "toolConfig.functionCallingConfig.allowedFunctionNames.0").String())
			}
		})
	}
}
func TestGeminiOpenAIResponsesLegacyIngressPreservesExplicitBudget(t *testing.T) {
	for _, tc := range []struct {
		name, body, text string
		budget           int64
	}{{"legacy messages", `{"model":"gemini-test","messages":[{"role":"user","content":"legacy"}],"max_tokens":1}`, "legacy", 1}, {"native authoritative", `{"model":"gemini-test","input":"native","messages":[{"role":"user","content":"legacy"}],"max_output_tokens":1}`, "native", 1}, {"responses budget overrides legacy", `{"model":"gemini-test","messages":[{"role":"user","content":"legacy"}],"max_tokens":128,"max_output_tokens":1}`, "legacy", 1}, {"legacy prompt", `{"model":"gemini-test","prompt":"prompt"}`, "prompt", 8192}} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := parseGeminiOpenAIRequestGeili([]byte(tc.body), false, "gemini-3.1-pro")
			require.NoError(t, err)
			require.Equal(t, tc.text, gjson.GetBytes(r.body, "contents.0.parts.0.text").String())
			require.Equal(t, tc.budget, gjson.GetBytes(r.body, "generationConfig.maxOutputTokens").Int())
		})
	}
}

func TestGeminiOpenAIReasoningAndFormatContractRejectsSilentLoss(t *testing.T) {
	for _, fragment := range []string{`"reasoning":{"summary":"concise"}`, `"reasoning":{"summary":"detailed"}`, `"reasoning":{"summary":true}`, `"reasoning":{"effort":3}`, `"reasoning":{"effort":null}`, `"text":{"format":"json_schema"}`} {
		body := []byte(`{"model":"gemini-3.1-pro","input":"hi",` + fragment + `}`)
		upstream := &queuedHTTPUpstreamStub{}
		svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
		c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
		result, err := svc.ForwardAsResponsesGeili(context.Background(), c, geminiSignalTestAccount(), body)
		require.Error(t, err, fragment)
		require.Nil(t, result)
		require.Equal(t, 400, w.Code)
		require.Empty(t, upstream.requestBodies)
	}
	r, err := parseGeminiOpenAIRequestGeili([]byte(`{"model":"gemini-3.1-pro","input":"hi","reasoning":{"summary":"auto"}}`), false, "gemini-3.1-pro")
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(r.body, "generationConfig.thinkingConfig.includeThoughts").Bool())
	require.False(t, gjson.GetBytes(r.body, "generationConfig.thinkingConfig.thinkingLevel").Exists())
	require.False(t, gjson.GetBytes(r.body, "generationConfig.thinkingConfig.thinkingBudget").Exists())
	_, err = parseGeminiOpenAIRequestGeili([]byte(`{"model":"alias","input":"hi","reasoning":{"summary":"auto"}}`), false, "unknown-alias")
	require.Error(t, err)
}
func TestGeminiOpenAINativeTextOnlyBoundary(t *testing.T) {
	account := geminiSignalTestAccount()
	require.True(t, ShouldUseGeminiOpenAINativeGeili(account, "gemini-3.1-pro"))
	require.False(t, ShouldUseGeminiOpenAINativeGeili(nil, "gemini-3.1-pro"))
	require.False(t, ShouldUseGeminiOpenAINativeGeili(&Account{Platform: PlatformAnthropic}, "gemini-3.1-pro"))
	require.False(t, ShouldUseGeminiOpenAINativeGeili(account, "gemini-2.5-flash-image"))
	account.Credentials["model_mapping"] = map[string]any{"image-alias": "gemini-2.5-flash-image"}
	require.False(t, ShouldUseGeminiOpenAINativeGeili(account, "image-alias"))
	for _, model := range []string{"gemini-2.5-flash-image", "image-alias"} {
		upstream := &queuedHTTPUpstreamStub{}
		svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
		body, _ := json.Marshal(map[string]any{"model": model, "input": "render"})
		c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
		result, err := svc.ForwardAsResponsesGeili(context.Background(), c, account, body)
		require.Nil(t, result)
		require.Error(t, err)
		require.Equal(t, 400, w.Code)
		require.Empty(t, upstream.requestBodies)
	}
	body := []byte(`{"model":"unknown-text-alias","input":"render"}`)
	native := `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2}}`
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(native))}}}
	svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
	c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
	result, err := svc.ForwardAsResponsesGeili(context.Background(), c, geminiSignalTestAccount(), body)
	require.Nil(t, result)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Empty(t, w.Body.String())
	require.NotContains(t, w.Body.String(), "image/png")
}

func TestGeminiOpenAIOptionalTopLevelNullUsesOmittedDefaults(t *testing.T) {
	fields := []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "tools", "functions", "reasoning_effort", "temperature", "top_p"}
	for _, chat := range []bool{false, true} {
		for _, field := range append(append([]string{}, fields...), "all") {
			t.Run(map[bool]string{false: "responses", true: "chat"}[chat]+"/"+field, func(t *testing.T) {
				body := map[string]any{"model": "gemini-3.1-pro"}
				if chat {
					body["messages"] = []any{map[string]any{"role": "user", "content": "synthetic"}}
				} else {
					body["input"] = "synthetic"
				}
				if field == "all" {
					for _, key := range fields {
						body[key] = nil
					}
				} else {
					body[field] = nil
				}
				raw, _ := json.Marshal(body)
				request, err := parseGeminiOpenAIRequestGeili(raw, chat, "gemini-3.1-pro")
				require.NoError(t, err)
				require.Equal(t, int64(8192), gjson.GetBytes(request.body, "generationConfig.maxOutputTokens").Int())
				for _, path := range []string{"generationConfig.temperature", "generationConfig.topP", "generationConfig.thinkingConfig", "tools"} {
					require.False(t, gjson.GetBytes(request.body, path).Exists(), path)
				}
			})
		}
	}
}

func TestGeminiOpenAINullBudgetDoesNotOverridePositiveBudget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options map[string]any
		budget  int64
	}{
		{"null legacy then positive completion", map[string]any{"max_tokens": nil, "max_completion_tokens": 1}, 1},
		{"positive legacy then null completion", map[string]any{"max_tokens": 1, "max_completion_tokens": nil}, 1},
		{"both null", map[string]any{"max_tokens": nil, "max_completion_tokens": nil}, 8192},
		{"positive completion keeps precedence", map[string]any{"max_tokens": 2, "max_completion_tokens": 1}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"model": "gemini-3.1-pro", "messages": []any{map[string]any{"role": "user", "content": "synthetic"}}}
			for key, value := range tc.options {
				body[key] = value
			}
			raw, _ := json.Marshal(body)
			request, err := parseGeminiOpenAIRequestGeili(raw, true, "gemini-3.1-pro")
			require.NoError(t, err)
			require.Equal(t, tc.budget, gjson.GetBytes(request.body, "generationConfig.maxOutputTokens").Int())
		})
	}
	// Null normalization must not create Chat budget aliases for native Responses.
	request, err := parseGeminiOpenAIRequestGeili([]byte(`{"model":"gemini-3.1-pro","input":"synthetic","max_output_tokens":null,"max_tokens":1,"max_completion_tokens":1}`), false, "gemini-3.1-pro")
	require.NoError(t, err)
	require.Equal(t, int64(8192), gjson.GetBytes(request.body, "generationConfig.maxOutputTokens").Int())
}

func TestGeminiOpenAINullNormalizationPreservesRejectionAndNoDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, options string
		chat          bool
	}{
		{"zero legacy with null completion", `"max_tokens":0,"max_completion_tokens":null`, true},
		{"negative completion with null legacy", `"max_tokens":null,"max_completion_tokens":-1`, true},
		{"false legacy", `"max_tokens":false`, true},
		{"empty legacy", `"max_tokens":""`, true},
		{"string completion", `"max_completion_tokens":"1"`, true},
		{"false tools", `"tools":false`, true},
		{"false functions", `"functions":false`, true},
		{"false effort", `"reasoning_effort":false`, true},
		{"false temperature", `"temperature":false`, true},
		{"string top_p", `"top_p":"0.5"`, true},
		{"zero output", `"max_output_tokens":0`, false},
		{"negative output", `"max_output_tokens":-1`, false},
		{"nested effort null", `"reasoning":{"effort":null}`, false},
		{"nested format null", `"text":{"format":null}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := `"input":"synthetic"`
			path := "/v1/responses"
			if tc.chat {
				input = `"messages":[{"role":"user","content":"synthetic"}]`
				path = "/v1/chat/completions"
			}
			raw := []byte(`{"model":"gemini-3.1-pro",` + input + `,` + tc.options + `}`)
			upstream := &queuedHTTPUpstreamStub{}
			svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
			c, w := newAntigravityCompatContext(http.MethodPost, path, raw)
			result, err := svc.forwardOpenAINativeGeili(context.Background(), c, geminiSignalTestAccount(), raw, tc.chat)
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, 400, w.Code)
			require.Empty(t, upstream.requestBodies)
		})
	}
}
