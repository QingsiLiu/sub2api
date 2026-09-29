package service

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const geiliTestNormalizedPluginKey = "test.normalized-plugin"

func withGeiliNormalizedPluginKey(t *testing.T) {
	t.Helper()
	previous := geiliPluginResponseNormalizedKeys
	geiliPluginResponseNormalizedKeys = map[string]struct{}{geiliTestNormalizedPluginKey: {}}
	t.Cleanup(func() { geiliPluginResponseNormalizedKeys = previous })
}

func geiliTestPluginRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	// 插件转发会读走请求体，模型名必须从 GetBody 取副本。
	_, _ = io.ReadAll(request.Body)
	return request
}

func geiliTestPluginResponse(contentType, body string) *http.Response {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": []string{contentType}, "Content-Length": []string{"1"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func readGeiliNormalizedBody(t *testing.T, key, contentType, body string) (*http.Response, string) {
	t.Helper()
	request := geiliTestPluginRequest(t, `{"model":"gpt-6-sol","stream":true,"input":"hi"}`)
	response := normalizeGeiliPluginOpenAIResponse(&PluginInstallation{PluginKey: key}, request, geiliTestPluginResponse(contentType, body))
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	return response, string(raw)
}

func TestGeiliPluginResponseNormalizesStreamingResponses(t *testing.T) {
	withGeiliNormalizedPluginKey(t)
	stream := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-6-sol-excel"}}`,
		``,
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"model gpt-6-sol-excel cache_write_tokens"}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"model":"gpt-6-sol-excel","usage":{"input_tokens":100,"output_tokens":7,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":30}}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\r\n")

	response, out := readGeiliNormalizedBody(t, geiliTestNormalizedPluginKey, "text/event-stream; charset=utf-8", stream)

	require.EqualValues(t, -1, response.ContentLength)
	require.Empty(t, response.Header.Get("Content-Length"))
	require.NotContains(t, out, `"model":"gpt-6-sol-excel"`)
	// 正文里的同名文本不能被改写。
	require.Contains(t, out, `"delta":"model gpt-6-sol-excel cache_write_tokens"`)
	require.Contains(t, out, "data: [DONE]\r\n")
	require.Contains(t, out, "event: response.completed\r\n")

	svc := &OpenAIGatewayService{}
	usage := svc.parseSSEUsageFromBody(out)
	require.NotNil(t, usage)
	require.Equal(t, 0, usage.CacheCreationInputTokens)
	require.Equal(t, 60, usage.CacheReadInputTokens)
	require.Equal(t, 100, usage.InputTokens)
	require.Equal(t, 7, usage.OutputTokens)
}

func TestGeiliPluginResponseNormalizesNonStreamingResponses(t *testing.T) {
	withGeiliNormalizedPluginKey(t)
	body := `{"id":"resp_1","model":"gpt-6-sol-excel","output":[{"type":"message","content":[{"type":"output_text","text":"gpt-6-sol-excel"}]}],"usage":{"input_tokens":100,"output_tokens":7,"cache_creation_input_tokens":30,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":30}}}`

	_, out := readGeiliNormalizedBody(t, geiliTestNormalizedPluginKey, "application/json", body)

	require.Equal(t, "gpt-6-sol", gjson.Get(out, "model").String())
	require.Equal(t, "gpt-6-sol-excel", gjson.Get(out, "output.0.content.0.text").String())
	require.False(t, gjson.Get(out, "usage.cache_creation_input_tokens").Exists())
	require.EqualValues(t, 0, gjson.Get(out, "usage.input_tokens_details.cache_write_tokens").Int())
	usage, ok := extractOpenAIUsageFromJSONBytes([]byte(out))
	require.True(t, ok)
	require.Equal(t, 0, usage.CacheCreationInputTokens)
	require.Equal(t, 60, usage.CacheReadInputTokens)
	require.Equal(t, 100, usage.InputTokens)
}

func TestGeiliPluginResponseNormalizesChatCompletionsUsage(t *testing.T) {
	withGeiliNormalizedPluginKey(t)
	stream := "data: {\"id\":\"chat_1\",\"model\":\"gpt-6-sol-excel\",\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":7,\"prompt_tokens_details\":{\"cached_tokens\":60,\"cache_write_tokens\":30}}}\n\ndata: [DONE]\n\n"

	_, out := readGeiliNormalizedBody(t, geiliTestNormalizedPluginKey, "text/event-stream", stream)

	require.Contains(t, out, `"model":"gpt-6-sol"`)
	require.Contains(t, out, `"cache_write_tokens":0`)
	require.Contains(t, out, `"cached_tokens":60`)
}

func TestGeiliPluginResponseNormalizesSSEFramedBufferedBody(t *testing.T) {
	withGeiliNormalizedPluginKey(t)
	stream := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6-sol-excel\",\"usage\":{\"input_tokens\":10,\"output_tokens\":1,\"input_tokens_details\":{\"cache_write_tokens\":4}}}}\n\n"

	_, out := readGeiliNormalizedBody(t, geiliTestNormalizedPluginKey, "application/octet-stream", stream)

	require.Contains(t, out, `"model":"gpt-6-sol"`)
	require.Contains(t, out, `"cache_write_tokens":0`)
	require.True(t, strings.HasSuffix(out, "\n\n"))
}

