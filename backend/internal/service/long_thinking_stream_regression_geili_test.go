//go:build unit

package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLongThinkingExpandedStreamsGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"anthropic", "responses", "native-converted", "antigravity-gemini", "antigravity-claude", "antigravity-buffered", "antigravity-upstream", "antigravity-compat"} {
		t.Run(protocol, func(t *testing.T) {
			t.Parallel()
			cfg := &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1, LongThinkingStreamDataIntervalTimeout: 3, MaxLineSize: defaultMaxLineSize}}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			beginUpstreamResponseModelObservation(c)
			reader, writer := io.Pipe()
			defer func() { _ = reader.Close(); _ = writer.Close() }()
			payload := anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText, geiliAnthropicClosed, geiliAnthropicReason, geiliAnthropicStop)
			model := "claude-opus-5"
			if protocol == "responses" {
				model = "deepseek-reasoner"
				payload = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n\n"
			}
			if protocol == "native-converted" {
				model = "glm-5"
			}
			if protocol == "antigravity-gemini" || protocol == "antigravity-claude" || protocol == "antigravity-buffered" || protocol == "antigravity-compat" {
				model = "gemini-3.1-pro"
				payload = "data: {\"response\":{\"responseId\":\"resp_ok\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":1}}}\n\n"
			}
			SetOpsUpstreamModel(c, model)
			go func() {
				defer func() { _ = writer.Close() }()
				time.Sleep(1400 * time.Millisecond) // Beyond the ordinary watchdog.
				_, _ = io.WriteString(writer, payload)
			}()
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}
			started := time.Now()
			openAI := &OpenAIGatewayService{cfg: cfg}
			ag := newAntigravityTestService(cfg)
			var err error
			switch protocol {
			case "anthropic":
				svc := newMinimalGatewayService()
				svc.cfg = cfg
				_, err = svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{}, started, "public-alias", model, false)
			case "responses":
				_, err = openAI.handleStreamingResponse(c.Request.Context(), resp, c, &Account{Platform: PlatformOpenAI}, started, "public-alias", model)
			case "native-converted":
				_, err = openAI.handleResponsesStreamingFromNativeAnthropic(resp, c, "public-alias", model, model, nil, started, apicompat.ResponsesClientToolMapping{})
			case "antigravity-gemini":
				_, err = ag.handleGeminiStreamingResponse(c, resp, started)
			case "antigravity-claude":
				_, err = ag.handleClaudeStreamingResponse(c, resp, started, "public-alias")
			case "antigravity-buffered":
				_, err = ag.handleClaudeStreamToNonStreaming(c, resp, started, "public-alias")
			case "antigravity-upstream":
				ag.streamUpstreamResponse(c, resp, started)
			case "antigravity-compat":
				_, err = ag.handleResponsesStreamingFromAntigravity(c, resp, started, "public-alias")
			}
			require.NoError(t, err)
			require.NotEmpty(t, rec.Body.String())
			require.NotContains(t, rec.Body.String(), "stream_timeout")
			require.GreaterOrEqual(t, time.Since(started), time.Second)
		})
	}
}

func TestLongThinkingAntigravityMappedBudgetGeili(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 180, LongThinkingStreamDataIntervalTimeout: 600}}
	svc := newAntigravityTestService(cfg)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	SetOpsUpstreamModel(c, "gemini-3.1-pro")
	require.Equal(t, 600*time.Second, svc.antigravityStreamIntervalGeili(c, "public-alias"))
	SetOpsUpstreamModel(c, "gemini-2.5-flash-image")
	require.Equal(t, 180*time.Second, svc.antigravityStreamIntervalGeili(c, "claude-opus-5"), "mapped media must not inherit a public alias's text budget")
	cfg.Gateway.StreamDataIntervalTimeout = 0
	SetOpsUpstreamModel(c, "gemini-3.1-pro")
	require.Zero(t, svc.antigravityStreamIntervalGeili(c, "public-alias"))
}

func TestLongThinkingOpus5StillTimesOutWithDownstreamHeartbeatsGeili(t *testing.T) {
	svc := newMinimalGatewayService()
	svc.cfg.Gateway.StreamDataIntervalTimeout = 1
	svc.cfg.Gateway.LongThinkingStreamDataIntervalTimeout = 3
	svc.cfg.Gateway.StreamKeepaliveInterval = 1
	c, rec := newOpenAIStreamFailedTestContext()
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	go func() {
		_, _ = io.WriteString(writer, anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText))
	}()
	started := time.Now()
	_, err := svc.handleStreamingResponse(c.Request.Context(), &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}, c, &Account{}, started, "claude-opus-5", "claude-opus-5", false)
	require.ErrorContains(t, err, "stream data interval timeout")
	require.Contains(t, rec.Body.String(), "event: ping")
	require.Contains(t, rec.Body.String(), "stream_timeout")
	require.GreaterOrEqual(t, time.Since(started), 3*time.Second)
	require.Less(t, time.Since(started), 5*time.Second, "downstream heartbeats must not replenish the upstream idle budget")
}

func TestLongThinkingAntigravityPreContentDeadlineGeili(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 180, LongThinkingStreamDataIntervalTimeout: 600}}
	svc := newAntigravityTestService(cfg)
	c, rec := newAntigravityCompatContext(http.MethodPost, "/v1/responses", nil)
	SetOpsUpstreamModel(c, "gemini-3.1-pro")
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	go func() {
		defer func() { _ = writer.Close() }()
		_, _ = io.WriteString(writer, "data: {\"response\":{\"responseId\":\"resp_ok\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}}\n\n")
	}()
	// The old fixed two-minute pre-content deadline would immediately fail,
	// despite the selected model being allowed ten minutes of silent thinking.
	_, err := svc.handleResponsesStreamingFromAntigravity(c, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}, time.Now().Add(-121*time.Second), "public-alias")
	require.NoError(t, err)
	require.Contains(t, rec.Body.String(), "response.completed")
}
