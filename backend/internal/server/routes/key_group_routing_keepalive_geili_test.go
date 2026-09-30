package routes

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const upstreamLimitBody = `{"type":"error","error":{"type":"rate_limit_error","message":"Upstream rate limit exceeded, please retry later"}}`

// keyGroupAttemptLoop 复刻 explicitKeyRouting 对每个候选分组的处理：每次尝试用 keyRouteWriter 缓冲被拒绝的响应，
// 只有可重试的失败才继续下一个分组。其余部分（鉴权、解析候选分组）与本测试无关。
func keyGroupAttemptLoop(attemptDelay time.Duration, outcomes ...int) gin.HandlerFunc {
	return func(c *gin.Context) {
		handler.ArmEdgeSSEKeepalive(c, true)
		for index, status := range outcomes {
			attempt := handler.CopyContextForAttempt(c)
			writer := newKeyRouteWriter(c.Writer)
			attempt.Writer = writer
			time.Sleep(attemptDelay)
			if status == http.StatusOK {
				attempt.Header("Content-Type", "text/event-stream")
				_, _ = attempt.Writer.WriteString("event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
				attempt.Writer.Flush()
			} else {
				attempt.JSON(status, gin.H{"type": "error", "error": gin.H{"type": "rate_limit_error", "message": "Upstream rate limit exceeded, please retry later"}})
			}
			if index+1 < len(outcomes) && writer.canRetry() {
				continue
			}
			writer.commit()
			return
		}
	}
}

func postThroughKeepalive(t *testing.T, loop gin.HandlerFunc) (*http.Response, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.Gateway.StreamKeepaliveInterval = 1
	r := gin.New()
	r.POST("/v1/messages", handler.NewEdgeSSEKeepalive(cfg, 30*time.Millisecond), loop)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages", strings.NewReader(`{"stream":true}`))
	require.NoError(t, err)
	req.Header.Set("Cf-Ray", "a431221ddf69ce83-SIN")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(body)
}

// 多分组 Key：第一个分组慢了很久才返回上游限流，心跳此时已经把线上响应提交成 200。
// 跨组重试不能因此失效，第二个分组的真实流要能接在心跳后面。
func TestEdgeKeepaliveKeepsCrossGroupRetryWorking(t *testing.T) {
	resp, body := postThroughKeepalive(t, keyGroupAttemptLoop(120*time.Millisecond, http.StatusTooManyRequests, http.StatusOK))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, body, "event: ping", "第一个分组等待期间 Cloudflare 必须看到字节")
	require.Contains(t, body, "event: message_start", "第二个分组应当接管并返回真实流")
	require.NotContains(t, body, "event: error", "被重试掉的失败不得泄漏给客户端")
	require.Less(t, strings.Index(body, "event: ping"), strings.Index(body, "event: message_start"))
}

func TestEdgeKeepaliveDeliversFinalFailureAsSSEErrorAfterAllGroups(t *testing.T) {
	resp, body := postThroughKeepalive(t, keyGroupAttemptLoop(120*time.Millisecond, http.StatusTooManyRequests, http.StatusTooManyRequests))

	require.Equal(t, http.StatusOK, resp.StatusCode, "线上状态码在心跳提交后已固化")
	require.Contains(t, body, "event: ping")
	require.Equal(t, 1, strings.Count(body, "event: error"))
	frame := body[strings.Index(body, "event: error"):]
	require.True(t, strings.HasSuffix(frame, "\n\n"))
	require.JSONEq(t, upstreamLimitBody, strings.TrimSpace(strings.TrimPrefix(strings.SplitN(frame, "\n", 3)[1], "data: ")))
}

func TestEdgeKeepaliveFastCrossGroupFailureStillReturnsHTTPStatus(t *testing.T) {
	resp, body := postThroughKeepalive(t, keyGroupAttemptLoop(0, http.StatusTooManyRequests, http.StatusTooManyRequests))

	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.JSONEq(t, upstreamLimitBody, body)
}
