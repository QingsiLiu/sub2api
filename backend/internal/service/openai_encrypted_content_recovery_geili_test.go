package service

import (
	"context"
	"fmt"
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

// Synthetic ciphertext only; the error shape matches the affected provider.
const encryptedContentErrorGeili = `{"error":{"code":"invalid_request_error","type":"invalid_request_error","message":"The encrypted content gAAA...test could not be verified. Reason: Encrypted content could not be decrypted or parsed."}}`

func encryptedRecoveryResponseGeili(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func encryptedRecoveryFixtureGeili(upstream *httpUpstreamRecorder) (*OpenAIGatewayService, *Account) {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	return &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}, &Account{
		ID: 10, Name: "synthetic-encrypted-recovery", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test-fixture", "base_url": "https://example.com"},
		Extra:       map[string]any{"use_responses_api": true},
	}
}

func encryptedRecoveryContextGeili(session string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("session_id", session)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	return c, rec
}

func TestOpenAIEncryptedRecoveryGeiliRestoresReasoningAndNextTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			success := func() *http.Response {
				payload := `{"id":"resp_ok","object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":2}}`
				resp := encryptedRecoveryResponseGeili(http.StatusOK, payload)
				if stream {
					resp.Header.Set("Content-Type", "text/event-stream")
					resp.Body = io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":" + payload + "}\n\ndata: [DONE]\n\n"))
				}
				return resp
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				encryptedRecoveryResponseGeili(http.StatusBadRequest, encryptedContentErrorGeili), success(), success(), success(), success(),
			}}
			svc, account := encryptedRecoveryFixtureGeili(upstream)
			body := []byte(fmt.Sprintf(`{"model":"gpt-6-astra","stream":%v,"reasoning":{"effort":"high"},"tools":[{"type":"function","name":"read_file","parameters":{"type":"object","properties":{}}}],"input":[{"type":"reasoning","id":"rs_test","encrypted_content":"old-cipher","summary":[{"type":"summary_text","text":"keep summary"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"continue","nonce":9007199254740993}]},{"type":"function_call","call_id":"call_test","name":"read_file","arguments":"{}"},{"type":"function_call_output","call_id":"call_test","output":"keep tool output"}]}`, stream))
			original := string(body)
			c, rec := encryptedRecoveryContextGeili("session-recover")
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, http.StatusOK, rec.Code)
			require.NotContains(t, rec.Body.String(), "could not be verified", "the recoverable error must not leak before retry succeeds")
			require.Len(t, upstream.bodies, 2)
			require.Equal(t, "old-cipher", gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").String())
			require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.encrypted_content").Exists())
			for _, path := range []string{"model", "reasoning", "tools", "input.0.summary", "input.1", "input.2", "input.3"} {
				require.Equal(t, gjson.GetBytes(upstream.bodies[0], path).Value(), gjson.GetBytes(upstream.bodies[1], path).Value(), path)
			}
			require.Equal(t, "9007199254740993", gjson.GetBytes(upstream.bodies[1], "input.1.content.0.nonce").Raw)
			require.Equal(t, original, string(body), "canonical client body must remain immutable")

			// The client resends its original history; only the rejected cipher is removed.
			c, _ = encryptedRecoveryContextGeili("session-recover")
			_, err = svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.Len(t, upstream.bodies, 3)
			require.False(t, gjson.GetBytes(upstream.bodies[2], "input.0.encrypted_content").Exists())

			freshBody := []byte(strings.Replace(original, "old-cipher", "fresh-cipher", 1))
			c, _ = encryptedRecoveryContextGeili("session-recover")
			_, err = svc.Forward(context.Background(), c, account, freshBody)
			require.NoError(t, err)
			require.Equal(t, "fresh-cipher", gjson.GetBytes(upstream.bodies[3], "input.0.encrypted_content").String())

			c, _ = encryptedRecoveryContextGeili("unrelated-session")
			_, err = svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.Len(t, upstream.bodies, 5)
			require.Equal(t, "old-cipher", gjson.GetBytes(upstream.bodies[4], "input.0.encrypted_content").String())
		})
	}
}

func TestOpenAIEncryptedRecoveryGeiliRetriesOnlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		encryptedRecoveryResponseGeili(http.StatusBadRequest, encryptedContentErrorGeili),
		encryptedRecoveryResponseGeili(http.StatusBadRequest, encryptedContentErrorGeili),
	}}
	svc, account := encryptedRecoveryFixtureGeili(upstream)
	c, _ := encryptedRecoveryContextGeili("session-repeat")
	_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-6-astra","input":[{"type":"reasoning","encrypted_content":"cipher","summary":[]},{"type":"message","role":"user","content":"continue"}]}`))
	require.Error(t, err)
	require.Len(t, upstream.bodies, 2)
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.encrypted_content").Exists())
}

