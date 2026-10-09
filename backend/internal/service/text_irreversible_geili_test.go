//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func irreversibleOpenAIFrameGeili(payload string, eofFrame bool) string {
	frame := "data: " + payload
	if !eofFrame {
		frame += "\n\n"
	}
	return frame
}

// Public synchronous calls keep supplier events private, but the absence of
// client bytes cannot authorize replay after a supplier has started a built-in
// operation. The same verdict must survive the next composite-key group.
func TestTextIrreversibleGeiliPublicSynchronousOpenAIForwards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/responses", "/v1/messages", "/v1/chat/completions"} {
		for _, eofFrame := range []bool{false, true} {
			for _, operation := range []string{"code_interpreter_call", "web_search_call", "computer_call", "image_generation_call", "client_function_call"} {
				t.Run(path+"/"+operation+"/eof="+map[bool]string{false: "false", true: "true"}[eofFrame], func(t *testing.T) {
					body := []byte(`{"model":"gpt-6-astra","stream":false,"input":"hello"}`)
					if path != "/v1/responses" {
						body = []byte(`{"model":"gpt-6-astra","stream":false,"messages":[{"role":"user","content":"hello"}]}`)
					}
					c, rec := guardContextGeili(path)
					root := WithRequestRecovery(context.Background(), time.Minute)
					firstGroup := context.WithValue(root, ctxkey.Group, &Group{ID: 41, Platform: PlatformOpenAI})
					c.Request = c.Request.WithContext(firstGroup)
					stream := irreversibleOpenAIFrameGeili(`{"type":"response.created","response":{"id":"private-A","model":"gpt-6-astra","usage":{"input_tokens":11,"output_tokens":0}}}`, false)
					payload := `{"type":"response.` + operation + `.in_progress","item_id":"operation-A","output_index":0}`
					if operation == "client_function_call" {
						payload = `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_A","call_id":"call_A","name":"run","arguments":"{\"number\":9007199254740993}"}}`
					}
					stream += irreversibleOpenAIFrameGeili(payload, eofFrame)
					upstream := &httpUpstreamRecorder{responses: []*http.Response{newGeiliIrreversibleUpstreamResponse("text/event-stream", stream)}}
					s := newOpenAIRejectedFieldTestService(upstream)
					account := newOpenAIRejectedFieldTestAccount()
					var result *OpenAIForwardResult
					var err error
					switch path {
					case "/v1/messages":
						result, err = s.ForwardAsAnthropic(firstGroup, c, account, body, "", "")
					case "/v1/chat/completions":
						result, err = s.ForwardAsChatCompletions(firstGroup, c, account, body, "", "")
					default:
						result, err = s.Forward(firstGroup, c, account, body)
					}
					require.Error(t, err)
					require.Nil(t, result)
					require.Len(t, upstream.requests, 1)
					require.Empty(t, rec.Body.String(), "supplier progress stays private for a synchronous caller")
					require.Empty(t, rec.Header().Get("X-Request-ID"))
					var failover *UpstreamFailoverError
					replay := errors.As(err, &failover) && failover.ShouldRetryNextAccount()
					nextGroup := WithRequestRecovery(context.WithValue(root, ctxkey.Group, &Group{ID: 42, Platform: PlatformOpenAI}), time.Minute)
					if operation == "client_function_call" {
						require.True(t, replay, "a buffered client function call has not executed at the supplier")
						require.True(t, BeginRequestRecovery(nextGroup))
					} else {
						require.False(t, replay, "a second generation must not repeat an already started supplier operation")
						require.False(t, BeginRequestRecovery(nextGroup), "changing group cannot reset the no-replay decision")
					}
				})
			}
		}
	}
}

func newGeiliIrreversibleUpstreamResponse(contentType, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}, "X-Request-Id": {"private-A"}}, Body: io.NopCloser(strings.NewReader(body))}
}

// Compatible event-named SSE supplies the kind in event:, with no duplicate
// type member in data. It has the same execution boundary at both delimiters
// and EOF as an event carrying its own type member.
func TestTextIrreversibleGeiliEventNamedBuiltinCannotReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, eofFrame := range []bool{false, true} {
			t.Run("passthrough="+map[bool]string{false: "false", true: "true"}[passthrough]+"/eof="+map[bool]string{false: "false", true: "true"}[eofFrame], func(t *testing.T) {
				body := []byte(`{"model":"gpt-6-astra","stream":false,"input":"hello"}`)
				c, rec := guardContextGeili("/v1/responses")
				stream := "event: response.code_interpreter_call.in_progress\n" + irreversibleOpenAIFrameGeili(`{"item_id":"operation-A","output_index":0}`, eofFrame)
				upstream := &httpUpstreamRecorder{responses: []*http.Response{newGeiliIrreversibleUpstreamResponse("text/event-stream", stream)}}
				account := newOpenAIRejectedFieldTestAccount()
				if passthrough {
					account.Extra["openai_passthrough"] = true
				}
				result, err := newOpenAIRejectedFieldTestService(upstream).Forward(c.Request.Context(), c, account, body)
				require.Error(t, err)
				require.Nil(t, result)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover) && failover.ShouldRetryNextAccount())
				require.False(t, BeginRequestRecovery(c.Request.Context()))
				require.Empty(t, rec.Body.String())
				require.Len(t, upstream.requests, 1)
			})
		}
	}
}

