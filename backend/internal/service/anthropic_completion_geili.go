package service

// Anthropic converters must validate the upstream stream before synthesizing a
// terminal event. EOF and usage alone do not mean the response completed.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type anthropicCompletionGeili struct {
	response      *apicompat.AnthropicResponse
	usage         ClaudeUsage
	open          map[int]bool
	blocks        map[int]int
	toolArguments map[int]*anthropicToolArgumentsGeili
	stopped       bool
	semantic      bool
	readErr       error
	retainContent bool
	syntheticStop bool
}

func anthropicEventHasSemanticOutputGeili(event *apicompat.AnthropicStreamEvent) bool {
	if event == nil {
		return false
	}
	if block := event.ContentBlock; event.Type == "content_block_start" && block != nil {
		return block.Text != "" || block.Thinking != "" || block.Type == "tool_use" || block.Type == "server_tool_use" || block.Type == "redacted_thinking"
	}
	if delta := event.Delta; event.Type == "content_block_delta" && delta != nil {
		return delta.Text != "" || delta.Thinking != "" || delta.PartialJSON != ""
	}
	return false
}

func (t *anthropicCompletionGeili) observe(event *apicompat.AnthropicStreamEvent) error {
	if t.open == nil {
		t.open = make(map[int]bool)
		t.blocks = make(map[int]int)
		t.toolArguments = make(map[int]*anthropicToolArgumentsGeili)
	}
	switch event.Type {
	case "message_start":
		if event.Message == nil || t.response != nil {
			return errors.New("upstream stream read error: invalid message_start")
		}
		t.response = event.Message
		mergeAnthropicUsage(&t.usage, event.Message.Usage)
	case "content_block_start":
		if t.response == nil || event.Index == nil || *event.Index < 0 || event.ContentBlock == nil || t.open[*event.Index] {
			return errors.New("upstream stream read error: invalid content_block_start")
		}
		index := *event.Index
		t.blocks[index] = len(t.response.Content)
		t.open[index] = true
		block := event.ContentBlock
		t.semantic = t.semantic || block.Text != "" || block.Thinking != "" || block.Type == "tool_use" || block.Type == "server_tool_use" || block.Type == "redacted_thinking"
		stored := *block
		if block.Type == "tool_use" || block.Type == "server_tool_use" {
			arguments := &anthropicToolArgumentsGeili{seed: append(json.RawMessage(nil), block.Input...)}
			t.toolArguments[index] = arguments
			stored.Input = arguments.input()
		}
		if !t.retainContent {
			stored.Text, stored.Thinking, stored.Signature = "", "", ""
		}
		t.response.Content = append(t.response.Content, stored)
	case "content_block_delta":
		if t.response == nil || event.Index == nil || !t.open[*event.Index] || event.Delta == nil {
			return errors.New("upstream stream read error: invalid content_block_delta")
		}
		block := &t.response.Content[t.blocks[*event.Index]]
		switch event.Delta.Type {
		case "text_delta":
			if t.retainContent {
				block.Text += event.Delta.Text
			}
			t.semantic = t.semantic || event.Delta.Text != ""
		case "thinking_delta":
			if t.retainContent {
				block.Thinking += event.Delta.Thinking
			}
			t.semantic = t.semantic || event.Delta.Thinking != ""
		case "signature_delta":
			if t.retainContent {
				block.Signature += event.Delta.Signature
			}
		case "input_json_delta":
			arguments := t.toolArguments[*event.Index]
			if arguments == nil {
				return errors.New("upstream stream read error: input_json_delta outside tool block")
			}
			arguments.append(event.Delta.PartialJSON)
			block.Input = arguments.input()
		}
	case "content_block_stop":
		if event.Index == nil || !t.open[*event.Index] {
			return errors.New("upstream stream read error: invalid content_block_stop")
		}
		delete(t.open, *event.Index)
	case "message_delta":
		if event.Usage != nil {
			mergeAnthropicUsage(&t.usage, *event.Usage)
		}
		if event.Delta != nil && event.Delta.StopReason != "" && t.response != nil {
			t.response.StopReason = apicompat.AnthropicStopReasonPtr(event.Delta.StopReason)
		}
	case "message_stop":
		if err := t.complete(); err != nil {
			return err
		}
		t.stopped = true
	}
	return nil
}

func (t *anthropicCompletionGeili) complete() error {
	if t.response == nil || apicompat.AnthropicStopReasonString(t.response.StopReason) == "" {
		return errors.New("upstream stream ended without a terminal response event: missing terminal event")
	}
	if len(t.open) != 0 {
		return errors.New("upstream stream read error: ended with an open content block")
	}
	for position, block := range t.response.Content {
		if (block.Type == "tool_use" || block.Type == "server_tool_use") && !json.Valid(block.Input) {
			index := position
			for candidate, storedPosition := range t.blocks {
				if storedPosition == position {
					index = candidate
					break
				}
			}
			return newAnthropicToolArgumentsDiagnosticGeili(index, block, t.toolArguments[index], t.readErr)
		}
	}
	return nil
}

