package service

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// OpenAIEncryptedRecoveryErrorGeili is a pre-output encrypted-state failure.
// Forward owns the request-wide once guard and final usage settlement.
type OpenAIEncryptedRecoveryErrorGeili struct {
	Body      []byte
	Retryable bool
}

func (e *OpenAIEncryptedRecoveryErrorGeili) Error() string {
	return "invalid_encrypted_content: encrypted session state could not be decoded"
}

// ClassifyOpenAIEncryptedFailureGeili does not trust a gateway's outer HTTP status.
// Only explicit encrypted-state codes and precise decryption messages qualify.
func ClassifyOpenAIEncryptedFailureGeili(payload []byte) bool {
	if !gjson.ValidBytes(payload) {
		return false
	}
	for _, path := range []string{"error", "response.error"} {
		value := gjson.GetBytes(payload, path)
		if !value.IsObject() {
			continue
		}
		code := strings.ToLower(strings.TrimSpace(value.Get("code").String()))
		if code == "invalid_encrypted_content" {
			return true
		}
		message := strings.ToLower(strings.TrimSpace(value.Get("message").String()))
		switch code {
		case "", "invalid_request_error", "thinking_signature_invalid", "upstream_error":
		default:
			continue
		}
		if strings.Contains(message, "encrypted function output content") &&
			(strings.Contains(message, "could not be decrypted") || strings.Contains(message, "could not be decoded")) {
			return true
		}
		if strings.Contains(message, "encrypted content") && strings.Contains(message, "could not be verified") &&
			strings.Contains(message, "could not be decrypted or parsed") {
			return true
		}
	}
	return false
}

// PrepareOpenAIEncryptedReasoningRecoveryGeili never drops opaque history. Raw
// JSON edits preserve large integers and every unrelated item byte for byte.
func PrepareOpenAIEncryptedReasoningRecoveryGeili(body []byte) ([]byte, bool, string) {
	if !gjson.ValidBytes(body) {
		return body, false, "invalid_json"
	}
	payload := openAIRequestPayloadView(body)
	if strings.TrimSpace(payload.Get("previous_response_id").String()) != "" {
		return body, false, "previous_response_state"
	}
	// Built-in tools can execute before their progress frame arrives. A client
	// function/custom definition is safe because we have not emitted its call.
	unsafeTools := false
	payload.Get("tools").ForEach(func(_, tool gjson.Result) bool {
		kind := tool.Get("type").String()
		unsafeTools = kind != "function" && kind != "custom"
		return !unsafeTools
	})
	if unsafeTools {
		return body, false, "server_tools_enabled"
	}
	input := payload.Get("input")
	if !input.IsArray() && !input.IsObject() {
		return body, false, "missing_reasoning_cipher"
	}
	prefix := "input"
	// Native WebSocket envelopes may carry the actual request under response.
	if gjson.GetBytes(body, "response").IsObject() && payload.Raw == gjson.GetBytes(body, "response").Raw {
		prefix = "response.input"
	}
	paths := []string{}
	unsafe := ""
	check := func(item gjson.Result, path string) {
		if !item.IsObject() {
			unsafe = "opaque_item_reference"
			return
		}
		kind := strings.TrimSpace(item.Get("type").String())
		switch kind {
		case "compaction", "compaction_summary", "compaction_trigger":
			unsafe = "opaque_compaction"
			return
		}
		// Every nested encryption-bearing field must be understood before a retry.
		var walk func(gjson.Result, bool)
		walk = func(value gjson.Result, root bool) {
			if unsafe != "" {
				return
			}
			if value.IsArray() {
				value.ForEach(func(_, child gjson.Result) bool { walk(child, false); return unsafe == "" })
			} else if value.IsObject() {
				nestedKind := strings.ToLower(value.Get("type").String())
				if nestedKind == "encrypted_content" || nestedKind == "compaction" || nestedKind == "compaction_summary" {
					unsafe = "opaque_encrypted_state"
					return
				}
				value.ForEach(func(key, child gjson.Result) bool {
					name := strings.ToLower(key.String())
					if strings.Contains(name, "encrypted") && child.Type != gjson.Null && child.Raw != `""` {
						if !(root && kind == "reasoning" && name == "encrypted_content" && child.Type == gjson.String) {
							unsafe = "opaque_encrypted_state"
							return false
						}
					}
					walk(child, false)
					return unsafe == ""
				})
			}
		}
		walk(item, true)
		if unsafe == "" && kind == "reasoning" && item.Get("encrypted_content").Type == gjson.String && item.Get("encrypted_content").String() != "" {
			paths = append(paths, path)
		}
	}
	if input.IsArray() {
		input.ForEach(func(index, item gjson.Result) bool {
			check(item, prefix+"."+strconv.Itoa(int(index.Int())))
			return unsafe == ""
		})
	} else {
		check(input, prefix)
	}
	if unsafe != "" {
		return body, false, unsafe
	}
	if len(paths) == 0 {
		return body, false, "missing_reasoning_cipher"
	}
	recovered := append([]byte(nil), body...)
	for _, path := range paths {
		var err error
		recovered, err = sjson.DeleteBytes(recovered, path+".encrypted_content")
		if err != nil {
			return body, false, "invalid_json"
		}
		recovered, err = sjson.DeleteBytes(recovered, path+".id")
		if err != nil {
			return body, false, "invalid_json"
		}
	}
	return recovered, true, "reasoning_cipher_removed"
}

