package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func auditTestSession(ws bool) *ResponseAuditSession {
	_, s := WithResponseAudit(context.Background(), nil, ResponseAudit{RequestID: "same-client-controlled-id", APIKeyID: 7, UserID: 3, Protocol: "responses"}, ws)
	return s
}
func auditSSE(s *ResponseAuditSession, data string) {
	p := []byte("data: " + data + "\n\n")
	s.Write(p, len(p), nil, true)
}
func TestResponseAuditProtocolsAndOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		frames []string
		status string
		field  string
	}{
		{"empty-with-billed-input", []string{`{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":100}}}`, `{"type":"message_stop"}`}, "empty", ""},
		{"text-then-eof", []string{`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`}, "partial_failure", "text"},
		{"reasoning-only", []string{`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"let me think"}}`, `{"type":"message_stop"}`}, "success", "reasoning"},
		{"empty-tool-arguments-valid", []string{`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"lookup","input":{}}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_stop"}`}, "success", "tool"},
		{"fragmented-tool", []string{`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"lookup","input":{}}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"x\"}"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_stop"}`}, "success", "tool"},
		{"incomplete-tool", []string{`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"lookup","input":{}}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":"}}`}, "failed", ""},
		{"responses-empty-with-usage", []string{`{"type":"response.completed","response":{"output":[],"usage":{"input_tokens":10,"output_tokens":0}}}`}, "empty", ""},
		{"refusal-is-output", []string{`{"type":"response.refusal.delta","delta":"I cannot do that"}`, `{"type":"response.completed","response":{"output":[]}}`}, "success", "text"},
		{"http-200-stream-failure", []string{`{"type":"response.failed","response":{"error":{"code":"upstream_error"}}}`}, "failed", ""},
		{"chat", []string{`{"choices":[{"delta":{"content":"hello"},"finish_reason":null}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`, `[DONE]`}, "success", "text"},
		{"chat-tool-no-prose", []string{`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{\"query\":\"x\"}"}}]}}]}`, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`, `[DONE]`}, "success", "tool"},
		{"opaque-output", []string{`{"type":"response.completed","response":{"output":[{"type":"new_provider_output","data":"opaque"}]}}`}, "unknown", ""},
		{"heartbeat-only", []string{`{"type":"ping"}`}, "failed", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := auditTestSession(false)
			for _, f := range c.frames {
				auditSSE(s, f)
			}
			s.Finish(200, false)
			require.Equal(t, c.status, s.current.Status)
			switch c.field {
			case "text":
				require.True(t, s.current.TextWritten)
			case "reasoning":
				require.True(t, s.current.ReasoningWritten)
			case "tool":
				require.True(t, s.current.ToolWritten)
			}
			if c.field == "" {
				require.False(t, s.current.TextWritten || s.current.ReasoningWritten || s.current.ToolWritten)
			}
		})
	}
}
func TestResponseAuditNonStreamingAndWriteFailure(t *testing.T) {
	p := []byte(`{"id":"resp_1","object":"response","status":"completed","error":null,"output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}],"usage":{"output_tokens":0}}`)
	s := auditTestSession(false)
	s.Write(p, len(p), nil, false)
	s.Finish(200, false)
	require.Equal(t, "success", s.current.Status)
	require.True(t, s.current.TextWritten)
	s = auditTestSession(false)
	s.Upstream(p, "")
	s.Write(p, 0, errors.New("broken pipe"), false)
	s.Finish(200, true)
	require.Equal(t, "failed", s.current.Status)
	require.True(t, s.current.UpstreamContentSeen)
	require.False(t, s.current.TextWritten)
	require.True(t, s.current.WriteFailed)
	s = auditTestSession(false)
	auditSSE(s, `{"type":"response.output_text.delta","delta":"already returned"}`)
	s.Write([]byte("partial"), 0, errors.New("broken pipe"), true)
	s.Finish(200, true)
	require.Equal(t, "partial_failure", s.current.Status)
}
func TestResponseAuditFragmentationLimitsAndUniqueIdentity(t *testing.T) {
	s := auditTestSession(false)
	p := []byte("event: content_block_delta\r\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"你好\"}}\r\n\r\ndata: {\"type\":\"message_stop\"}\r\n\r\n")
	for _, b := range p {
		s.Write([]byte{b}, 1, nil, true)
	}
	s.Finish(200, false)
	require.Equal(t, "success", s.current.Status)
	a := auditTestSession(false)
	b := auditTestSession(false)
	require.NotEqual(t, a.current.AuditRequestID, b.current.AuditRequestID)
	s = auditTestSession(false)
	big := []byte(strings.Repeat("x", responseAuditMaxBytes+1))
	s.Write(big, len(big), nil, false)
	s.Finish(200, false)
	require.Equal(t, "unknown", s.current.Status)
	e := auditEvidence{}
	s.toolFragment(&e, "0", "lookup", "call_0", strings.Repeat("x", responseAuditMaxBytes), false)
	s.toolFragment(&e, "1", "lookup", "call_1", "x", false)
	require.True(t, e.opaque)
	require.LessOrEqual(t, e.argsBytes, responseAuditMaxBytes)
}
func TestResponseAuditWebSocketTurnsAndTerminalWrite(t *testing.T) {
	s := auditTestSession(true)
	s.LinkUsage(7, "resp_1", "resp_1")
	s.WSWrite([]byte(`{"type":"response.output_text.delta","delta":"hello"}`), nil)
	s.WSWrite([]byte(`{"type":"response.completed","response":{"id":"resp_1","output":[]}}`), nil)
	require.Equal(t, "success", s.current.Status)
	id := s.current.AuditRequestID
	s.BeginTurn(2, "model-2")
	require.False(t, s.current.TextWritten)
	require.Equal(t, id, s.current.AuditRequestID)
	require.Empty(t, s.current.UsageRequestID)
	s.WSWrite([]byte(`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"never delivered"}]}]}}`), errors.New("broken pipe"))
	require.Equal(t, "failed", s.current.Status)
	require.True(t, s.current.UpstreamContentSeen)
	require.False(t, s.current.TextWritten)
	require.False(t, s.current.TerminalWritten)
	s.BeginTurn(3, "model-3")
	s.Finish(101, true)
	require.Equal(t, "failed", s.current.Status)
	require.Equal(t, 3, s.current.Turn)
}
