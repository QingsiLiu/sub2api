package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type auditRelayUpstream struct{ payload []byte }

func (c *auditRelayUpstream) ReadFrame(context.Context) (coderws.MessageType, []byte, error) {
	if c.payload == nil {
		return coderws.MessageText, nil, io.EOF
	}
	p := c.payload
	c.payload = nil
	return coderws.MessageText, p, nil
}
func (*auditRelayUpstream) WriteFrame(context.Context, coderws.MessageType, []byte) error { return nil }
func (*auditRelayUpstream) Close() error                                                  { return nil }

func TestResponseAuditRealWSWriterPreservesSettlementCallbackOrder(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "written"
		if fail {
			name = "terminal-write-failed"
		}
		t.Run(name, func(t *testing.T) {
			result := make(chan ResponseAudit, 1)
			var callbacks atomic.Int32
			var callbackBeforeAuditClosed atomic.Bool
			terminal := []byte(`{"type":"response.completed","response":{"id":"resp_audit_ws","status":"completed","model":"test","output":[{"type":"message","content":[{"type":"output_text","text":"real websocket output"}]}],"usage":{"input_tokens":10,"output_tokens":0}}}`)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
				defer cancel()
				ctx, audit := WithResponseAudit(ctx, nil, ResponseAudit{APIKeyID: 7, Model: "test"}, true)
				downstream := &openAIWSClientFrameConn{conn: conn, controlCtx: ctx}
				_, _ = openaiwsv2.Relay(ctx, downstream, &auditRelayUpstream{payload: terminal}, []byte(`{"type":"response.create","model":"test"}`), openaiwsv2.RelayOptions{
					StartClientAfterFirstDownstream: true,
					OnTurnComplete: func(turn openaiwsv2.RelayTurnResult) {
						callbacks.Add(1)
						audit.mu.Lock()
						callbackBeforeAuditClosed.Store(!audit.closed)
						audit.mu.Unlock()
						audit.LinkUsage(7, turn.RequestID, turn.RequestID)
						if fail {
							conn.CloseNow()
						}
					},
				})
				audit.Finish(101, true)
				result <- audit.current
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			client, _, err := coderws.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1), nil)
			require.NoError(t, err)
			defer client.CloseNow()
			_, wire, readErr := client.Read(ctx)
			if !fail {
				require.NoError(t, readErr)
				require.JSONEq(t, string(terminal), string(wire))
			}
			a := <-result
			require.EqualValues(t, 1, callbacks.Load())
			require.True(t, callbackBeforeAuditClosed.Load(), "existing settlement callback still precedes terminal write")
			require.Equal(t, "resp_audit_ws", a.UsageRequestID)
			if fail {
				require.Equal(t, "failed", a.Status)
				require.True(t, a.WriteFailed)
				require.True(t, a.UpstreamContentSeen)
				require.False(t, a.TerminalWritten)
			} else {
				require.Equal(t, "success", a.Status)
				require.True(t, a.TerminalWritten)
				require.True(t, a.TextWritten)
			}
		})
	}
}
