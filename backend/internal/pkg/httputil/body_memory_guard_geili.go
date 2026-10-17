package httputil

import "io"

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

func readDecompressedRequestBodyGeili(reader io.Reader, budget *requestBodyDecompressionBudgetGeili) ([]byte, error) {
	reader = io.LimitReader(reader, maxDecompressedBodySize)
	if budget == nil {
		return io.ReadAll(reader)
	}
	capacity := requestBodyReadInitCap
	var chunks [][]byte
	var allocated int64
	total := 0
	for {
		chunkCapacity := capacity
		if remaining := maxDecompressedBodySize - total; remaining < chunkCapacity {
			chunkCapacity = remaining
		}
		if err := budget.reserve(allocated + int64(chunkCapacity)); err != nil {
			return nil, err
		}
		chunk := make([]byte, chunkCapacity)
		allocated += int64(chunkCapacity)
		n := 0
		var err error
		for n < len(chunk) && err == nil {
			var read int
			read, err = reader.Read(chunk[n:])
			n += read
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		if n > 0 {
			chunks = append(chunks, chunk[:n])
			total += n
		}
		if err == io.EOF || total == maxDecompressedBodySize {
			if len(chunks) == 0 {
				return chunk[:0], nil
			}
			if len(chunks) == 1 {
				return chunks[0], nil
			}
			// The accumulated chunks stay live while assembling the exact-size
			// output. Reserve that second representation before allocating it.
			if err := budget.reserve(allocated + int64(total)); err != nil {
				return nil, err
			}
			body := make([]byte, total)
			offset := 0
			for _, part := range chunks {
				offset += copy(body[offset:], part)
			}
			return body, nil
		}
		if capacity < requestBodyReadMaxInitCap {
			capacity *= 2
			if capacity > requestBodyReadMaxInitCap {
				capacity = requestBodyReadMaxInitCap
			}
		}
	}
}
