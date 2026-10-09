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
			block.Input = appendRawJSON(block.Input, event.Delta.PartialJSON)
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
	for _, block := range t.response.Content {
		if (block.Type == "tool_use" || block.Type == "server_tool_use") && !json.Valid(block.Input) {
			return errors.New("upstream stream read error: ended with invalid tool arguments")
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
		var idle <-chan time.Time
		if pump.timer != nil {
			idle = pump.timer.C
		}
		for {
			select {
			case event, ok := <-pump.events:
				if !ok {
					return "", io.EOF
				}
				pump.resetTimer()
				return event.line, event.err
			case <-idle:
				return "", errAnthropicNativeStreamIdle
			case <-canceled:
				// An already started request is drained for the provider's final usage;
				// cancellation never starts another route attempt.
				canceled = nil
				keepalive = nil
			case <-keepalive:
				if _, err := fmt.Fprint(c.Writer, ping); err != nil {
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
			if errors.Is(err, io.EOF) {
				if e := process(); e != nil {
					return tracker, e
				}
				if e := tracker.complete(); e == nil {
					return tracker, nil
				} else {
					return tracker, GeiliUpstreamReadFailure(c, e)
				}
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
