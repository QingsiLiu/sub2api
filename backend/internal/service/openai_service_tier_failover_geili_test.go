package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const openAIServiceTierDeniedBodyGeili = `{"error":{"code":null,"message":"Invalid service_tier argument: The requested service tier is not allowed for this project.","param":"service_tier","type":"invalid_request_error"}}`

func TestOpenAIServiceTierNotAllowedFailsOverGeili(t *testing.T) {
	body := []byte(openAIServiceTierDeniedBodyGeili)
	require.True(t, isOpenAIServiceTierNotAllowedGeili(http.StatusBadRequest, body))
	require.True(t, (&OpenAIGatewayService{}).shouldFailoverOpenAIUpstreamResponse(newOpenAIUpstreamErrorTestAccount(), http.StatusBadRequest, "", body))
	require.True(t, shouldFailoverOpenAIPassthroughResponse(&Account{Type: AccountTypeAPIKey}, http.StatusBadRequest, body))

	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"invalid tier value":  {400, `{"error":{"message":"Invalid service_tier \"turbo\"","param":"service_tier","type":"invalid_request_error"}}`},
		"other param":         {400, `{"error":{"message":"The requested service tier is not allowed for this project.","param":"model","type":"invalid_request_error"}}`},
		"text only in prompt": {400, `{"error":{"message":"bad input","param":"input"},"echo":"service tier is not allowed"}`},
		"plain text":          {400, `The requested service tier is not allowed for this project.`},
		"not a 400":           {403, openAIServiceTierDeniedBodyGeili},
	} {
		t.Run(name, func(t *testing.T) {
			require.False(t, isOpenAIServiceTierNotAllowedGeili(tc.status, []byte(tc.body)))
		})
	}
}

func TestWriteOpenAIServiceTierExhaustedGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	failover := &UpstreamFailoverError{StatusCode: http.StatusBadRequest, ResponseBody: []byte(openAIServiceTierDeniedBodyGeili)}

	require.False(t, WriteOpenAIServiceTierExhaustedGeili(c, failover, true), "a started stream keeps the in-stream error path")
	require.True(t, WriteOpenAIServiceTierExhaustedGeili(c, failover, false))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), `"param":"service_tier"`)
	require.Contains(t, rec.Body.String(), "not allowed for this project")

	other := &UpstreamFailoverError{StatusCode: http.StatusBadRequest, ResponseBody: []byte(`{"error":{"message":"x","param":"model"}}`)}
	require.False(t, WriteOpenAIServiceTierExhaustedGeili(c, other, false))
}

func TestClassifyAnthropicBedrockChannelValidationGeili(t *testing.T) {
	cases := map[string]struct {
		body    string
		matched bool
	}{
		"output_config extra input": {`{"error":{"message":"InvokeModel: operation error Bedrock Runtime: InvokeModel, https response error StatusCode: 400, ValidationException: output_config.format: Extra inputs are not permitted","type":"<nil>"},"type":"error"}`, true},
		"unsupported beta header":   {`{"error":{"message":"InvokeModelWithResponseStream: operation error Bedrock Runtime: InvokeModelWithResponseStream, https response error StatusCode: 400, ValidationException: Unexpected value(s) ` + "`redact-thinking-2026-02-12`" + ` for the ` + "`anthropic-beta`" + ` header.","type":"<nil>"},"type":"error"}`, true},
		"direct anthropic extra":    {`{"type":"error","error":{"type":"invalid_request_error","message":"output_config.format: Extra inputs are not permitted"}}`, false},
		"bedrock prompt too long":   {`{"error":{"message":"InvokeModel: operation error Bedrock Runtime: InvokeModel, https response error StatusCode: 400, ValidationException: prompt is too long: 6260894 tokens > 1000000 maximum"},"type":"error"}`, false},
		"bedrock tool_choice":       {`{"error":{"message":"Bedrock Runtime: ValidationException: tool_choice: type \"tool\" and \"any\" are not supported for this model."},"type":"error"}`, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			reason, matched := classifyAnthropicAccountScopedFailureGeili(http.StatusBadRequest, []byte(tc.body))
			require.Equal(t, tc.matched, matched)
			if tc.matched {
				require.Equal(t, anthropicChannelIncompatibleReasonGeili, reason)
			}
		})
	}
}

func TestWriteAnthropicChannelIncompatibleExhaustedGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	body := `{"error":{"message":"InvokeModel: operation error Bedrock Runtime: InvokeModel, https response error StatusCode: 400, ValidationException: output_config.format: Extra inputs are not permitted","type":"<nil>"},"type":"error"}`
	failover := &UpstreamFailoverError{StatusCode: http.StatusBadRequest, ResponseBody: []byte(body), Reason: anthropicChannelIncompatibleReasonGeili}
	require.False(t, failover.ShouldReportAccountScheduleFailure(), "a channel limitation is not account health")
	require.True(t, WriteAnthropicChannelIncompatibleExhaustedGeili(c, failover, false))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, body, rec.Body.String())
}