func anthropicStreamIntervalGeili(cfg *config.Config, model string) time.Duration {
	if cfg == nil || cfg.Gateway.StreamDataIntervalTimeout <= 0 {
		return 0
	}
	return longThinkingStreamInterval(cfg, model, time.Duration(cfg.Gateway.StreamDataIntervalTimeout)*time.Second)
}

// readAnthropicCompletionGeili handles entire SSE events, including data-only,
// compact and multi-line frames. A compatible EOF is accepted only after a stop
// reason, closed content blocks and valid tool arguments have been observed.
func readAnthropicCompletionGeili(c *gin.Context, resp *http.Response, cfg *config.Config, model string, stream bool, onEvent func(*apicompat.AnthropicStreamEvent, string) error, keepalivePayload ...string) (*anthropicCompletionGeili, error) {
	tracker := &anthropicCompletionGeili{retainContent: !stream}
	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if cfg != nil && cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	var preOutput atomic.Bool
	preOutput.Store(true)
	scanner.Split(openAIFirstOutputDynamicScanLines(&preOutput))
	pump := newAnthropicNativeLinePump(scanner, anthropicStreamIntervalGeili(cfg, model))
	defer pump.stop()
	defer func() { _ = resp.Body.Close() }()
	var keepalive <-chan time.Time
	if stream && cfg != nil && cfg.Gateway.StreamKeepaliveInterval > 0 {
		ticker := time.NewTicker(time.Duration(cfg.Gateway.StreamKeepaliveInterval) * time.Second)
		defer ticker.Stop()
		keepalive = ticker.C
	}
	requestContext := context.Background()
	if c != nil && c.Request != nil {
		requestContext = c.Request.Context()
	}
	canceled := requestContext.Done()
	ping := ": ping\n\n"
	if len(keepalivePayload) > 0 {
		ping = keepalivePayload[0]
	}
	next := func() (string, error) {
		for {
			if geiliTextClientDisconnected(c) {
				GeiliMarkTextClientDisconnect(c)
				canceled, keepalive = nil, nil
				pump.enableInterval(anthropicCompatDisconnectDrainInterval)
			}
			var idle <-chan time.Time
			if pump.timer != nil {
				idle = pump.timer.C
			}
			select {
			case event, ok := <-pump.events:
				if !ok {
					return "", io.EOF
				}
				pump.resetTimer()
				return event.line, event.err
			case <-idle:
				// Match the upstream pump: queued data wins an expired timer
				// when conversion or downstream processing took time.
				select {
				case event, ok := <-pump.events:
					return pump.deliver(event, ok)
				default:
					return "", errAnthropicNativeStreamIdle
				}
			case <-canceled:
				// An already started request is drained for the provider's final usage;
				// cancellation never starts another route attempt.
				GeiliMarkTextClientDisconnect(c)
				canceled, keepalive = nil, nil
				pump.enableInterval(anthropicCompatDisconnectDrainInterval)
			case <-keepalive:
				if _, err := fmt.Fprint(c.Writer, ping); err != nil {
					GeiliMarkTextClientDisconnect(c)
					keepalive = nil
					continue
				}
				c.Writer.Flush()
			}
		}
	}
	var eventName string
	var data []string
	var frame strings.Builder
	process := func() error {
		if len(data) == 0 {
			raw := frame.String()
			frame.Reset()
			eventName = ""
			if onEvent != nil && strings.TrimSpace(raw) != "" {
				return onEvent(&apicompat.AnthropicStreamEvent{Type: "ping"}, raw)
			}
			return nil
		}
		payload := strings.Join(data, "\n")
		raw := frame.String()
		data = nil
		frame.Reset()
		eventType := eventName
		eventName = ""
		if payload == "[DONE]" {
			if err := tracker.complete(); err != nil {
				return GeiliUpstreamReadFailure(c, err)
			}
			tracker.stopped = true
			tracker.syntheticStop = true
			if onEvent != nil {
				return onEvent(&apicompat.AnthropicStreamEvent{Type: "ping"}, raw)
			}
			return nil
		}
		if !json.Valid([]byte(payload)) {
			return GeiliUpstreamReadFailure(c, errors.New("upstream stream read error: invalid JSON event"))
		}
		if eventType == "error" || gjson.Get(payload, "type").String() == "error" || gjson.Get(payload, "error").Exists() {
			if geiliTextClientDisconnected(c) && tracker.usage.hasObservedTokens() {
				tracker.stopped = true
				return nil
			}
			return GeiliUpstreamErrorFailure(c, []byte(payload), resp.StatusCode, resp.Header)
		}
		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return GeiliUpstreamReadFailure(c, fmt.Errorf("upstream stream read error: %w", err))
		}
		if event.Type == "" {
			event.Type = eventType
		}
		if err := tracker.observe(&event); err != nil {
			return GeiliUpstreamReadFailure(c, err)
		}
		geiliObserveUpstreamOperation(c, []byte(payload))
		preOutput.Store(!tracker.semantic)
		if onEvent != nil {
			return onEvent(&event, raw)
		}
		return nil
	}
	for {
		line, err := next()
		if err != nil {
			tracker.readErr = err
			// geili hook: accept a fully validated final event even when the peer
			// omits the final empty SSE line and then stalls or drops transport.
			if frame.Len() > 0 {
				if e := process(); e != nil {
					return tracker, e
				}
			}
			if e := tracker.complete(); e == nil {
				return tracker, nil
			}
			// Keep observed provider usage after a client disconnect, bounded by
			// the configured read interval (or the upstream fallback interval).
			if geiliTextClientDisconnected(c) && tracker.usage.hasObservedTokens() {
				GeiliMarkTextClientDisconnect(c)
				return tracker, nil
			}
			if errors.Is(err, io.EOF) {
				return tracker, GeiliUpstreamReadFailure(c, tracker.complete())
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return tracker, err
			}
			if errors.Is(err, bufio.ErrTooLong) {
				return tracker, err
			}
			return tracker, GeiliUpstreamReadFailure(c, fmt.Errorf("upstream stream read error: %w", err))
		}
		maxFrameSize := openAIFirstOutputStageMaxBytes
		if tracker.semantic {
			maxFrameSize = maxLineSize
		}
		if frame.Len()+len(line)+1 > maxFrameSize {
			return tracker, GeiliUpstreamReadFailure(c, errors.New("upstream SSE event exceeds staging limit"))
		}
		_, _ = frame.WriteString(line)
		_ = frame.WriteByte('\n')
		if line == "" {
			if err := process(); err != nil {
				return tracker, err
			}
			if tracker.stopped {
				return tracker, nil
			}
			continue
		}
		if value, ok := parseAnthropicSSEField(line, "event"); ok {
			eventName = value
		} else if value, ok := parseAnthropicSSEField(line, "data"); ok {
			data = append(data, value)
		}
	}
}