func newOpenAIEncryptedStreamRecoveryGeili(c *gin.Context, payload []byte, usage *OpenAIUsage, outputStarted bool) *OpenAIEncryptedRecoveryErrorGeili {
	if c == nil || c.Request == nil || outputStarted || !OpenAIEncryptedRouteProtectedGeili(c.Request.Context()) || !ClassifyOpenAIEncryptedFailureGeili(payload) {
		return nil
	}
	retryable := OpenAIEncryptedRouteRecoveryEnabledGeili(c.Request.Context()) && c.Request.Context().Err() == nil &&
		gjson.GetBytes(payload, "type").String() != "error"
	if usage != nil && *usage != (OpenAIUsage{}) {
		retryable = false
	}
	if state := requestRecoveryFromContext(c.Request.Context()); state != nil {
		state.mu.Lock()
		retryable = retryable && !state.replayForbidden
		state.mu.Unlock()
	}
	if writer, ok := c.Writer.(*geiliTextForwardWriter); ok && writer.unreplayable {
		retryable = false
	}
	return &OpenAIEncryptedRecoveryErrorGeili{Body: append([]byte(nil), payload...), Retryable: retryable}
}

// RenderOpenAIEncryptedFailureGeili makes the error truthful even when the
// supplier wrapped it in a 502. A committed heartbeat must end as SSE, not JSON.
func RenderOpenAIEncryptedFailureGeili(c *gin.Context, payload []byte) error {
	normalized := []byte(`{"error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"上游无法解析或解密会话状态；请恢复原会话线路，勿原样重试"}}`)
	failure := GeiliUpstreamErrorFailure(c, normalized, http.StatusBadRequest, nil)
	if c == nil || c.Writer == nil {
		return failure
	}
	MarkResponseCommitted(c)
	if writeOpenAICompactSSEBridge(c, http.StatusBadRequest, normalized) {
		return failure
	}
	committed := c.Writer.Written()
	if writer, ok := c.Writer.(*geiliTextForwardWriter); ok {
		committed = committed || writer.ResponseWriter.Written()
	}
	if committed {
		GeiliWriteStreamFailure(c, failure)
	} else {
		c.Data(http.StatusBadRequest, "application/json; charset=utf-8", normalized)
	}
	return failure
}

// prepareOpenAIEncryptedHTTPRecoveryGeili enforces request-wide eligibility.
// Failed input with known metering is returned for settlement, never replayed.
func prepareOpenAIEncryptedHTTPRecoveryGeili(c *gin.Context, body, failure []byte, metering *OpenAIUsage, eligible bool) ([]byte, bool) {
	if c == nil || c.Request == nil || !eligible || !OpenAIEncryptedRouteRecoveryEnabledGeili(c.Request.Context()) || !ClassifyOpenAIEncryptedFailureGeili(failure) {
		return body, false
	}
	if metering != nil && *metering != (OpenAIUsage{}) {
		return body, false
	}
	if writer, ok := c.Writer.(*geiliTextForwardWriter); ok && (writer.semantic || writer.unreplayable) {
		return body, false
	}
	next, changed, _ := PrepareOpenAIEncryptedReasoningRecoveryGeili(body)
	if !changed || !ClaimOpenAIEncryptedRecoveryGeili(c.Request.Context()) {
		return body, false
	}
	return next, true
}

// encryptedOutputDeliveredGeili respects the outer first-output staging guard.
// A successful write to that guard alone does not mean the client received it.
func encryptedOutputDeliveredGeili(c *gin.Context) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	if writer, ok := c.Writer.(*geiliTextForwardWriter); ok {
		return writer.semantic && writer.writeErr == nil && !writer.clientDisconnected
	}
	return c.Writer.Written()
}

func queuedEncryptedOutputGeili(ctx context.Context, payload []byte) bool {
	if encryptedRouteStateGeili(ctx) == nil {
		return false
	}
	event := gjson.GetBytes(payload, "type").String()
	var items gjson.Result
	switch event {
	case "response.output_item.done":
		items = gjson.GetBytes(payload, "item")
	case "response.completed", "response.done":
		items = gjson.GetBytes(payload, "response.output")
	default:
		return false
	}
	return len(encryptedDigestsGeili([]byte(`{"input":`+items.Raw+`}`))) > 0
}

// RefreshOpenAIEncryptedOutputOriginGeili runs immediately before each actual
// send so a compact model retry cannot attribute fresh ciphers to the first model.
func RefreshOpenAIEncryptedOutputOriginGeili(ctx context.Context, c *gin.Context, account *Account, upstreamModel string) {
	state := encryptedRouteStateGeili(ctx)
	if state == nil {
		return
	}
	groupID := getOpenAIGroupIDFromContext(c)
	if groupID <= 0 {
		state.mu.Lock()
		if state.origin != nil {
			groupID = state.origin.GroupID
		}
		state.mu.Unlock()
	}
	if groupID > 0 {
		WithOpenAIEncryptedActualOutputOriginGeili(ctx, &groupID, account, upstreamModel)
	}
}
