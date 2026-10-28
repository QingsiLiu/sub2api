//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiOpenAINativeSameAccountRetriesGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, chat := range []bool{false, true} {
		for _, retries := range []int{0, 1} {
			t.Run(fmt.Sprintf("chat=%t/retries=%d", chat, retries), func(t *testing.T) {
				svc, upstream, _ := newGeminiSingleCheckService(geminiSequenceUpstreamResponse{status: http.StatusServiceUnavailable, body: geminiRetryTest503Body})
				svc.cfg.Gateway.MaxSameAccountRetriesGemini = retries
				body := []byte(`{"model":"gemini-3.5-flash","input":"hello"}`)
				path := "/v1/responses"
				if chat {
					path = "/v1/chat/completions"
					body = []byte(`{"model":"gemini-3.5-flash","messages":[{"role":"user","content":"hello"}]}`)
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
				_, err := svc.forwardOpenAINativeGeili(context.Background(), c, geminiSameAccountRetryTestAccount(), body, chat)
				require.Error(t, err)
				require.Equal(t, retries+1, upstream.calls)
			})
		}
	}
}
