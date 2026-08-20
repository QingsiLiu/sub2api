package trainingdata

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCaptureRequestBodyPreservesForwardedBytes(t *testing.T) {
	original := []byte(" {\n  \"model\": \"gpt-test\", \"password\": \"keep-live-body\"\n}\n")
	source := &trackingReadCloser{Reader: bytes.NewReader(original)}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	request.Body = source
	request.ContentLength = int64(len(original))

	captured, reasons := captureRequestBody(request, 1024)
	replayed, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	require.Equal(t, original, replayed, "capture must not normalize or redact the live forwarded request")
	require.True(t, source.closed, "fully buffered source body should be closed")
	require.Empty(t, reasons)
	require.JSONEq(t, `{"model":"gpt-test","password":"<redacted>"}`, string(captured))
}

func TestCaptureRequestBodyTooLargeRestoresPrefixAndRemainder(t *testing.T) {
	original := []byte(`{"model":"gpt-test","input":"0123456789"}`)
	source := &trackingReadCloser{Reader: bytes.NewReader(original)}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	request.Body = source
	request.ContentLength = -1

	captured, reasons := captureRequestBody(request, 8)
	replayed, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	require.Nil(t, captured)
	require.Equal(t, []string{"client_request_body_too_large"}, reasons)
	require.Equal(t, original, replayed)
	require.False(t, source.closed, "partially consumed source remains owned by the replay wrapper")
	require.NoError(t, request.Body.Close())
	require.True(t, source.closed)
}

func TestIsCapturableInferenceRouteIncludesStreamingGeminiOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		path string
		want bool
	}{
		{path: "/v1beta/models/gemini-3:generateContent", want: true},
		{path: "/v1beta/models/gemini-3:streamGenerateContent", want: true},
		{path: "/antigravity/v1beta/models/gemini-3:generateContent", want: true},
		{path: "/v1beta/models/gemini-3:countTokens", want: false},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(http.MethodPost, test.path, nil)
			require.Equal(t, test.want, isCapturableInferenceRoute(context))
		})
	}
}

func TestRequestModelFallsBackToGeminiURL(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3-pro:streamGenerateContent", nil)
	require.Equal(t, "gemini-3-pro", requestModel(request))
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}
