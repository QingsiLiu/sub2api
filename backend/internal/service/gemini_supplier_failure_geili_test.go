//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const supplierCapacityMessageGeili = "当前号池渠道暂时无可用资源，请稍后重试 (request id: 20261016062726898015068268d9d6VRqSdQJW)"
const supplierPrechargeMessageGeili = "预扣费额度失败, 用户剩余额度: ¥0.215730, 需要预扣费额度: ¥0.265954 (request id: id1) (request id: id2)"

func supplierMessageBodyGeili(message string) []byte {
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"message": message, "type": "server_error"}})
	return body
}

func TestGeminiSupplierFailureGeiliExactTemplates(t *testing.T) {
	account := geminiPoolModeAPIKeyAccount()
	for _, test := range []struct {
		name, message string
		status        int
		reason        GatewayFailureReason
	}{
		{"capacity", supplierCapacityMessageGeili, 400, geminiSupplierCapacityReasonGeili},
		{"capacity503", supplierCapacityMessageGeili, 503, geminiSupplierCapacityReasonGeili},
		{"precharge", supplierPrechargeMessageGeili, 403, geminiSupplierBalanceReasonGeili},
		{"zero", "用户额度不足, 剩余额度: ¥0.000000 (request id: id1) (request id: id2)", 403, geminiSupplierBalanceReasonGeili},
		{"punctuation", "当前号池渠道暂时无可用资源, 请稍后重试。", 400, geminiSupplierCapacityReasonGeili},
		{"unknown400", "invalid parameter", 400, ""},
		{"positive balance alone", "用户额度不足, 剩余额度: ¥0.000001", 403, ""},
		{"broad balance", "账户余额不足", 403, ""},
		{"malformed currency", "用户额度不足, 剩余额度: ¥0.", 403, ""},
		{"fake request id", "当前号池渠道暂时无可用资源，请稍后重试 (request id: try another model)", 400, ""},
		{"trailing instruction", supplierCapacityMessageGeili + " invalid parameter", 400, ""},
		{"negative money", "预扣费额度失败, 用户剩余额度: ¥-1, 需要预扣费额度: ¥2", 403, ""},
		{"wrong status", supplierCapacityMessageGeili, 401, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.reason, classifyGeminiSupplierFailureGeili(account, test.status, supplierMessageBodyGeili(test.message)))
		})
	}
	require.Empty(t, classifyGeminiSupplierFailureGeili(&Account{Platform: PlatformAnthropic}, 400, supplierMessageBodyGeili(supplierCapacityMessageGeili)))
	require.Empty(t, classifyGeminiSupplierFailureGeili(account, 400, []byte(`{"input":"当前号池渠道暂时无可用资源，请稍后重试","error":{"message":"bad input"}}`)))
	require.Empty(t, classifyGeminiSupplierFailureGeili(account, 400, []byte(`<html>当前号池渠道暂时无可用资源，请稍后重试</html>`)))
}

