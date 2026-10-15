package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAnthropicThinkingEffortGeili(t *testing.T) {
	for _, tc := range []struct {
		name, model, body, want string
	}{
		{"move", "claude-opus-5", `{"thinking":{"type":"adaptive","effort":"high"}}`, `{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`},
		{"explicit wins", "claude-opus-5", `{"thinking":{"type":"adaptive","effort":"high"},"output_config":{"effort":"low","format":{"type":"json_schema"}}}`, `{"thinking":{"type":"adaptive"},"output_config":{"effort":"low","format":{"type":"json_schema"}}}`},
		{"keep format", "claude-opus-5-5", `{"thinking":{"type":"adaptive","effort":"xhigh","display":"omitted"},"output_config":{"format":{"type":"json_schema"}}}`, `{"thinking":{"type":"adaptive","display":"omitted"},"output_config":{"format":{"type":"json_schema"},"effort":"xhigh"}}`},
		{"unknown value stays invalid", "claude-opus-5", `{"thinking":{"type":"adaptive","effort":"banana"}}`, `{"thinking":{"type":"adaptive"},"output_config":{"effort":"banana"}}`},
		{"valid request", "claude-opus-5", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`, ""},
		{"enabled unchanged", "claude-opus-5", `{"thinking":{"type":"enabled","effort":"high"}}`, ""},
		{"third party unchanged", "kimi-k3", `{"thinking":{"type":"adaptive","effort":"high"}}`, ""},
		{"non string unchanged", "claude-opus-5", `{"thinking":{"type":"adaptive","effort":42}}`, ""},
		{"null unchanged", "claude-opus-5", `{"thinking":{"type":"adaptive","effort":null}}`, ""},
		{"malformed output unchanged", "claude-opus-5", `{"thinking":{"type":"adaptive","effort":"high"},"output_config":[]}`, ""},
		{"null output unchanged", "claude-opus-5", `{"thinking":{"type":"adaptive","effort":"high"},"output_config":null}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, changed := normalizeAnthropicThinkingEffortGeili([]byte(tc.body), tc.model)
			if tc.want == "" {
				require.False(t, changed)
				require.Equal(t, tc.body, string(out))
				return
			}
			require.True(t, changed)
			require.JSONEq(t, tc.want, string(out))
			again, changed := normalizeAnthropicThinkingEffortGeili(out, tc.model)
			require.False(t, changed)
			require.Equal(t, out, again)
		})
	}
}

// Exercise the public forwarding boundary and the actual wire body, including
// mapped aliases, streaming, and the separate count_tokens branch. Tool inputs
// must retain raw large integers, and parsed effort must follow the wire body.
func TestAnthropicThinkingEffortGeiliForward(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, mode := range []string{"messages", "stream", "count_tokens"} {
			name := mode + "/regular"
			if passthrough {
				name = mode + "/passthrough"
			}
			t.Run(name, func(t *testing.T) {
				stream := mode == "stream"
				body := `{"model":"public-alias","max_tokens":1024,"thinking":{"type":"adaptive","effort":"high"},"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"lookup","input":{"id":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"ok"}]}]}`
				if stream {
					body = strings.TrimSuffix(body, "}") + `,"stream":true}`
				}
				parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), PlatformAnthropic)
				require.NoError(t, err)
				payload := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`
				contentType := "application/json"
				if stream {
					contentType = "text/event-stream"
					payload = strings.Join([]string{
						`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
						`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
						`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
						`data: {"type":"content_block_stop","index":0}`,
						`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
						`data: {"type":"message_stop"}`,
						"",
					}, "\n\n")
				} else if mode == "count_tokens" {
					payload = `{"input_tokens":3}`
				}
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
					StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(payload)),
				}}
				cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
				svc := &GatewayService{cfg: cfg, httpUpstream: upstream, responseHeaderFilter: compileResponseHeaderFilter(cfg), rateLimitService: &RateLimitService{}, deferredService: &DeferredService{}}
				account := newAnthropicAPIKeyAccountForTest()
				account.Extra["anthropic_passthrough"] = passthrough
				account.Credentials["model_mapping"] = map[string]any{"public-alias": "claude-opus-5"}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				path := "/v1/messages"
				if mode == "count_tokens" {
					path += "/count_tokens"
				}
				c.Request = httptest.NewRequest(http.MethodPost, path, nil)
				c.Request.Header.Set("User-Agent", "node")
				if mode == "count_tokens" {
					err = svc.ForwardCountTokens(context.Background(), c, account, parsed)
				} else {
					_, err = svc.Forward(context.Background(), c, account, parsed)
				}
				require.NoError(t, err)
				require.Equal(t, 200, rec.Code)
				if passthrough {
					// Strict passthrough sends the client's bytes; the provider validates the field.
					require.Equal(t, body, string(upstream.lastBody))
					return
				}
				require.False(t, gjson.GetBytes(upstream.lastBody, "thinking.effort").Exists())
				require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "output_config.effort").String())
				require.Equal(t, "adaptive", gjson.GetBytes(upstream.lastBody, "thinking.type").String())
				require.Equal(t, "claude-opus-5", gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, "9007199254740993", gjson.GetBytes(upstream.lastBody, "messages.1.content.0.input.id").Raw)
				require.Equal(t, "high", parsed.OutputEffort)
			})
		}
	}
}
