package repository

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareUpstreamResponseKeepsWireHeadersAndCapturesDecodedBody(t *testing.T) {
	var compressed bytes.Buffer
	encoder := gzip.NewWriter(&compressed)
	_, err := encoder.Write([]byte(`{"result":"decoded"}`))
	require.NoError(t, err)
	require.NoError(t, encoder.Close())

	response := &http.Response{
		StatusCode: 200,
		Header: http.Header{
			"Content-Encoding": []string{"gzip"},
			"Content-Length":   []string{"999"},
		},
		Body: io.NopCloser(bytes.NewReader(compressed.Bytes())),
	}
	capture := &recordingUpstreamCapture{}

	prepareUpstreamResponse(response, capture)
	decoded, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	require.Equal(t, "gzip", capture.headerEncoding)
	require.Empty(t, capture.encodingAtBodyWrap)
	require.JSONEq(t, `{"result":"decoded"}`, string(decoded))
	require.Equal(t, decoded, capture.body.Bytes())
	result := response.Header.Get("Content-Encoding")
	require.Empty(t, result)
}

type recordingUpstreamCapture struct {
	headerEncoding     string
	encodingAtBodyWrap string
	body               bytes.Buffer
}

func (c *recordingUpstreamCapture) RecordResponseHeaders(response *http.Response) {
	c.headerEncoding = response.Header.Get("Content-Encoding")
}

func (c *recordingUpstreamCapture) CaptureResponseBody(response *http.Response) {
	c.encodingAtBodyWrap = response.Header.Get("Content-Encoding")
	response.Body = &captureReadCloser{ReadCloser: response.Body, writer: &c.body}
}

type captureReadCloser struct {
	io.ReadCloser
	writer io.Writer
}

func (r *captureReadCloser) Read(target []byte) (int, error) {
	read, err := r.ReadCloser.Read(target)
	if read > 0 {
		_, _ = r.writer.Write(target[:read])
	}
	return read, err
}
