//go:build unit

package service

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func anthropicToolFixtureGeili(seed string, fragments []string, terminal bool) string {
	input := ""
	if seed != "" {
		input = `,"input":` + seed
	}
	parts := []string{geiliAnthropicStart, `{"type":"content_block_start","index":7,"content_block":{"type":"tool_use","id":"tool_1","name":"private_tool"` + input + `}}`}
	for _, fragment := range fragments {
		parts = append(parts, `{"type":"content_block_delta","index":7,"delta":{"type":"input_json_delta","partial_json":`+strconv.Quote(fragment)+`}}`)
	}
	parts = append(parts, `{"type":"content_block_stop","index":7}`, `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`)
	if terminal {
		parts = append(parts, geiliAnthropicStop)
	}
	return anthropicCompletionFixtureGeili(parts...)
}

func TestAnthropicStreamDiagnosticGeili_ToolArgumentAccumulation(t *testing.T) {
	cases := []struct {
		name, seed, want string
		fragments        []string
		invalid          bool
	}{
		{name: "no arguments", seed: `{}`, want: `{}`},
		{name: "empty delta retains seed", seed: `{}`, fragments: []string{"", ""}, want: `{}`},
		{name: "omitted seed", want: `{}`},
		{name: "omitted seed empty deltas", fragments: []string{"", ""}, want: `{}`},
		{name: "complete seed", seed: `{"secret":"PRIVATE_MARKER"}`, want: `{"secret":"PRIVATE_MARKER"}`},
		{name: "complete seed empty delta", seed: `{"secret":"PRIVATE_MARKER"}`, fragments: []string{""}, want: `{"secret":"PRIVATE_MARKER"}`},
		{name: "real stream replaces seed", seed: `{"seed":true}`, fragments: []string{`{"large":`, `9007199254740993}`}, want: `{"large":9007199254740993}`},
		{name: "empty object then empty fragment", seed: `{}`, fragments: []string{`{}`, ""}, want: `{}`},
		{name: "empty object then whitespace", seed: `{}`, fragments: []string{`{}`, "", " \n"}, want: "{} \n"},
		{name: "space inside split string", seed: `{}`, fragments: []string{`{"value":"first`, " ", `second"}`}, want: `{"value":"first second"}`},
		{name: "leading whitespace", seed: `{}`, fragments: []string{" \n", `{"value":true}`}, want: " \n{\"value\":true}"},
		{name: "invalid seeded JSON", seed: `"PRIVATE_MARKER`, invalid: true},
		{name: "invalid delta cannot fall back", seed: `{}`, fragments: []string{`{"secret":"PRIVATE_MARKER`}, invalid: true},
		{name: "streamed empty object then extra value", seed: `{}`, fragments: []string{`{}`, `{"extra":true}`}, invalid: true},
	}
	for _, tc := range cases {
		for _, terminal := range []bool{false, true} {
			t.Run(tc.name+"/terminal="+strconv.FormatBool(terminal), func(t *testing.T) {
				// An explicit malformed seed must remain malformed; JSON syntax in a
				// start event itself cannot carry incomplete JSON, so test it directly.
				if tc.name == "invalid seeded JSON" {
					arguments := &anthropicToolArgumentsGeili{seed: json.RawMessage(tc.seed)}
					require.False(t, json.Valid(arguments.input()))
					return
				}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				tracker, err := readAnthropicCompletionGeili(c, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(anthropicToolFixtureGeili(tc.seed, tc.fragments, terminal)))}, nil, "claude-opus-5", true, func(_ *apicompat.AnthropicStreamEvent, raw string) error {
					_, err := c.Writer.WriteString(raw)
					return err
				})
				if !tc.invalid {
					require.NoError(t, err)
					require.Equal(t, tc.want, string(tracker.response.Content[0].Input))
					return
				}
				require.Error(t, err)
				code, message, detail, ok := AnthropicStreamDiagnosticGeili(err)
				require.True(t, ok)
				require.Equal(t, "upstream_tool_arguments_invalid", code)
				require.Equal(t, "Upstream tool arguments were invalid", message)
				require.Equal(t, 7, detail["content_block_index"])
				if terminal {
					require.Equal(t, "none", detail["read_error_kind"])
				} else {
					require.Equal(t, "eof", detail["read_error_kind"])
				}
				require.NotContains(t, err.Error(), "PRIVATE_MARKER")
				encoded, marshalErr := json.Marshal(detail)
				require.NoError(t, marshalErr)
				require.NotContains(t, string(encoded), "PRIVATE_MARKER")
				require.NotContains(t, string(encoded), "private_tool")
				require.NotContains(t, string(encoded), "tool_1")
			})
		}
	}
}

