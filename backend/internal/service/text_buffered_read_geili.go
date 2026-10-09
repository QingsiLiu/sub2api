package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// The synchronous client may receive an upstream SSE body. Read real bytes
// under the same idle budget as streaming calls, and stop on a real terminal
// frame rather than waiting for the supplier to close its persistent socket.
func (s *OpenAIGatewayService) readTextBufferedBodyGeili(ctx context.Context, resp *http.Response, c *gin.Context, model string) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("upstream response body is nil")
	}
	limit := resolveUpstreamResponseReadLimit(s.cfg)
	interval := anthropicStreamIntervalGeili(s.cfg, model)
	type chunk struct {
		data []byte
		err  error
	}
	events := make(chan chunk, 8)
	done := make(chan struct{})
	var lastRead atomic.Int64
	lastRead.Store(time.Now().UnixNano())
	go func() {
		defer close(events)
		buffer := make([]byte, 32*1024)
		for {
			n, err := resp.Body.Read(buffer)
			if n > 0 {
				lastRead.Store(time.Now().UnixNano())
			}
			ev := chunk{data: append([]byte(nil), buffer[:n]...), err: err}
			select {
			case events <- ev:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer close(done)
	defer func() { _ = resp.Body.Close() }()
	var tick <-chan time.Time
	var idleTimer *time.Timer
	if interval > 0 {
		idleTimer = time.NewTimer(interval)
		defer idleTimer.Stop()
		tick = idleTimer.C
	}
	if ctx == nil {
		ctx = context.Background()
	}
	canceled := ctx.Done()
	var drain <-chan time.Time
	var drainTimer *time.Timer
	defer func() {
		if drainTimer != nil {
			drainTimer.Stop()
		}
	}()
	var body, pending bytes.Buffer
	stream := isEventStreamResponse(resp.Header)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return body.Bytes(), nil
			}
			if int64(body.Len()+len(ev.data)) > limit {
				setOpsUpstreamError(c, http.StatusBadGateway, "upstream response too large", "")
				openAITooLargeError(c)
				return nil, fmt.Errorf("%w: limit=%d", ErrUpstreamResponseBodyTooLarge, limit)
			}
			_, _ = body.Write(ev.data)
			if stream {
				_, _ = pending.Write(ev.data)
				for {
					raw := pending.Bytes()
					end, delimiter := bytes.Index(raw, []byte("\n\n")), 2
					if crlf := bytes.Index(raw, []byte("\r\n\r\n")); crlf >= 0 && (end < 0 || crlf < end) {
						end, delimiter = crlf, 4
					}
					if end < 0 {
						break
					}
					frame := pending.Next(end + delimiter)
					payload := geiliSSEPayload(frame)
					kind := geiliBufferedFrameType(frame, payload)
					geiliObserveUpstreamOperation(c, payload, kind)
					switch kind {
					case "response.completed", "response.done", "response.incomplete", "response.failed", "response.cancelled", "response.canceled":
						if gjson.GetBytes(payload, "response").IsObject() {
							return body.Bytes(), nil
						}
					}
				}
			}
			if ev.err != nil {
				// SSE permits the final frame to end at EOF without a blank line.
				// A complete built-in progress payload still forbids replay.
				if stream && pending.Len() > 0 {
					frame := pending.Bytes()
					payload := geiliSSEPayload(frame)
					geiliObserveUpstreamOperation(c, payload, geiliBufferedFrameType(frame, payload))
				}
				if errors.Is(ev.err, io.EOF) {
					return body.Bytes(), nil
				}
				return nil, GeiliUpstreamReadFailure(c, newOpenAIUpstreamStreamReadError(ev.err))
			}
		case <-tick:
			remaining := interval - time.Since(time.Unix(0, lastRead.Load()))
			if remaining <= 0 {
				return nil, GeiliUpstreamReadFailure(c, errors.New("stream data interval timeout"))
			}
			idleTimer.Reset(remaining)
		case <-canceled:
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, ctx.Err()
			}
			// Preserve existing bounded usage drain when a proxy/client disconnects.
			// This never permits another account attempt for the canceled request.
			canceled = nil
			drainTimer = time.NewTimer(8 * time.Second)
			drain = drainTimer.C
		case <-drain:
			return nil, context.Canceled
		}
	}
}

func geiliBufferedFrameType(frame, payload []byte) string {
	if kind := gjson.GetBytes(payload, "type").String(); kind != "" {
		return kind
	}
	for _, line := range strings.Split(string(frame), "\n") {
		if strings.HasPrefix(line, "event:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}
	return ""
}

func geiliSyncJSONFailure(c *gin.Context, resp *http.Response, body []byte) error {
	if gjson.ValidBytes(body) && (gjson.GetBytes(body, "error").IsObject() || gjson.GetBytes(body, "response.error").IsObject()) {
		return GeiliUpstreamErrorFailure(c, body, resp.StatusCode, resp.Header)
	}
	return nil
}
