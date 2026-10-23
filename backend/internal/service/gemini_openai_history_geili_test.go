//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const geminiHistoryContinuationFixtureGeili = "Continue and complete the previous assistant turn from where it stopped. Do not repeat content already provided; use the tool results already present."

func TestGeminiOpenAIHistoryGeiliContinuation(t *testing.T) {
	for _, tc := range []struct {
		name, fields       string
		chat, continuation bool
		roles              []string
	}{
		{"assistant", `"input":[{"role":"user","content":"task"},{"role":"assistant","content":"partial"}]`, false, true, []string{"user", "model", "user"}},
		{"developer extracted", `"input":[{"role":"user","content":"task"},{"role":"assistant","content":"partial"},{"role":"developer","content":"rule"}]`, false, true, []string{"user", "model", "user"}},
		{"additional tools extracted", `"input":[{"role":"user","content":"task"},{"role":"assistant","content":"partial"},{"type":"additional_tools","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}]`, false, true, []string{"user", "model", "user"}},
		{"summary metadata", `"input":[{"role":"user","content":"task"},{"type":"reasoning","summary":[{"type":"summary_text","text":"old thought"}],"gemini_thought_signature":"metadata-only"}]`, false, false, []string{"user"}},
		{"empty user array", `"input":[{"role":"user","content":"task"},{"role":"assistant","content":"partial"},{"role":"user","content":[]}]`, false, true, []string{"user", "model", "user"}},
		{"blank user", `"input":[{"role":"user","content":"task"},{"role":"assistant","content":"partial"},{"role":"user","content":" \n\t"}]`, false, true, []string{"user", "model", "user"}},
		{"normal user", `"input":[{"role":"user","content":"task"},{"role":"assistant","content":"partial"},{"role":"user","content":"next"}]`, false, false, []string{"user", "model", "user"}},
		{"image user", `"input":[{"role":"user","content":[{"type":"input_text","text":" "},{"type":"input_image","image_url":"data:image/png;base64,aW1hZ2U="}]}]`, false, false, []string{"user"}},
		{"assistant text and image", `"input":[{"role":"user","content":"task"},{"role":"assistant","content":[{"type":"output_text","text":"partial"},{"type":"input_image","image_url":"data:image/png;base64,aW1hZ2U="}]}]`, false, true, []string{"user", "model", "user"}},
		{"chat assistant", `"messages":[{"role":"user","content":"task"},{"role":"assistant","content":"partial"}]`, true, true, []string{"user", "model", "user"}},
		{"chat developer", `"messages":[{"role":"user","content":"task"},{"role":"assistant","content":"partial"},{"role":"developer","content":"rule"}]`, true, true, []string{"user", "model", "user"}},
		{"legacy messages", `"messages":[{"role":"user","content":"task"},{"role":"assistant","content":"partial"}]`, false, true, []string{"user", "model", "user"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gemini-test",` + tc.fields + `}`)
			original := string(body)
			r, err := parseGeminiOpenAIRequestGeili(body, tc.chat, "gemini-4-argon")
			require.NoError(t, err)
			require.Equal(t, original, string(body))
			contents := gjson.GetBytes(r.body, "contents").Array()
			roles := make([]string, 0, len(contents))
			for _, content := range contents {
				roles = append(roles, content.Get("role").String())
				for _, part := range content.Get("parts").Array() {
					require.False(t, part.Get("thought").Exists())
					if part.Get("text").Exists() {
						require.NotEmpty(t, strings.TrimSpace(part.Get("text").String()))
					}
				}
			}
			require.Equal(t, tc.roles, roles)
			last := contents[len(contents)-1]
			require.Equal(t, tc.continuation, last.Get("parts.0.text").String() == geminiHistoryContinuationFixtureGeili)
			if strings.Contains(tc.name, "developer") {
				require.Equal(t, "rule", gjson.GetBytes(r.body, "systemInstruction.parts.0.text").String())
			}
			require.NotContains(t, string(r.body), "old thought")
			require.NotContains(t, string(r.body), "metadata-only")
			if tc.continuation {
				require.Equal(t, "partial", contents[1].Get("parts.0.text").String())
			}
			again, err := parseGeminiOpenAIRequestGeili(body, tc.chat, "gemini-4-argon")
			require.NoError(t, err)
			require.Equal(t, string(r.body), string(again.body))
		})
	}
}