func TestAnthropicStreamDiagnosticGeili_InterleavedToolsAndSeedFallback(t *testing.T) {
	for _, kind := range []string{"tool_use", "server_tool_use"} {
		t.Run(kind, func(t *testing.T) {
			tracker := &anthropicCompletionGeili{}
			require.NoError(t, tracker.observe(&apicompat.AnthropicStreamEvent{Type: "message_start", Message: &apicompat.AnthropicResponse{}}))
			for _, index := range []int{7, 2} {
				require.NoError(t, tracker.observe(&apicompat.AnthropicStreamEvent{Type: "content_block_start", Index: &index, ContentBlock: &apicompat.AnthropicContentBlock{Type: kind, Input: json.RawMessage(`{}`)}}))
			}
			for _, delta := range []struct {
				index    int
				fragment string
			}{{7, `{"a":`}, {2, `{}`}, {7, `true}`}, {2, ""}, {2, " "}} {
				index := delta.index
				require.NoError(t, tracker.observe(&apicompat.AnthropicStreamEvent{Type: "content_block_delta", Index: &index, Delta: &apicompat.AnthropicDelta{Type: "input_json_delta", PartialJSON: delta.fragment}}))
			}
			for _, index := range []int{2, 7} {
				require.NoError(t, tracker.observe(&apicompat.AnthropicStreamEvent{Type: "content_block_stop", Index: &index}))
			}
			require.NoError(t, tracker.observe(&apicompat.AnthropicStreamEvent{Type: "message_delta", Delta: &apicompat.AnthropicDelta{StopReason: "tool_use"}}))
			require.NoError(t, tracker.complete())
			require.Equal(t, `{"a":true}`, string(tracker.response.Content[0].Input))
			require.Equal(t, `{} `, string(tracker.response.Content[1].Input))
			tracker.response.Content[1].Input = json.RawMessage(`{"bad":`)
			err := tracker.complete()
			require.Error(t, err)
			_, _, detail, ok := AnthropicStreamDiagnosticGeili(err)
			require.True(t, ok)
			require.Equal(t, 2, detail["content_block_index"])
		})
	}
}

