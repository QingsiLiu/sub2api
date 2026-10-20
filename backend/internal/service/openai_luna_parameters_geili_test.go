package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func lunaParameterAccountGeili(baseURL string) *Account {
	return &Account{ID: 101, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "fixture-key", "base_url": baseURL},
		Extra:       map[string]any{"openai_responses_supported": true}}
}

func lunaParameterContextGeili(path string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	return c, rec
}

func TestGeiliLunaOfficialChatNonePreservesParameters(t *testing.T) {
	for _, model := range []string{"gpt-6-luna", "gpt-5.6-luna"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream_%t", model, stream), func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":"public-luna","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"none","max_completion_tokens":8,"temperature":0.3,"top_p":0.7,"stream":%t}`, stream))
				payload := `{"id":"chat_fixture","object":"chat.completion","model":"` + model + `","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"length"}],"usage":{"prompt_tokens":1,"completion_tokens":8,"total_tokens":9}}`
				contentType := "application/json"
				if stream {
					contentType = "text/event-stream"
					payload = "data: " + strings.ReplaceAll(payload, `"message":`, `"delta":`) + "\n\ndata: [DONE]\n\n"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(payload))}}
				svc := &OpenAIGatewayService{cfg: structuredInputRetryTestConfig(), httpUpstream: upstream}
				account := lunaParameterAccountGeili("https://api.openai.com/v1")
				account.Credentials["model_mapping"] = map[string]any{"public-luna": model}
				c, rec := lunaParameterContextGeili("/v1/chat/completions", body)
				result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
				require.NotNil(t, upstream.lastReq)
				require.Equal(t, "/v1/chat/completions", upstream.lastReq.URL.Path)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, int64(8), gjson.GetBytes(upstream.lastBody, "max_completion_tokens").Int())
				require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
				require.Equal(t, 0.3, gjson.GetBytes(upstream.lastBody, "temperature").Float())
				require.Equal(t, 0.7, gjson.GetBytes(upstream.lastBody, "top_p").Float())
				require.Contains(t, rec.Body.String(), `"finish_reason":"length"`)
				require.Equal(t, 8, result.Usage.OutputTokens)
			})
		}
	}
}

func TestGeiliLunaConvertedChatHonorsExplicitLimit(t *testing.T) {
	for _, model := range []string{"gpt-6-luna", "gpt-5.6-luna"} {
		for _, baseURL := range []string{"https://api.openai.com", "https://compat.example"} {
			for _, limit := range []int{16, 32, 256} {
				t.Run(fmt.Sprintf("%s/%s/%d", model, baseURL, limit), func(t *testing.T) {
					body := []byte(fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"none","max_tokens":512,"max_completion_tokens":%d,"temperature":0.3,"top_p":0.7}`, model, limit))
					payload := `data: {"type":"response.completed","response":{"id":"resp_fixture","model":"` + model + `","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n"
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(payload))}}
					svc := &OpenAIGatewayService{cfg: structuredInputRetryTestConfig(), httpUpstream: upstream}
					account := lunaParameterAccountGeili(baseURL)
					account.Extra["openai_responses_mode"] = "force_responses"
					c, _ := lunaParameterContextGeili("/v1/chat/completions", body)
					result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, "/v1/responses", upstream.lastReq.URL.Path)
					require.EqualValues(t, limit, gjson.GetBytes(upstream.lastBody, "max_output_tokens").Int())
					require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
					require.Equal(t, 0.3, gjson.GetBytes(upstream.lastBody, "temperature").Float())
					require.Equal(t, 0.7, gjson.GetBytes(upstream.lastBody, "top_p").Float())
				})
			}
		}
	}
}