func TestGeminiOpenAIHistoryGeiliToolResultsAndSummary(t *testing.T) {
	for _, summary := range []string{"", `,{"type":"reasoning","summary":[{"type":"summary_text","text":"old thought"}]}`} {
		body := []byte(`{"model":"gemini-test","input":[{"role":"user","content":"task"},{"type":"function_call","call_id":"native-a","name":"lookup","arguments":"{\"x\":1}","gemini_thought_signature":"sig-a"},{"type":"function_call","call_id":"native-b","name":"lookup","arguments":"{\"x\":2}","gemini_thought_signature":"sig-b"},{"type":"function_call_output","call_id":"native-b","output":"second"},{"type":"function_call_output","call_id":"native-a","output":""}` + summary + `]}`)
		r, err := parseGeminiOpenAIRequestGeili(body, false, "gemini-4-argon")
		require.NoError(t, err)
		require.Len(t, gjson.GetBytes(r.body, "contents").Array(), 3)
		require.Equal(t, "user", gjson.GetBytes(r.body, "contents.2.role").String())
		require.Equal(t, "sig-a", gjson.GetBytes(r.body, "contents.1.parts.0.thoughtSignature").String())
		require.Equal(t, "sig-b", gjson.GetBytes(r.body, "contents.1.parts.1.thoughtSignature").String())
		require.Equal(t, "native-b", gjson.GetBytes(r.body, "contents.2.parts.0.functionResponse.id").String())
		require.Equal(t, "native-a", gjson.GetBytes(r.body, "contents.2.parts.1.functionResponse.id").String())
		require.Equal(t, "second", gjson.GetBytes(r.body, "contents.2.parts.0.functionResponse.response.output").String())
		require.Equal(t, "", gjson.GetBytes(r.body, "contents.2.parts.1.functionResponse.response.output").String())
		require.NotContains(t, string(r.body), geminiHistoryContinuationFixtureGeili)
		require.NotContains(t, string(r.body), "old thought")
	}
}

func TestGeminiOpenAIHistoryGeiliCodexTools(t *testing.T) {
	body := []byte(`{"model":"gemini-test","tools":[{"type":"custom","name":"apply_patch","format":{"type":"text"}},{"type":"tool_search"},{"type":"namespace","name":"team","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}],"input":[{"role":"user","content":"task"},{"type":"custom_tool_call","call_id":"custom-a","name":"apply_patch","input":"patch"},{"type":"custom_tool_call_output","call_id":"custom-a","output":"patched"},{"type":"tool_search_call","call_id":"search-a","arguments":{"query":"repo"}},{"type":"tool_search_output","call_id":"search-a","output":"found"},{"type":"function_call","call_id":"namespace-a","namespace":"team","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"namespace-a","output":"located"},{"type":"reasoning","summary":[{"type":"summary_text","text":"old thought"}]}]}`)
	r, err := parseGeminiOpenAIRequestGeili(body, false, "gemini-4-argon")
	require.NoError(t, err)
	require.True(t, r.mapping.CustomTools["apply_patch"])
	require.True(t, r.mapping.ToolSearch)
	require.Contains(t, string(r.body), `"name":"team__lookup"`)
	require.Equal(t, "user", gjson.GetBytes(r.body, "contents.6.role").String())
	require.Equal(t, "located", gjson.GetBytes(r.body, "contents.6.parts.0.functionResponse.response.output").String())
	require.NotContains(t, string(r.body), "old thought")
}

func TestGeminiOpenAIHistoryGeiliRejectsBeforeSupplier(t *testing.T) {
	call := `{"type":"function_call","call_id":"native-a","name":"lookup","arguments":"{}"}`
	result := `{"type":"function_call_output","call_id":"native-a","output":"ok"}`
	for _, tc := range []struct{ name, input string }{
		{"missing result", `[` + call + `]`},
		{"missing parallel result", `[` + call + `,{"type":"function_call","call_id":"native-b","name":"lookup","arguments":"{}"},` + result + `]`},
		{"orphan", `[` + result + `]`},
		{"duplicate result", `[` + call + `,` + result + `,` + result + `]`},
		{"duplicate call", `[` + call + `,` + call + `,` + result + `]`},
		{"summary only", `[{"type":"reasoning","summary":[{"type":"summary_text","text":"thought"}]}]`},
		{"empty array", `[]`},
		{"blank text", `" \n\t"`},
		{"empty content", `[{"role":"user","content":[]}]`},
		{"summary wrong type", `[{"role":"user","content":"task"},{"type":"reasoning","summary":"thought"}]`},
		{"summary invalid element", `[{"role":"user","content":"task"},{"type":"reasoning","summary":[{"type":"summary_text","text":5}]}]`},
		{"encrypted reasoning", `[{"role":"user","content":"task"},{"type":"reasoning","encrypted_content":"cipher"}]`},
		{"model media only", `[{"role":"user","content":"task"},{"role":"assistant","content":[{"type":"input_image","image_url":"data:image/png;base64,aW1hZ2U="}]}]`},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: " buffered", true: " stream"}[stream], func(t *testing.T) {
				body, err := json.Marshal(map[string]any{"model": "gemini-test", "input": json.RawMessage(tc.input), "stream": stream})
				require.NoError(t, err)
				upstream := &queuedHTTPUpstreamStub{}
				svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
				c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
				result, err := svc.ForwardAsResponsesGeili(context.Background(), c, geminiSignalTestAccount(), body)
				require.Error(t, err)
				require.Nil(t, result)
				require.Equal(t, http.StatusBadRequest, w.Code)
				require.Equal(t, "invalid_request_error", gjson.Get(w.Body.String(), "error.type").String())
				require.Empty(t, upstream.requestBodies)
			})
		}
	}
}

