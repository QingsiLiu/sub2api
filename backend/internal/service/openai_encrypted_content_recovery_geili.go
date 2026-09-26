package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// canRecoverOpenAIWrappedEncryptedReasoningGeili recognizes providers that replace
// invalid_encrypted_content with invalid_request_error while retaining the exact
// decryption failure message. It extends the HTTP recovery path only: do not infer
// a bad cipher from an arbitrary 400 or an unrelated explicit provider error code.
//
// This message-only fallback is deliberately narrower than the legacy exact-code
// recovery. Compaction may be the only copy of older conversation history, and a
// previous_response_id may refer to opaque server-side state. Neither can safely
// be repaired by deleting items from this request.
func canRecoverOpenAIWrappedEncryptedReasoningGeili(status int, code, message string, body []byte) bool {
	if status != http.StatusBadRequest {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "", "invalid_request_error":
	default:
		return false
	}
	message = strings.ToLower(strings.TrimSpace(message))
	if !strings.Contains(message, "encrypted content") ||
		!strings.Contains(message, "could not be verified") ||
		!strings.Contains(message, "could not be decrypted or parsed") {
		return false
	}
	if strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String()) != "" {
		return false
	}
	input := gjson.GetBytes(body, "input")
	hasReasoningCipher, hasOpaqueHistory := false, false
	check := func(item gjson.Result) {
		encrypted := item.Get("encrypted_content")
		itemType := strings.TrimSpace(item.Get("type").String())
		if itemType == "compaction" || itemType == "compaction_summary" {
			hasOpaqueHistory = true
		} else if encrypted.Type == gjson.String && encrypted.String() != "" {
			if itemType == "reasoning" {
				hasReasoningCipher = true
			} else {
				hasOpaqueHistory = true
			}
		}
	}
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			check(item)
			return !hasOpaqueHistory
		})
	} else if input.IsObject() {
		check(input)
	}
	return hasReasoningCipher && !hasOpaqueHistory
}