func TestOpenAIEncryptedRecoveryGeiliPreservesOpaqueHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, input := range []string{
		`[{"type":"compaction","encrypted_content":"history-cipher"},{"type":"reasoning","encrypted_content":"reasoning-cipher","summary":[]}]`,
		`[{"type":"compaction_summary","encrypted_content":"history-cipher"}]`,
		`{"type":"compaction","encrypted_content":"history-cipher"}`,
	} {
		t.Run(input, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{responses: []*http.Response{encryptedRecoveryResponseGeili(http.StatusBadRequest, encryptedContentErrorGeili)}}
			svc, account := encryptedRecoveryFixtureGeili(upstream)
			c, _ := encryptedRecoveryContextGeili("session-history")
			body := []byte(`{"model":"gpt-6-astra","input":` + input + `}`)
			_, err := svc.Forward(context.Background(), c, account, body)
			require.Error(t, err)
			require.Len(t, upstream.bodies, 1, "cannot recover by deleting the only copy of conversation history")
			require.JSONEq(t, input, gjson.GetBytes(upstream.bodies[0], "input").Raw)
			require.False(t, svc.getOpenAIWSStateStore().HasAnySessionInvalidEncryptedContent(), "unsafe history must not be marked for deletion on the next turn")
		})
	}
}

func TestOpenAIEncryptedRecoveryGeiliRejectsAmbiguousFailures(t *testing.T) {
	message := "The encrypted content gAAA...test could not be verified. Reason: Encrypted content could not be decrypted or parsed."
	reasoning := `{"input":[{"type":"reasoning","encrypted_content":"cipher","summary":[]}]}`
	for _, tc := range []struct {
		name, code, message, body string
		status                    int
		want                      bool
	}{
		{name: "provider generic code", status: 400, code: "invalid_request_error", message: message, body: reasoning, want: true},
		{name: "provider dropped code", status: 400, message: message, body: reasoning, want: true},
		{name: "single input object", status: 400, message: message, body: `{"input":{"type":"reasoning","encrypted_content":"cipher"}}`, want: true},
		{name: "normalized generic code", status: 400, code: " INVALID_REQUEST_ERROR ", message: strings.ToUpper(message), body: reasoning, want: true},
		{name: "rate limit", status: 429, message: message, body: reasoning},
		{name: "authentication failure", status: 401, message: message, body: reasoning},
		{name: "server error", status: 500, message: message, body: reasoning},
		{name: "unrelated explicit code", status: 400, code: "context_length_exceeded", message: message, body: reasoning},
		{name: "ordinary invalid request", status: 400, code: "invalid_request_error", message: "The function call could not be verified", body: reasoning},
		{name: "only mentions encrypted content", status: 400, message: "encrypted content is not supported", body: reasoning},
		{name: "incomplete decryption signature", status: 400, message: "The encrypted content could not be verified", body: reasoning},
		{name: "no ciphertext", status: 400, message: message, body: `{"input":[{"type":"reasoning","summary":[]}]}`},
		{name: "null ciphertext", status: 400, message: message, body: `{"input":[{"type":"reasoning","encrypted_content":null}]}`},
		{name: "only user text", status: 400, message: message, body: `{"input":[{"type":"message","role":"user","content":"encrypted_content"}]}`},
		{name: "previous response state", status: 400, message: message, body: `{"previous_response_id":"resp_state","input":[{"type":"reasoning","encrypted_content":"cipher"}]}`},
		{name: "unknown encrypted item", status: 400, message: message, body: `{"input":[{"type":"reasoning","encrypted_content":"cipher"},{"type":"future_history","encrypted_content":"opaque"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, canRecoverOpenAIWrappedEncryptedReasoningGeili(tc.status, tc.code, tc.message, []byte(tc.body)))
		})
	}
}

func TestOpenAIEncryptedRecoveryGeiliLeavesOrdinary400Unchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, errorBody := range []string{
		`{"error":{"code":"invalid_request_error","message":"Missing tool output for call_test"}}`,
		strings.Replace(encryptedContentErrorGeili, `"code":"invalid_request_error"`, `"code":"context_length_exceeded"`, 1),
	} {
		t.Run(errorBody, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{responses: []*http.Response{encryptedRecoveryResponseGeili(http.StatusBadRequest, errorBody)}}
			svc, account := encryptedRecoveryFixtureGeili(upstream)
			c, _ := encryptedRecoveryContextGeili("ordinary-error")
			_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-6-astra","input":[{"type":"reasoning","encrypted_content":"cipher","summary":[]}]}`))
			require.Error(t, err)
			require.Len(t, upstream.bodies, 1)
			require.Equal(t, "cipher", gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").String())
			require.False(t, svc.getOpenAIWSStateStore().HasAnySessionInvalidEncryptedContent())
		})
	}
}
