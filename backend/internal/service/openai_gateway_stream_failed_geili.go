package service

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

// buildOpenAIGatewayStreamFailedSSE 生成网关自己发起的 response.failed 终止帧
// （上游读超时、读错误、单行超限等），保证 Responses 客户端拿到终止事件。
// sequence_number / created_at 始终写出：grok-build 等 serde 客户端把它们当必填字段。
func buildOpenAIGatewayStreamFailedSSE(responseID, model, code, message string) string {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		responseID = "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	response := map[string]any{
		"id":         responseID,
		"object":     "response",
		"created_at": time.Now().Unix(),
		"status":     "failed",
		"output":     []any{},
		"error": map[string]any{
			"type":    "upstream_error",
			"code":    code,
			"message": message,
		},
	}
	if model = strings.TrimSpace(model); model != "" {
		response["model"] = model
	}
	payload, err := json.Marshal(map[string]any{
		"type":            "response.failed",
		"sequence_number": 0,
		"response":        response,
	})
	if err != nil {
		payload = []byte(`{"type":"response.failed","sequence_number":0,"response":{"status":"failed","output":[],"error":{"type":"upstream_error","code":"upstream_error","message":"Upstream response failed"}}}`)
	}
	return "event: response.failed\ndata: " + string(payload) + "\n\n"
}
