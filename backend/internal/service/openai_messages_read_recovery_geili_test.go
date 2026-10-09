//go:build unit

package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Model the response-body boundary, after HTTP headers succeeded. A transport
// error returned by Do() exercises a different recovery path.
type messagesReadFaultGeili struct {
	io.Reader
	err error
}

func (r *messagesReadFaultGeili) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		return n, r.err
	}
	return n, err
}

func (*messagesReadFaultGeili) Close() error { return nil }

func messagesReadFrameGeili(payload string) string { return "data: " + payload + "\n\n" }

func messagesReadCreatedGeili() string {
	return messagesReadFrameGeili(`{"type":"response.created","response":{"id":"private-A","model":"gpt-6-astra","usage":{"input_tokens":11,"output_tokens":0}}}`)
}

func messagesReadForwardGeili(t *testing.T, model string, stream bool, data string, fault error, canceled bool) (*OpenAIForwardResult, error, *gin.Context, string) {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"model":%q,"stream":%t,"max_tokens":32,"messages":[{"role":"user","content":"synthetic response-body read recovery"}]}`, model, stream))
	c, rec := guardContextGeili("/v1/messages")
	c.Request.Body = io.NopCloser(strings.NewReader(string(body)))
	if canceled {
		ctx, cancel := context.WithCancel(c.Request.Context())
		cancel()
		c.Request = c.Request.WithContext(ctx)
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: http.StatusOK,
		Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"private-A"}},
		Body:   &messagesReadFaultGeili{Reader: strings.NewReader(data), err: fault}}}}
	account := newOpenAIRejectedFieldTestAccount()
	account.Extra["openai_responses_mode"] = "force_responses"
	result, err := newOpenAIRejectedFieldTestService(upstream).ForwardAsAnthropic(c.Request.Context(), c, account, body, "", "")
	require.Len(t, upstream.requests, 1, "service attempt never starts an internal second generation")
	require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
	require.True(t, gjson.GetBytes(upstream.bodies[0], "stream").Bool(), "a JSON Messages caller still consumes upstream SSE")
	return result, err, c, rec.Body.String()
}

func TestMessagesReadRecoveryGeiliPreOutputHTTP2Reset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		for _, prelude := range []struct{ name, data string }{
			{"headers-only", ""},
			{"created-with-metering", messagesReadCreatedGeili()},
			{"ping", ": supplier heartbeat\n\nevent: ping\ndata: {\"type\":\"ping\"}\n\n" + messagesReadCreatedGeili()},
		} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, prelude.name), func(t *testing.T) {
				fault := errors.New("stream error: stream ID 3; INTERNAL_ERROR; received from peer")
				result, err, c, clientBody := messagesReadForwardGeili(t, "gpt-6-astra", stream, prelude.data, fault, false)
				require.Error(t, err)
				require.Nil(t, result, "an abandoned pre-output attempt must not produce a receipt, even with prelude usage")
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.True(t, failover.SafeToFailoverAfterWrite)
				require.True(t, failover.ShouldRetryNextAccount())
				require.Equal(t, OpenAIUpstreamHTTP2StreamErrorCode, gjson.GetBytes(failover.ResponseBody, "error.code").String())
				require.NotContains(t, clientBody, "private-A")
				require.NotContains(t, clientBody, "event: error", "the next account owns the response")
				require.Empty(t, c.Writer.Header().Get("X-Request-ID"))
				require.Less(t, GeiliTextForwardWrittenSize(c), 1, "neutral heartbeats cannot prevent handler failover")
			})
		}
	}
	// Cover the scanner's direct loop as well as Astra's timed select loop.
	t.Run("ordinary-model-direct-loop", func(t *testing.T) {
		result, err, _, _ := messagesReadForwardGeili(t, "gpt-4.1-mini", true, "", errors.New("stream error: stream ID 7; INTERNAL_ERROR; received from peer"), false)
		require.Nil(t, result)
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover)
		require.True(t, failover.SafeToFailoverAfterWrite)
	})
}

func TestMessagesReadRecoveryGeiliDeliveredPartialUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"gpt-6-astra", "gpt-4.1-mini"} {
		for _, partial := range []struct{ name, data, visible string }{
			{"text", messagesReadFrameGeili(`{"type":"response.output_text.delta","item_id":"msg-A","output_index":0,"content_index":0,"delta":"partial visible text"}`), "partial visible text"},
			{"thinking", messagesReadFrameGeili(`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs-A","summary":[]}}`) + messagesReadFrameGeili(`{"type":"response.reasoning_summary_text.delta","item_id":"rs-A","output_index":0,"summary_index":0,"delta":"partial visible thinking"}`), "partial visible thinking"},
			{"tool", messagesReadFrameGeili(`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc-A","call_id":"call-A","name":"lookup","arguments":""}}`) +
				messagesReadFrameGeili(`{"type":"response.function_call_arguments.delta","item_id":"fc-A","output_index":0,"delta":"{\"nonce\":9007199254740993}"}`), "9007199254740993"},
		} {
			t.Run(model+"/"+partial.name, func(t *testing.T) {
				data := messagesReadCreatedGeili() + partial.data + messagesReadFrameGeili(`{"type":"response.in_progress","response":{"id":"private-A","usage":{"input_tokens":11,"output_tokens":5}}}`)
				result, err, _, clientBody := messagesReadForwardGeili(t, model, true, data, errors.New("stream error: stream ID 3; INTERNAL_ERROR; received from peer"), false)
				require.Error(t, err)
				require.NotNil(t, result, "delivered output retains genuine failed-attempt usage")
				require.Equal(t, 11, result.Usage.InputTokens)
				require.Equal(t, 5, result.Usage.OutputTokens)
				require.Equal(t, "private-A", result.RequestID)
				require.Equal(t, model, result.Model)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover) && failover.SafeToFailoverAfterWrite)
				require.Contains(t, clientBody, partial.visible)
				require.Contains(t, clientBody, "event: error")
				require.NotContains(t, clientBody, "event: message_stop", "a transport reset cannot fabricate successful completion")
			})
		}
	}
}

