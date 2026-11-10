package httputil

import (
	"context"
	"io"
)

func requestBodyReadChunkCapacityGeili(reader io.Reader, requested int) int {
	if budget, ok := reader.(interface{ RequestBodyReadChunkCapacityGeili(int) int }); ok {
		return budget.RequestBodyReadChunkCapacityGeili(requested)
	}
	return requested
}

type requestBodyDecompressionReserverGeili interface {
	RequestBodyDecompressionReserveGeili(extraBytes int64) error
}

type requestBodyDecompressionBudgetGeili struct {
	reserver  requestBodyDecompressionReserverGeili
	workspace int64
}

func requestBodyDecompressionBudgetForGeili(reader io.Reader, encoding string) (*requestBodyDecompressionBudgetGeili, error) {
	reserver, ok := reader.(requestBodyDecompressionReserverGeili)
	if !ok {
		return nil, nil
	}
	var workspace int64
	switch encoding {
	case "zstd":
		// With concurrency one and low-memory decoding, history may need the
		// 64 MiB window plus ~1 MiB; include block/literal decoder scratch too.
		workspace = maxDecompressedBodySize + (4 << 20)
	case "gzip", "x-gzip", "deflate":
		workspace = 128 << 10
	default:
		return nil, nil // Keep the existing unsupported-encoding error.
	}
	budget := &requestBodyDecompressionBudgetGeili{reserver: reserver, workspace: workspace}
	if err := budget.reserve(0); err != nil {
		return nil, err
	}
	return budget, nil
}

func (b *requestBodyDecompressionBudgetGeili) reserve(outputBytes int64) error {
	return b.reserver.RequestBodyDecompressionReserveGeili(b.workspace + outputBytes)
}

// Keep the Geili reservation on the same chunk/assembly path as the upstream budget.
type decompressionReaderGeili struct {
	io.Reader
	budget *requestBodyDecompressionBudgetGeili
}

func reserveDecompressionOutputGeili(reader io.Reader, allocated int64) error {
	if decoded, ok := reader.(*decompressionReaderGeili); ok && decoded.budget != nil {
		return decoded.budget.reserve(allocated)
	}
	return nil
}

func decompressRequestBodyWithReservationGeili(encoding string, raw []byte, source io.Reader) ([]byte, error) {
	body, err := decompressRequestBodyWithBudget(context.Background(), encoding, raw, source)
	return body.data, err
}