func TestGeminiSupplierFailureGeiliTextEntrypoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, entry := range []string{"responses", "chat", "messages", "native"} {
		for _, test := range []struct {
			name, message string
			status        int
		}{{"capacity", supplierCapacityMessageGeili, 400}, {"balance", supplierPrechargeMessageGeili, 403}} {
			t.Run(entry+"/"+test.name, func(t *testing.T) {
				upstream := &epFixedUpstream{statusCode: test.status, body: string(supplierMessageBodyGeili(test.message))}
				repo := &errorPolicyRepoStub{}
				cfg := &config.Config{}
				policy := NewRateLimitService(repo, nil, cfg, nil, nil)
				svc := &GeminiMessagesCompatService{cfg: cfg, httpUpstream: upstream, rateLimitService: policy}
				account := geminiPoolModeAPIKeyAccount()
				// Even explicit error policy matches must not persist supplier balance/capacity failures.
				account.Credentials["pool_mode"] = false
				account.Credentials["custom_error_codes_enabled"] = true
				account.Credentials["custom_error_codes"] = []any{float64(400), float64(403)}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+entry, nil)
				setOpsUpstreamError(c, 500, "stale attempt", "stale detail")
				var err error
				switch entry {
				case "responses":
					gateway := &GatewayService{cfg: cfg, httpUpstream: upstream, rateLimitService: policy}
					_, err = gateway.ForwardAsResponses(context.Background(), c, account, []byte(`{"model":"gemini-4-argon","input":"hello"}`), nil)
				case "chat":
					_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, []byte(`{"model":"gemini-4-argon","messages":[{"role":"user","content":"hello"}]}`))
				case "messages":
					_, err = svc.Forward(context.Background(), c, account, []byte(`{"model":"gemini-4-argon","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`))
				case "native":
					_, err = svc.ForwardNative(context.Background(), c, account, "gemini-4-argon", "generateContent", false, []byte(geminiTransportTestNativeBody))
				}
				var failure *UpstreamFailoverError
				require.True(t, errors.As(err, &failure), "%v", err)
				require.True(t, IsGeminiSupplierUnavailableGeili(failure))
				require.False(t, failure.RetryableOnSameAccount)
				require.False(t, failure.ShouldReportAccountScheduleFailure())
				require.Equal(t, test.status, failure.StatusCode)
				require.Equal(t, 503, failure.ClientStatusCode)
				require.Equal(t, 1, upstream.calls)
				require.Zero(t, repo.tempCalls)
				require.Zero(t, repo.setErrCalls)
				require.Empty(t, repo.modelRateLimitCalls)
				require.Empty(t, rec.Body.String())
				require.False(t, c.Writer.Written())
				require.Equal(t, test.status, c.GetInt(OpsUpstreamStatusCodeKey))
				require.Empty(t, c.GetString(OpsUpstreamErrorDetailKey))
				eventsRaw, ok := c.Get(OpsUpstreamErrorsKey)
				require.True(t, ok)
				events := eventsRaw.([]*OpsUpstreamErrorEvent)
				require.Len(t, events, 1)
				require.Equal(t, test.status, events[0].UpstreamStatusCode)
				require.NotEmpty(t, events[0].Reason)
				require.NotEmpty(t, events[0].UpstreamRequestID)
				require.NotEmpty(t, events[0].UpstreamURL)
				require.NotEmpty(t, GetActualOpenAIUpstreamEndpoint(c))
			})
		}
	}
}

func TestGeminiSupplierFailureGeiliResponsesUnknown400RecordsFinalAttempt(t *testing.T) {
	upstream := &epFixedUpstream{statusCode: 400, body: string(supplierMessageBodyGeili("invalid parameter"))}
	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setOpsUpstreamError(c, 403, "old balance error", "old detail")
	_, err := svc.ForwardAsResponses(context.Background(), c, geminiPoolModeAPIKeyAccount(), []byte(`{"model":"gemini-4-argon","input":"hello"}`), nil)
	require.Error(t, err)
	var failure *UpstreamFailoverError
	require.False(t, errors.As(err, &failure))
	require.Equal(t, 1, upstream.calls)
	require.Equal(t, 400, rec.Code)
	require.Equal(t, 400, c.GetInt(OpsUpstreamStatusCodeKey))
	require.Equal(t, "invalid parameter", c.GetString(OpsUpstreamErrorMessageKey))
	require.Empty(t, c.GetString(OpsUpstreamErrorDetailKey))
	eventsRaw, _ := c.Get(OpsUpstreamErrorsKey)
	events := eventsRaw.([]*OpsUpstreamErrorEvent)
	require.Equal(t, "http_error", events[len(events)-1].Kind)
	require.True(t, strings.HasSuffix(events[len(events)-1].UpstreamURL, "/messages"))
}

func TestGeminiSupplierFailureGeiliPreservesMediaAndBoundCache(t *testing.T) {
	for _, body := range []string{
		`{"generationConfig":{"responseModalities":["TEXT","IMAGE"]}}`,
		`{"generation_config":{"response_modalities":["AUDIO"]}}`,
		`{"request":{"generationConfig":{"responseModalities":["VIDEO"]}}}`,
	} {
		require.True(t, geminiSupplierMediaRequestGeili([]byte(body)))
	}
	require.False(t, geminiSupplierMediaRequestGeili([]byte(`{"generationConfig":{"responseModalities":["TEXT"]}}`)))
	account := geminiPoolModeAPIKeyAccount()
	require.Nil(t, geminiSupplierFailureGeili(WithGeminiCachedContentBound(context.Background()), nil, account, 400, supplierMessageBodyGeili(supplierCapacityMessageGeili), nil, "https://upstream/generateContent", "gemini-4-argon"))
	require.Nil(t, geminiSupplierFailureGeili(context.Background(), nil, account, 400, supplierMessageBodyGeili(supplierCapacityMessageGeili), nil, "https://upstream/generateContent", "gemini-2.5-flash-image"))
}

