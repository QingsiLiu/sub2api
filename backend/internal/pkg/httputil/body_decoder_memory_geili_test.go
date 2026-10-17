package httputil

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestRequestBodyGeiliZstdDecoderWindowIsBounded(t *testing.T) {
	// A valid empty zstd frame with a 128 MiB advertised window. Streaming
	// decoders allocate history by window size even when output is tiny.
	largeWindow := []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x88, 0x01, 0x00, 0x00}
	_, err := decompressRequestBody("zstd", largeWindow)
	require.Error(t, err)
	require.Contains(t, err.Error(), "window size")

	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	require.NoError(t, err)
	defer enc.Close()
	body := []byte(" \n{\"model\":\"keep\",\"input\":\"" + string(bytes.Repeat([]byte{'x'}, 2<<20)) + "\"} \n")
	decoded, err := decompressRequestBody("zstd", enc.EncodeAll(body, nil))
	require.NoError(t, err)
	require.Equal(t, body, decoded, "accepted compressed payload bytes stay unchanged")
}

var errDecoderBudgetGeili = errors.New("test decoder memory budget exhausted")

type decoderBudgetReaderGeili struct {
	io.ReadCloser
	limit int64
	calls []int64
}

func (r *decoderBudgetReaderGeili) RequestBodyDecompressionReserveGeili(extra int64) error {
	r.calls = append(r.calls, extra)
	if extra > r.limit {
		return errDecoderBudgetGeili
	}
	return nil
}

func compressDecoderBodyGeili(t *testing.T, encoding string, body []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	switch encoding {
	case "gzip", "x-gzip":
		writer := gzip.NewWriter(&compressed)
		_, err := writer.Write(body)
		require.NoError(t, err)
		require.NoError(t, writer.Close())
	case "deflate":
		writer := zlib.NewWriter(&compressed)
		_, err := writer.Write(body)
		require.NoError(t, err)
		require.NoError(t, writer.Close())
	case "zstd":
		writer, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
		require.NoError(t, err)
		defer writer.Close()
		return writer.EncodeAll(body, nil)
	default:
		t.Fatalf("unsupported test encoding %q", encoding)
	}
	return compressed.Bytes()
}

func TestRequestBodyGeiliSmallCompressedFitsDefaultStageBudget(t *testing.T) {
	body := []byte(" \n{\"model\":\"keep\",\"input\":\"" + string(bytes.Repeat([]byte{'x'}, 2<<20)) + "\"} \n")
	for _, encoding := range []string{"gzip", "x-gzip", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			raw := compressDecoderBodyGeili(t, encoding, body)
			req := newRequestWithBody(t, raw, encoding)
			budget := &decoderBudgetReaderGeili{ReadCloser: req.Body, limit: (128 << 20) - int64(len(raw))}
			req.Body = budget
			got, err := ReadRequestBodyWithPrealloc(req)
			require.NoError(t, err)
			require.Equal(t, body, got)
			require.Empty(t, req.Header.Get("Content-Encoding"))
			require.Equal(t, int64(len(body)), req.ContentLength)
			require.Greater(t, len(budget.calls), 2, "workspace and decoded chunks must both be reserved")
			require.GreaterOrEqual(t, budget.calls[len(budget.calls)-1], budget.calls[0]+2*int64(len(body)), "final assembly also retains accumulated chunks")
		})
	}
}

func TestRequestBodyGeiliReservesBeforeDecoderCreation(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			budget := &decoderBudgetReaderGeili{ReadCloser: io.NopCloser(bytes.NewReader(nil)), limit: 0}
			_, err := decompressRequestBodyWithReservationGeili(encoding, []byte("invalid compressed data"), budget)
			require.ErrorIs(t, err, errDecoderBudgetGeili, "capacity rejection must precede decoder header parsing/allocation")
			require.Len(t, budget.calls, 1)
		})
	}
}

func TestRequestBodyGeiliCompressedAssemblyCannotBypassBudget(t *testing.T) {
	body := bytes.Repeat([]byte{'x'}, (1<<20)+17)
	raw := compressDecoderBodyGeili(t, "gzip", body)
	req := newRequestWithBody(t, raw, "gzip")
	budget := &decoderBudgetReaderGeili{ReadCloser: req.Body, limit: (128 << 10) + (5 << 19)}
	req.Body = budget
	got, err := ReadRequestBodyWithPrealloc(req)
	require.ErrorIs(t, err, errDecoderBudgetGeili)
	require.Nil(t, got)
	require.Equal(t, "gzip", req.Header.Get("Content-Encoding"), "a failed read must not publish decoded metadata")
	require.Greater(t, budget.calls[len(budget.calls)-1], budget.limit)
	for _, reserved := range budget.calls[:len(budget.calls)-1] {
		require.LessOrEqual(t, reserved, budget.limit, "chunk allocation must fit before assembly is attempted")
	}
}

func TestRequestBodyGeiliCompressedConcatenatedFramesPreserveBytes(t *testing.T) {
	for _, encoding := range []string{"gzip", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			first, second := []byte("first\n"), bytes.Repeat([]byte("second\n"), 1024)
			raw := append(compressDecoderBodyGeili(t, encoding, first), compressDecoderBodyGeili(t, encoding, second)...)
			req := newRequestWithBody(t, raw, encoding)
			req.Body = &decoderBudgetReaderGeili{ReadCloser: req.Body, limit: 128 << 20}
			got, err := ReadRequestBodyWithPrealloc(req)
			require.NoError(t, err)
			require.Equal(t, append(first, second...), got)
		})
	}
}