func anthropicFrameDataGeili(frame string) string {
	var data []string
	for _, line := range strings.Split(frame, "\n") {
		if value, ok := parseAnthropicSSEField(line, "data"); ok {
			data = append(data, value)
		}
	}
	return strings.Join(data, "\n")
}

func validateAnthropicJSONGeili(c *gin.Context, resp *http.Response, body []byte) error {
	if gjson.GetBytes(body, "error").Exists() || gjson.GetBytes(body, "type").String() == "error" {
		return GeiliUpstreamErrorFailure(c, body, resp.StatusCode, resp.Header)
	}
	var message apicompat.AnthropicResponse
	if err := json.Unmarshal(body, &message); err != nil {
		return GeiliUpstreamReadFailure(c, fmt.Errorf("invalid upstream JSON response: %w", err))
	}
	tracker := &anthropicCompletionGeili{response: &message}
	if err := tracker.complete(); err != nil {
		return GeiliUpstreamReadFailure(c, err)
	}
	return nil
}

// geili hook: keep pre-semantic safe failover and the strict completion reader,
// while emitting upstream's protocol-specific failure frames after output.
func anthropicCompatFailureGeili(c *gin.Context, err error) *anthropicCompatStreamFailure {
	if err == nil || geiliTextClientDisconnected(c) {
		return nil
	}
	var failover *UpstreamFailoverError
	if errors.As(err, &failover) {
		return nil
	}
	failure := &anthropicCompatStreamFailure{err: err, errType: "stream_incomplete", message: "upstream stream ended without a terminal event"}
	var payload *geiliUpstreamPayloadFailure
	switch {
	case errors.As(err, &payload):
		failure.errType = firstNonEmpty(geiliPayloadErrorField(payload.payload, "type"), "upstream_error")
		failure.message = firstNonEmpty(sanitizeUpstreamErrorMessage(payload.message), "upstream stream error")
	case errors.Is(err, errAnthropicNativeStreamIdle):
		failure.errType, failure.message = "stream_timeout", "upstream stream data interval timeout"
	case errors.Is(err, bufio.ErrTooLong):
		failure.errType, failure.message = "response_too_large", "upstream SSE line exceeded the configured limit"
	case !strings.Contains(err.Error(), "missing terminal event"):
		failure.errType, failure.message = "stream_read_error", "upstream stream disconnected: "+sanitizeStreamError(err)
	}
	return failure
}