func TestGeiliLunaOfficialResponsesPreservesNone(t *testing.T) {
	body := []byte(`{"model":"gpt-6-luna","input":"hello","reasoning":{"effort":"none"},"max_output_tokens":32,"stream":false}`)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"resp_fixture","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`))}}
	svc := &OpenAIGatewayService{cfg: structuredInputRetryTestConfig(), httpUpstream: upstream}
	c, _ := lunaParameterContextGeili("/v1/responses", body)
	result, err := svc.Forward(context.Background(), c, lunaParameterAccountGeili("https://api.openai.com"), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.Equal(t, int64(32), gjson.GetBytes(upstream.lastBody, "max_output_tokens").Int())
}

func TestGeiliLunaNativeChatRoutingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, baseURL, accountType, mode, model, effort string
		responsesShape                                  bool
		want                                            bool
	}{
		{name: "official default URL", accountType: AccountTypeAPIKey, model: "gpt-6-luna", effort: "none", want: true},
		{name: "official explicit auto", baseURL: "https://api.openai.com/v1", accountType: AccountTypeAPIKey, mode: "auto", model: "gpt-5.6-luna", effort: "none", want: true},
		{name: "forced Responses", baseURL: "https://api.openai.com", accountType: AccountTypeAPIKey, mode: "force_responses", model: "gpt-6-luna", effort: "none"},
		{name: "compatible provider", baseURL: "https://compat.example", accountType: AccountTypeAPIKey, model: "gpt-6-luna", effort: "none"},
		{name: "lookalike host", baseURL: "https://api.openai.com.example", accountType: AccountTypeAPIKey, model: "gpt-6-luna", effort: "none"},
		{name: "OAuth", accountType: AccountTypeOAuth, model: "gpt-6-luna", effort: "none"},
		{name: "SetupToken", accountType: AccountTypeSetupToken, model: "gpt-6-luna", effort: "none"},
		{name: "reasoning tools keep Responses", accountType: AccountTypeAPIKey, model: "gpt-6-luna", effort: "high"},
		{name: "default reasoning keeps Responses", accountType: AccountTypeAPIKey, model: "gpt-6-luna"},
		{name: "other model", accountType: AccountTypeAPIKey, model: "gpt-6.1-sol", effort: "none"},
		{name: "Responses shaped Chat", accountType: AccountTypeAPIKey, model: "gpt-6-luna", effort: "none", responsesShape: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := lunaParameterAccountGeili(tc.baseURL)
			account.Type = tc.accountType
			account.Extra["openai_responses_mode"] = tc.mode
			body := []byte(fmt.Sprintf(`{"model":%q,"reasoning_effort":%q,"messages":[{"role":"user","content":"hi"}]}`, tc.model, tc.effort))
			if tc.responsesShape {
				body = []byte(fmt.Sprintf(`{"model":%q,"reasoning":{"effort":"none"},"input":"hi"}`, tc.model))
			}
			require.Equal(t, tc.want, useNativeLunaChatGeili(account, body, ""))
		})
	}
}

func TestGeiliLunaConversionKeepsLegacyFloorAndReasoningSampling(t *testing.T) {
	limit := 32
	temperature := 0.3
	cfg := &config.Config{}
	cfg.Gateway.MinOutputTokens = 256
	account := lunaParameterAccountGeili("https://api.openai.com")
	request := &apicompat.ChatCompletionsRequest{Model: "gpt-5.6-luna", ReasoningEffort: "high", MaxCompletionTokens: &limit, Temperature: &temperature}
	out, err := chatCompletionsToResponsesForAccountGeili(cfg, account, request)
	require.NoError(t, err)
	require.Equal(t, limit, *out.MaxOutputTokens, "explicit cap wins over a configured floor")
	require.Nil(t, out.Temperature, "reasoning requests keep sampling filtered")
	request.MaxCompletionTokens = nil
	request.MaxTokens = &limit
	out, err = chatCompletionsToResponsesForAccountGeili(cfg, account, request)
	require.NoError(t, err)
	require.Equal(t, 256, *out.MaxOutputTokens, "legacy max_tokens floor is unchanged")
	request.MaxTokens = nil
	out, err = chatCompletionsToResponsesForAccountGeili(cfg, account, request)
	require.NoError(t, err)
	require.Nil(t, out.MaxOutputTokens, "no implicit cap is introduced")
}
