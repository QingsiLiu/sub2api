package service

import (
	"encoding/json"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Strip the entire bound thinking history after a validation failure. A later
// block can depend on an earlier invalid block. Keep raw tool JSON (including
// large integers), user content, and the model's current thinking configuration.
func stripClaude55ThinkingHistoryForRetry(body []byte) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body
	}
	outMessages := make([]json.RawMessage, 0, len(messages.Array()))
	modified := false
	for _, msg := range messages.Array() {
		raw := []byte(msg.Raw)
		content := msg.Get("content")
		if content.IsArray() {
			blocks := make([]json.RawMessage, 0, len(content.Array()))
			changed := false
			for _, block := range content.Array() {
				switch block.Get("type").String() {
				case "thinking", "redacted_thinking":
					changed = true
				default:
					blocks = append(blocks, json.RawMessage(block.Raw))
				}
			}
			if changed {
				if len(blocks) == 0 {
					blocks = append(blocks, json.RawMessage(`{"type":"text","text":"(assistant content removed)"}`))
				}
				encoded, err := json.Marshal(blocks)
				if err != nil {
					return body
				}
				raw, err = sjson.SetRawBytes(raw, "content", encoded)
				if err != nil {
					return body
				}
				modified = true
			}
		}
		outMessages = append(outMessages, json.RawMessage(raw))
	}
	if !modified {
		return body
	}
	encoded, err := json.Marshal(outMessages)
	if err != nil {
		return body
	}
	out, err := sjson.SetRawBytes(body, "messages", encoded)
	if err != nil {
		return body
	}
	return out
}
