package handler

import (
	"bytes"
	"crypto/sha256"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Keep only an immutable fingerprint of the compatibility routing view. A slice
// alias would miss in-place policy rewrites, while copying it holds another full
// request body through every queue wait, failover and upstream response stream.
type anthropicRoutedBodySnapshotGeili struct {
	size   int
	digest [sha256.Size]byte
}

func snapshotAnthropicRoutedBodyGeili(body []byte) anthropicRoutedBodySnapshotGeili {
	return anthropicRoutedBodySnapshotGeili{size: len(body), digest: sha256.Sum256(body)}
}

func (s anthropicRoutedBodySnapshotGeili) matches(body []byte) bool {
	return len(body) == s.size && sha256.Sum256(body) == s.digest
}

// Only an actual strict attempt needs its own writable copy of the caller's
// exact bytes. Routing and billing retain the compatibility model identity.
func useStrictAnthropicBodySnapshotGeili(parsed *service.ParsedRequest, routed anthropicRoutedBodySnapshotGeili, clientBody []byte) bool {
	if parsed == nil || !routed.matches(parsed.Body.Bytes()) {
		return false
	}
	return parsed.UseClientBodyGeili(bytes.Clone(clientBody)) == nil
}