func TestTextIrreversibleGeiliStreamingEventNamedBuiltinCannotReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, eofFrame := range []bool{false, true} {
		t.Run("eof="+map[bool]string{false: "false", true: "true"}[eofFrame], func(t *testing.T) {
			body := []byte(`{"model":"gpt-6-astra","stream":true,"input":"hello"}`)
			c, _ := guardContextGeili("/v1/responses")
			stream := "event: response.code_interpreter_call.in_progress\n" + irreversibleOpenAIFrameGeili(`{"item_id":"operation-A","output_index":0}`, eofFrame)
			upstream := &httpUpstreamRecorder{responses: []*http.Response{newGeiliIrreversibleUpstreamResponse("text/event-stream", stream)}}
			_, err := newOpenAIRejectedFieldTestService(upstream).Forward(c.Request.Context(), c, newOpenAIRejectedFieldTestAccount(), body)
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover) && failover.ShouldRetryNextAccount())
			require.False(t, BeginRequestRecovery(c.Request.Context()))
			require.Len(t, upstream.requests, 1)
		})
	}
}

func TestTextIrreversibleGeiliPublicSynchronousAnthropicConversions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/responses", "/v1/chat/completions"} {
		for _, eofFrame := range []bool{false, true} {
			for _, operation := range []string{"server_tool_use", "tool_use"} {
				t.Run(path+"/"+operation+"/eof="+map[bool]string{false: "false", true: "true"}[eofFrame], func(t *testing.T) {
					body := []byte(`{"model":"claude-opus-4","stream":false,"input":"hello"}`)
					if path == "/v1/chat/completions" {
						body = []byte(`{"model":"claude-opus-4","stream":false,"messages":[{"role":"user","content":"hello"}]}`)
					}
					c, rec := guardContextGeili(path)
					root := WithRequestRecovery(context.Background(), time.Minute)
					c.Request = c.Request.WithContext(root)
					stream := irreversibleOpenAIFrameGeili(`{"type":"message_start","message":{"id":"private-A","type":"message","role":"assistant","content":[],"model":"claude-opus-4","usage":{"input_tokens":11}}}`, false)
					stream += irreversibleOpenAIFrameGeili(`{"type":"content_block_start","index":0,"content_block":{"type":"`+operation+`","id":"tool_A","name":"run","input":{"number":9007199254740993}}}`, eofFrame)
					upstream := &httpUpstreamRecorder{responses: []*http.Response{newGeiliIrreversibleUpstreamResponse("text/event-stream", stream)}}
					s := &GatewayService{cfg: newOpenAIRejectedFieldTestService(upstream).cfg, httpUpstream: upstream}
					account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "test-token", "base_url": "https://provider.example"}}
					var result *ForwardResult
					var err error
					if path == "/v1/responses" {
						result, err = s.ForwardAsResponses(root, c, account, body, nil)
					} else {
						result, err = s.ForwardAsChatCompletions(root, c, account, body, nil)
					}
					require.Error(t, err)
					require.Nil(t, result)
					require.Len(t, upstream.requests, 1)
					require.Empty(t, rec.Body.String())
					var failover *UpstreamFailoverError
					replay := errors.As(err, &failover) && failover.ShouldRetryNextAccount()
					if operation == "server_tool_use" {
						require.False(t, replay)
						require.False(t, BeginRequestRecovery(context.WithValue(root, ctxkey.Group, &Group{ID: 42})))
					} else {
						require.True(t, replay)
						require.True(t, BeginRequestRecovery(root))
					}
				})
			}
		}
	}
}

func TestTextIrreversibleGeiliPublicHardErrorsPreserveProtocolDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, wire := range []string{"json", "sse"} {
			t.Run(wire+"/passthrough="+map[bool]string{false: "false", true: "true"}[passthrough], func(t *testing.T) {
				body := []byte(`{"model":"gpt-6-astra","stream":false,"input":"hello"}`)
				c, rec := guardContextGeili("/v1/responses")
				errorBlock := `{"type":"invalid_request_error","code":"image_count_exceeded","param":"input","message":"Exceeded maximum number of images (50) allowed in the request."}`
				payload, contentType := `{"error":`+errorBlock+`}`, "application/json"
				if wire == "sse" {
					payload, contentType = irreversibleOpenAIFrameGeili(`{"type":"response.failed","response":{"id":"private-A","object":"response","model":"gpt-6-astra","status":"failed","output":[],"error":`+errorBlock+`,"usage":{"input_tokens":11,"output_tokens":0}}}`, false), "text/event-stream"
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{newGeiliIrreversibleUpstreamResponse(contentType, payload)}}
				account := newOpenAIRejectedFieldTestAccount()
				if passthrough {
					account.Extra["openai_passthrough"] = true
				}
				result, err := newOpenAIRejectedFieldTestService(upstream).Forward(c.Request.Context(), c, account, body)
				require.Error(t, err)
				require.Nil(t, result)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover), "input constraints never start a recovery generation")
				require.Len(t, upstream.requests, 1)
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
				require.Equal(t, "image_count_exceeded", gjson.Get(rec.Body.String(), "error.code").String())
				require.Equal(t, "input", gjson.Get(rec.Body.String(), "error.param").String())
				require.Equal(t, "Exceeded maximum number of images (50) allowed in the request.", gjson.Get(rec.Body.String(), "error.message").String())
				require.NotContains(t, rec.Body.String(), "private-A")
				require.Empty(t, rec.Header().Get("X-Request-ID"))
			})
		}
	}
}