func TestMessagesReadRecoveryGeiliBuiltinExecutionForbidsReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		for _, eventOnly := range []bool{false, true} {
			for _, pending := range []bool{false, true} {
				t.Run(fmt.Sprintf("stream=%t/event-only=%t/pending=%t", stream, eventOnly, pending), func(t *testing.T) {
					payload := `{"item_id":"operation-A","output_index":0}`
					if !eventOnly {
						payload = `{"type":"response.code_interpreter_call.in_progress","item_id":"operation-A","output_index":0}`
					}
					frame := "event: response.code_interpreter_call.in_progress\ndata: " + payload
					if !pending {
						frame += "\n\n"
					}
					result, err, c, clientBody := messagesReadForwardGeili(t, "gpt-6-astra", stream, messagesReadCreatedGeili()+frame, errors.New("stream error: stream ID 3; INTERNAL_ERROR; received from peer"), false)
					require.Error(t, err)
					if stream {
						require.NotNil(t, result, "executed supplier operations retain genuine observed metering")
						require.Equal(t, 11, result.Usage.InputTokens)
					} else {
						require.Nil(t, result, "buffered conversion preserves its no-delivery accounting contract")
					}
					var failover *UpstreamFailoverError
					require.False(t, errors.As(err, &failover) && (failover.SafeToFailoverAfterWrite || failover.ShouldRetryNextAccount()))
					require.False(t, BeginRequestRecovery(c.Request.Context()), "the no-replay verdict must survive composite-group routing")
					require.NotContains(t, clientBody, "operation-A", "a discarded supplier operation is not emitted as client content")
				})
			}
		}
	}
}

func TestMessagesReadRecoveryGeiliCancellationAndSizeLimitsStayFinal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		for _, local := range []struct {
			name     string
			err      error
			canceled bool
		}{
			{"body-read-canceled", context.Canceled, false},
			{"caller-canceled", errors.New("stream error: stream ID 3; INTERNAL_ERROR; received from peer"), true},
			{"scanner-too-long", bufio.ErrTooLong, false},
		} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, local.name), func(t *testing.T) {
				result, err, _, _ := messagesReadForwardGeili(t, "gpt-6-astra", stream, "", local.err, local.canceled)
				require.Error(t, err)
				require.Nil(t, result, "a failed attempt without delivery has no accounting result")
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover) && failover.SafeToFailoverAfterWrite)
			})
		}
	}
}

func TestMessagesReadRecoveryGeiliBuiltinWithoutDataForbidsReplay(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, pending := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/pending=%t", stream, pending), func(t *testing.T) {
				frame := "event: response.code_interpreter_call.in_progress"
				if !pending {
					frame += "\n\n"
				}
				_, err, c, body := messagesReadForwardGeili(t, "gpt-6-astra", stream, messagesReadCreatedGeili()+frame,
					errors.New("stream error: stream ID 3; INTERNAL_ERROR; received from peer"), false)
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover) && (failover.SafeToFailoverAfterWrite || failover.ShouldRetryNextAccount()))
				require.False(t, BeginRequestRecovery(c.Request.Context()))
				require.NotContains(t, body, "event: message_stop")
			})
		}
	}
}