func TestAnthropicStreamDiagnosticGeili_PublicForwardNoReplayAndOpsDetail(t *testing.T) {
	for _, account := range []*Account{newAnthropicOAuthAccountForPartialUsageTest(), newAnthropicAPIKeyAccountForTest()} {
		for _, invalid := range []bool{false, true} {
			t.Run(account.Type+"/invalid="+strconv.FormatBool(invalid), func(t *testing.T) {
				c, rec := guardContextGeili("/v1/messages")
				fragments := []string{`{}`, "", " "}
				if invalid {
					fragments = []string{`{"secret":"PRIVATE_MARKER`}
				}
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(anthropicToolFixtureGeili(`{}`, fragments, true)))}}
				svc := newForwardPartialUsageServiceForTest(upstream)
				body := []byte(`{"model":"claude-opus-5","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
				parsed, parseErr := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, parseErr)
				result, err := svc.Forward(c.Request.Context(), c, account, parsed)
				if !invalid {
					require.NoError(t, err)
					require.Contains(t, rec.Body.String(), `"type":"message_stop"`)
					require.NotContains(t, rec.Body.String(), "event: error")
					return
				}
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover), "a delivered tool start cannot authorize replay")
				require.NotNil(t, result, "partial usage is retained for existing settlement")
				require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error"))
				require.NotContains(t, rec.Body.String(), `"type":"message_stop"`)
				failureFrame := strings.Split(rec.Body.String(), "event: error")[1]
				require.Contains(t, failureFrame, "Upstream tool arguments were invalid")
				require.NotContains(t, failureFrame, "PRIVATE_MARKER")
				marked, exists := c.Get(OpsStreamErrorKey)
				require.True(t, exists)
				require.Equal(t, "upstream_tool_arguments_invalid", marked.(OpsStreamError).Code)
				value, exists := c.Get(OpsUpstreamErrorsKey)
				require.True(t, exists)
				events := value.([]*OpsUpstreamErrorEvent)
				require.Len(t, events, 1)
				require.Equal(t, "stream_protocol_error", events[0].Kind)
				require.Zero(t, events[0].UpstreamStatusCode, "no invented upstream HTTP 502")
				require.NotContains(t, events[0].Detail, "PRIVATE_MARKER")
				require.Contains(t, events[0].Detail, `"content_block_index":7`)
			})
		}
	}
}

func TestAnthropicStreamDiagnosticGeili_GuardAcrossProtocols(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"} {
		t.Run(path, func(t *testing.T) {
			c, rec := guardContextGeili(path)
			finish := BeginTextForwardGuard(c, true)
			c.Header("Content-Type", "text/event-stream")
			_, err := c.Writer.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"visible\"}}]}\n\n")
			require.NoError(t, err)
			err = finish(newAnthropicToolArgumentsDiagnosticGeili(9, apicompat.AnthropicContentBlock{Type: "tool_use", Name: "PRIVATE_TOOL", Input: json.RawMessage(`{"private":"PRIVATE_MARKER`)}, nil, io.EOF))
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
			require.Equal(t, 1, strings.Count(rec.Body.String(), "Upstream tool arguments were invalid"))
			require.Contains(t, rec.Body.String(), "visible")
			require.NotContains(t, rec.Body.String(), "PRIVATE_MARKER")
			require.NotContains(t, rec.Body.String(), "PRIVATE_TOOL")
			if path == "/v1/messages" {
				require.Contains(t, rec.Body.String(), `"type":"api_error"`)
			} else {
				require.Contains(t, rec.Body.String(), `"code":"upstream_tool_arguments_invalid"`)
			}
			marked, exists := c.Get(OpsStreamErrorKey)
			require.True(t, exists)
			require.Equal(t, "upstream_tool_arguments_invalid", marked.(OpsStreamError).Code)
			require.True(t, IsResponseCommitted(c))
		})
	}
}

func TestAnthropicStreamDiagnosticGeili_MixedToolsPublicForward(t *testing.T) {
	c, rec := guardContextGeili("/v1/messages")
	body := anthropicCompletionFixtureGeili(
		geiliAnthropicStart,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool_ok","name":"valid_tool","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"content_block_start","index":9,"content_block":{"type":"tool_use","id":"tool_bad","name":"invalid_tool","input":{}}}`,
		`{"type":"content_block_delta","index":9,"delta":{"type":"input_json_delta","partial_json":"{\"secret\":\"PRIVATE_MARKER"}}`,
		`{"type":"content_block_stop","index":9}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`,
		geiliAnthropicStop,
	)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}}
	svc := newForwardPartialUsageServiceForTest(upstream)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-opus-5","stream":true,"messages":[{"role":"user","content":"hello"}]}`)), PlatformAnthropic)
	require.NoError(t, err)
	result, err := svc.Forward(c.Request.Context(), c, newAnthropicAPIKeyAccountForTest(), parsed)
	require.Error(t, err)
	require.NotNil(t, result)
	_, _, detail, ok := AnthropicStreamDiagnosticGeili(err)
	require.True(t, ok)
	require.Equal(t, 9, detail["content_block_index"])
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.Contains(t, rec.Body.String(), "tool_ok")
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error"))
	require.NotContains(t, rec.Body.String(), `"type":"message_stop"`)
}