func TestGeiliPluginResponseLeavesOtherPluginsUntouched(t *testing.T) {
	withGeiliNormalizedPluginKey(t)
	body := `{"model":"gpt-6-sol-excel","usage":{"input_tokens":100,"output_tokens":7,"input_tokens_details":{"cache_write_tokens":30}}}`

	for _, installation := range []*PluginInstallation{nil, {PluginKey: "com.example.other"}} {
		source := geiliTestPluginResponse("application/json", body)
		response := normalizeGeiliPluginOpenAIResponse(installation, geiliTestPluginRequest(t, `{"model":"gpt-6-sol"}`), source)
		require.Same(t, source, response)
		raw, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, body, string(raw))
		require.EqualValues(t, len(body), response.ContentLength)
	}
}

func TestGeiliPluginResponseSkipsErrorsAndCompressedBodies(t *testing.T) {
	withGeiliNormalizedPluginKey(t)
	installation := &PluginInstallation{PluginKey: geiliTestNormalizedPluginKey}
	body := `{"model":"gpt-6-sol-excel","usage":{"input_tokens_details":{"cache_write_tokens":30}}}`

	failed := geiliTestPluginResponse("application/json", body)
	failed.StatusCode = http.StatusTooManyRequests
	failedBody := failed.Body
	require.Equal(t, failedBody, normalizeGeiliPluginOpenAIResponse(installation, geiliTestPluginRequest(t, `{"model":"gpt-6-sol"}`), failed).Body)

	compressed := geiliTestPluginResponse("application/json", body)
	compressed.Header.Set("Content-Encoding", "gzip")
	compressedBody := compressed.Body
	require.Equal(t, compressedBody, normalizeGeiliPluginOpenAIResponse(installation, geiliTestPluginRequest(t, `{"model":"gpt-6-sol"}`), compressed).Body)
}

func TestGeiliPluginResponseKeepsModelWhenRequestModelUnknown(t *testing.T) {
	withGeiliNormalizedPluginKey(t)
	request, err := http.NewRequest(http.MethodPost, "https://example.test/responses", io.NopCloser(strings.NewReader(`{"model":"gpt-6-sol"}`)))
	require.NoError(t, err)
	require.Nil(t, request.GetBody)
	body := `{"model":"gpt-6-sol-excel","usage":{"input_tokens":10,"input_tokens_details":{"cache_write_tokens":4}}}`

	response := normalizeGeiliPluginOpenAIResponse(&PluginInstallation{PluginKey: geiliTestNormalizedPluginKey}, request, geiliTestPluginResponse("application/json", body))
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	require.Equal(t, "gpt-6-sol-excel", gjson.GetBytes(raw, "model").String())
	require.EqualValues(t, 0, gjson.GetBytes(raw, "usage.input_tokens_details.cache_write_tokens").Int())
}

func TestGeiliPluginResponseRestoresAnySentModel(t *testing.T) {
	withGeiliNormalizedPluginKey(t)
	installation := &PluginInstallation{PluginKey: geiliTestNormalizedPluginKey}
	for _, tc := range []struct{ sent, raw string }{
		{sent: "gpt-6-sol", raw: "gpt-6-sol-excel"},
		{sent: "gpt-6-luna", raw: "gpt-6-luna-excel"},
		{sent: "gpt-6-astra", raw: "gpt-6-astra-internal"},
		{sent: "gpt-5.6", raw: "gpt-5.6-2026-08-01"},
		{sent: "gpt-5.6-codex", raw: "something-else"},
	} {
		request := geiliTestPluginRequest(t, `{"model":"`+tc.sent+`","input":"hi"}`)
		body := `{"model":"` + tc.raw + `","usage":{"input_tokens":10,"output_tokens":1}}`
		response := normalizeGeiliPluginOpenAIResponse(installation, request, geiliTestPluginResponse("application/json", body))
		raw, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, tc.sent, gjson.GetBytes(raw, "model").String(), tc.raw)
	}
}

// 插件改写后仍走核心的账号映射改名：客户端最终看到的是它请求的模型名。
func TestGeiliPluginResponseComposesWithAccountModelMapping(t *testing.T) {
	withGeiliNormalizedPluginKey(t)
	request := geiliTestPluginRequest(t, `{"model":"gpt-6-sol","input":"hi"}`)
	stream := "data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6-sol-excel\",\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n"
	response := normalizeGeiliPluginOpenAIResponse(&PluginInstallation{PluginKey: geiliTestNormalizedPluginKey}, request, geiliTestPluginResponse("text/event-stream", stream))
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	svc := &OpenAIGatewayService{}
	line := strings.SplitN(string(raw), "\n", 2)[0]
	clientLine := svc.replaceModelInSSELine(line, "gpt-6-sol", "gpt-6-high-iq")
	require.Contains(t, clientLine, `"model":"gpt-6-high-iq"`)
	require.NotContains(t, clientLine, "excel")
}