func TestGeminiSupplierFailureGeiliOrdinary400Attribution(t *testing.T) {
	for _, entry := range []string{"responses", "chat", "messages", "native"} {
		t.Run(entry, func(t *testing.T) {
			upstream := &geminiCompatHTTPUpstreamStub{response: &http.Response{StatusCode: 400, Header: http.Header{"X-Goog-Request-Id": []string{"ordinary-request-id"}}, Body: io.NopCloser(strings.NewReader(string(supplierMessageBodyGeili("invalid parameter"))))}}
			cfg := &config.Config{}
			svc := &GeminiMessagesCompatService{cfg: cfg, httpUpstream: upstream}
			account := geminiPoolModeAPIKeyAccount()
			account.Credentials["base_url"] = "https://supplier.example/custom"
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+entry, nil)
			var err error
			switch entry {
			case "responses":
				gateway := &GatewayService{cfg: cfg, httpUpstream: upstream}
				_, err = gateway.ForwardAsResponses(context.Background(), c, account, []byte(`{"model":"gemini-4-argon","input":"hello"}`), nil)
			case "chat":
				_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, []byte(`{"model":"gemini-4-argon","messages":[{"role":"user","content":"hello"}]}`))
			case "messages":
				_, err = svc.Forward(context.Background(), c, account, []byte(`{"model":"gemini-4-argon","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`))
			case "native":
				_, err = svc.ForwardNative(context.Background(), c, account, "gemini-4-argon", "generateContent", false, []byte(geminiTransportTestNativeBody))
			}
			require.Error(t, err)
			require.Equal(t, 400, rec.Code)
			require.Equal(t, 1, upstream.calls)
			eventsRaw, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok)
			events := eventsRaw.([]*OpsUpstreamErrorEvent)
			require.Len(t, events, 1, "enrichment must not create duplicate attempt events")
			require.Equal(t, 400, events[0].UpstreamStatusCode)
			require.Equal(t, "ordinary-request-id", events[0].UpstreamRequestID)
			require.Equal(t, safeUpstreamURL(upstream.lastReq.URL.String()), events[0].UpstreamURL)
		})
	}
}

func TestGeminiSupplierFailureGeiliAttributionResetAndPlatformIsolation(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	SetGeminiActualUpstreamURLGeili(c, "https://user:secret@supplier.example/v1beta/models/test:generateContent?key=secret#secret")
	SetGeminiUpstreamResponseHeadersGeili(c, http.Header{"X-Request-Id": []string{"req-first"}})
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: PlatformGemini})
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: PlatformAnthropic})
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: PlatformGemini, UpstreamURL: "https://explicit.example/messages", UpstreamRequestID: "explicit-id"})
	SetGeminiActualUpstreamURLGeili(c, "https://second.example/generateContent")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: PlatformGemini})
	ClearGeminiActualUpstreamGeili(c)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: PlatformGemini})
	raw, _ := c.Get(OpsUpstreamErrorsKey)
	events := raw.([]*OpsUpstreamErrorEvent)
	require.Len(t, events, 5)
	require.Equal(t, "https://supplier.example/v1beta/models/test:generateContent", events[0].UpstreamURL)
	require.Equal(t, "req-first", events[0].UpstreamRequestID)
	require.Empty(t, events[1].UpstreamURL)
	require.Empty(t, events[1].UpstreamRequestID)
	require.Equal(t, "https://explicit.example/messages", events[2].UpstreamURL)
	require.Equal(t, "explicit-id", events[2].UpstreamRequestID)
	require.Equal(t, "https://second.example/generateContent", events[3].UpstreamURL)
	require.Empty(t, events[3].UpstreamRequestID)
	require.Empty(t, events[4].UpstreamURL)
	require.Empty(t, events[4].UpstreamRequestID)
}
