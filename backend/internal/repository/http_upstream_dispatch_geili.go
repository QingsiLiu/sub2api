package repository

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// A context already cancelled at the sending boundary proves no attempt starts
// here. Cancellation after this check retains the dispatch marker: its outcome
// may be uncertain and must not be inferred safe to cancel by the lifecycle.
func doUpstreamRequestWithDispatchGeili(client *http.Client, req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	ctxkey.MarkUpstreamDispatched(req.Context())
	return doUpstreamRequest(client, req)
}
