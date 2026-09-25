//go:build unit

package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeiliToolProtocolWireGuardCoversEveryChatFallback(t *testing.T) {
	for _, tc := range []struct {
		name, body string
	}{
		{"responses", `{"model":"public","input":"hello","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"reasoning":{"effort":"high"}}`},
		{"chat", `{"model":"public","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"reasoning_effort":"high"}`},
		{"messages", `{"model":"public","max_tokens":100,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"output_config":{"effort":"high"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := geiliToolProtocolAccounts("public")[0]
			upstream := &httpUpstreamRecorder{}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+tc.name, bytes.NewBufferString(tc.body))
			var err error
			switch tc.name {
			case "responses":
				_, err = svc.forwardResponsesViaRawChatCompletions(context.Background(), c, &account, []byte(tc.body))
			case "chat":
				_, err = svc.forwardAsRawChatCompletions(context.Background(), c, &account, []byte(tc.body), "")
			case "messages":
				_, err = svc.forwardAnthropicViaRawChatCompletions(context.Background(), c, &account, []byte(tc.body), "")
			}
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Equal(t, GatewayFailureReason("responses_required"), failover.Reason)
			require.True(t, failover.ShouldRetryNextAccount())
			require.False(t, failover.RetryableOnSameAccount)
			require.False(t, c.Writer.Written(), "next account must still be able to answer")
			require.Nil(t, upstream.lastReq, "unsupported request must never reach upstream")
		})
	}
}