func TestGeminiOpenAIHistoryGeiliForwardContinuation(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			fields := `"input":[{"role":"user","content":"task"},{"role":"assistant","content":"partial"}]`
			path := "/v1/responses"
			if chat {
				fields = `"messages":[{"role":"user","content":"task"},{"role":"assistant","content":"partial"}]`
				path = "/v1/chat/completions"
			}
			body := []byte(`{"model":"gemini-test","stream":` + map[bool]string{false: "false", true: "true"}[stream] + `,` + fields + `}`)
			native := `{"candidates":[{"content":{"parts":[{"text":"continued"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2}}`
			if stream {
				native = "data: " + native + "\n\n"
			}
			upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(native))}}}
			svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
			c, w := newAntigravityCompatContext(http.MethodPost, path, body)
			result, err := svc.forwardOpenAINativeGeili(context.Background(), c, geminiSignalTestAccount(), body, chat)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 200, w.Code)
			require.Contains(t, w.Body.String(), "continued")
			require.Len(t, upstream.requestBodies, 1)
			require.Equal(t, geminiHistoryContinuationFixtureGeili, gjson.GetBytes(upstream.requestBodies[0], "contents.2.parts.0.text").String())
		}
	}
}

func TestGeminiOpenAIHistoryGeiliRejectsChatAndLegacy(t *testing.T) {
	for _, history := range []string{
		`[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"read","arguments":"{}"}}]}]`,
		`[{"role":"tool","tool_call_id":"a","content":"orphan"}]`,
		`[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"a","content":"one"},{"role":"tool","tool_call_id":"a","content":"duplicate"}]`,
	} {
		for _, chat := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				body, err := json.Marshal(map[string]any{"model": "gemini-test", "messages": json.RawMessage(history), "stream": stream})
				require.NoError(t, err)
				upstream := &queuedHTTPUpstreamStub{}
				svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
				c, w := newAntigravityCompatContext(http.MethodPost, "/v1/responses", body)
				result, err := svc.forwardOpenAINativeGeili(context.Background(), c, geminiSignalTestAccount(), body, chat)
				require.Error(t, err)
				require.Nil(t, result)
				require.Equal(t, http.StatusBadRequest, w.Code)
				require.Empty(t, upstream.requestBodies)
			}
		}
	}
}

func TestGeminiOpenAIHistoryGeiliAlreadyContinuedAndReadOnly(t *testing.T) {
	req := map[string]any{"input": []any{
		map[string]any{"role": "user", "content": "task"},
		map[string]any{"role": "assistant", "content": "partial"},
		map[string]any{"role": "user", "content": geminiHistoryContinuationFixtureGeili},
	}}
	before, err := json.Marshal(req)
	require.NoError(t, err)
	first, err := buildGeminiOpenAIHistoryGeili(req)
	require.NoError(t, err)
	second, err := buildGeminiOpenAIHistoryGeili(req)
	require.NoError(t, err)
	require.Equal(t, first, second)
	after, err := json.Marshal(req)
	require.NoError(t, err)
	require.Equal(t, before, after)
	encoded, err := json.Marshal(first)
	require.NoError(t, err)
	require.Len(t, gjson.GetBytes(encoded, "contents").Array(), 3)
	require.Equal(t, 1, strings.Count(string(encoded), geminiHistoryContinuationFixtureGeili))
}
