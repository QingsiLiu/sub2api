//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesPartialUsageGeiliRealForward(t *testing.T) {
	gin.SetMode(gin.TestMode)
	created := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"model\":\"gpt-6-astra\",\"service_tier\":\"default\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":0}}}\n\n"
	delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_partial\",\"output_index\":0,\"content_index\":0,\"delta\":\"visible\"}\n\n"
	complete := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_partial\",\"model\":\"gpt-6-astra\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"visible\"}]}],\"usage\":{\"input_tokens\":1000,\"output_tokens\":5}}}\n\n"
	for _, scenario := range []string{"text", "thinking", "tool", "prelude-only", "unmetered", "cyber", "write-disconnect", "cancel-disconnect"} {
		t.Run(scenario, func(t *testing.T) {
			body := []byte(`{"model":"gpt-6-astra","stream":true,"input":"hello"}`)
			c := newOpenAIRejectedFieldTestContext(body)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			stream := created + delta
			switch scenario {
			case "thinking":
				stream = created + "event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"item_id\":\"rs_partial\",\"output_index\":0,\"summary_index\":0,\"delta\":\"visible\"}\n\n"
			case "tool":
				stream = created + "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"fc_partial\",\"type\":\"function_call\",\"name\":\"run\",\"call_id\":\"call_partial\",\"arguments\":\"\"}}\n\n"
				stream += "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"fc_partial\",\"output_index\":0,\"delta\":\"{\"}\n\n"
			case "prelude-only":
				stream = created
			case "unmetered":
				stream = delta
			case "cyber":
				stream += "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_partial\",\"status\":\"failed\",\"error\":{\"code\":\"cyber_policy\",\"message\":\"blocked\"},\"usage\":{\"input_tokens\":1000,\"output_tokens\":5}}}\n\n"
			case "write-disconnect", "cancel-disconnect":
				if scenario == "write-disconnect" {
					c.Writer = &anthropicDisconnectWriterGeili{ResponseWriter: c.Writer}
					stream += complete
				} else {
					c.Writer = &responsesCancelWriterGeili{ResponseWriter: c.Writer, cancel: cancel}
				}
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"attempt-partial"}}, Body: io.NopCloser(strings.NewReader(stream))}}}
			result, err := newOpenAIRejectedFieldTestService(upstream).Forward(ctx, c, newOpenAIRejectedFieldTestAccount(), body)
			require.Len(t, upstream.requests, 1, "one admitted request must not restart delivered content")
			if scenario == "prelude-only" || scenario == "unmetered" || scenario == "cyber" {
				require.Error(t, err)
				require.Nil(t, result, "unused prelude, unmetered failure and cyber each retain their accounting contract")
				if scenario == "prelude-only" {
					var failover *UpstreamFailoverError
					require.ErrorAs(t, err, &failover)
					require.True(t, failover.SafeToFailoverAfterWrite)
				}
				return
			}
			require.NotNil(t, result, "delivered partial output retains observed metering")
			require.Equal(t, 1000, result.Usage.InputTokens)
			require.Equal(t, "attempt-partial", result.RequestID)
			require.Equal(t, "resp_partial", result.ResponseID)
			require.Equal(t, "gpt-6-astra", result.Model)
			require.Equal(t, "gpt-6-astra", result.BillingModel)
			require.Equal(t, "gpt-6-astra", result.UpstreamModel)
			require.Equal(t, "gpt-6-astra", result.UpstreamResponseModel)
			require.Equal(t, "/v1/responses", result.UpstreamEndpoint)
			if scenario == "write-disconnect" {
				require.NoError(t, err, "a completed bounded usage drain is successful after the downstream writer closes")
				require.Equal(t, 5, result.Usage.OutputTokens)
				require.True(t, result.ClientDisconnect)
				require.Equal(t, "response.completed", result.UpstreamTerminalEvent)
			} else {
				require.Error(t, err)
				require.Equal(t, scenario == "cancel-disconnect", result.ClientDisconnect)
			}
		})
	}
}

type responsesCancelWriterGeili struct {
	gin.ResponseWriter
	cancel context.CancelFunc
}

func (w *responsesCancelWriterGeili) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if strings.Contains(string(p), "visible") {
		w.cancel()
	}
	return n, err
}
