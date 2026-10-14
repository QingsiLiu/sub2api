package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
)

const anthropicStreamDiagnosticKeyGeili = "geili_anthropic_stream_diagnostic"

// A block's seed is fallback input. Once actual argument fragments arrive they
// form their own stream, independent of the seed and of intermediate valid {}.
type anthropicToolArgumentsGeili struct {
	seed            json.RawMessage
	delta           []byte
	deltaCount      int
	emptyDeltaCount int
}

func (a *anthropicToolArgumentsGeili) append(fragment string) {
	if fragment == "" {
		a.emptyDeltaCount++
		return
	}
	a.deltaCount++
	a.delta = append(a.delta, fragment...)
}

func (a *anthropicToolArgumentsGeili) input() json.RawMessage {
	if a.deltaCount > 0 {
		return a.delta
	}
	if len(a.seed) > 0 {
		return a.seed
	}
	// Compatible relays can omit input entirely for a no-argument tool.
	return json.RawMessage("{}")
}

type anthropicStreamDiagnosticGeiliError struct {
	code, message string
	detail        map[string]any
}

func (e *anthropicStreamDiagnosticGeiliError) Error() string {
	return fmt.Sprintf("upstream stream read error: ended with invalid tool arguments (content_block_index=%v)", e.detail["content_block_index"])
}

func newAnthropicToolArgumentsDiagnosticGeili(index int, block apicompat.AnthropicContentBlock, arguments *anthropicToolArgumentsGeili, readErr error) error {
	detail := map[string]any{
		"reason": "invalid_tool_arguments", "content_block_index": index,
		"block_type": block.Type, "argument_bytes": len(block.Input), "json_valid": false,
		"has_terminal_reason": true, "open_block_count": 0,
		"seed_bytes": len(block.Input), "delta_bytes": 0,
		"delta_count": 0, "nonempty_delta_count": 0, "empty_delta_count": 0,
	}
	if arguments != nil {
		detail["seed_bytes"] = len(arguments.seed)
		detail["delta_bytes"] = len(arguments.delta)
		detail["delta_count"] = arguments.deltaCount + arguments.emptyDeltaCount
		detail["nonempty_delta_count"] = arguments.deltaCount
		detail["empty_delta_count"] = arguments.emptyDeltaCount
	}
	var parsed json.RawMessage
	var syntax *json.SyntaxError
	if err := json.Unmarshal(block.Input, &parsed); errors.As(err, &syntax) {
		detail["syntax_offset"] = syntax.Offset
	}
	switch {
	case errors.Is(readErr, io.EOF):
		detail["read_error_kind"] = "eof"
	case errors.Is(readErr, context.Canceled):
		detail["read_error_kind"] = "canceled"
	case errors.Is(readErr, context.DeadlineExceeded):
		detail["read_error_kind"] = "deadline"
	case readErr != nil:
		detail["read_error_kind"] = "read_error"
	default:
		detail["read_error_kind"] = "none"
	}
	return &anthropicStreamDiagnosticGeiliError{
		code: "upstream_tool_arguments_invalid", message: "Upstream tool arguments were invalid", detail: detail,
	}
}

// AnthropicStreamDiagnosticGeili exposes only fixed classifications and counts.
// Tool names, IDs, argument text, JSON syntax messages and addresses stay private.
func AnthropicStreamDiagnosticGeili(err error) (code, message string, detail map[string]any, ok bool) {
	var diagnostic *anthropicStreamDiagnosticGeiliError
	if !errors.As(err, &diagnostic) {
		return "", "", nil, false
	}
	detail = make(map[string]any, len(diagnostic.detail))
	for key, value := range diagnostic.detail {
		detail[key] = value
	}
	return diagnostic.code, diagnostic.message, detail, true
}

func rememberAnthropicStreamDiagnosticGeili(c *gin.Context, err error) {
	var diagnostic *anthropicStreamDiagnosticGeiliError
	if c != nil && errors.As(err, &diagnostic) {
		c.Set(anthropicStreamDiagnosticKeyGeili, diagnostic)
	}
}

func recordAnthropicStreamDiagnosticGeili(c *gin.Context, account *Account, err error) {
	if c == nil || account == nil || err == nil {
		return
	}
	if value, _ := c.Get(anthropicStreamDiagnosticKeyGeili); value != nil {
		if diagnostic, ok := value.(*anthropicStreamDiagnosticGeiliError); ok {
			err = diagnostic
		}
	}
	c.Set(anthropicStreamDiagnosticKeyGeili, nil)
	code, message, detail, ok := AnthropicStreamDiagnosticGeili(err)
	if !ok {
		return
	}
	encoded, _ := json.Marshal(detail)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
		ProxyID: opsUpstreamProxyID(account), ProxyName: opsUpstreamProxyName(account),
		Kind: "stream_protocol_error", Stage: "inference", Scope: "account", Reason: code,
		Message: message, Detail: string(encoded),
	})
}
