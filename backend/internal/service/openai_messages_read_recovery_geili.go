package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
)

// Classify only errors encountered while reading an upstream body. Local
// cancellation and response-size enforcement must never create another attempt.
func geiliMessagesStreamReadError(c *gin.Context, err error) error {
	var ctx context.Context
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	if !errors.Is(err, bufio.ErrTooLong) && shouldClassifyOpenAIUpstreamStreamReadError(err, ctx) {
		return newOpenAIUpstreamStreamReadError(err)
	}
	return fmt.Errorf("stream usage incomplete: %w", err)
}

// A frame without its final blank line may still prove a built-in operation
// started. Observe that evidence before classifying a read failure; never turn
// the pending frame into successful client output or a synthetic terminal.
func (s *OpenAIGatewayService) observePendingCompatReadGeili(c *gin.Context, parser *openAICompatSSEFrameParser, usage *OpenAIUsage) {
	frame, ok := parser.Finish()
	geiliObserveUpstreamOperation(c, []byte(frame.Data), frame.EventType)
	if ok {
		payload := openAICompatPayloadWithEventType(frame.Data, frame.EventType)
		s.parseSSEUsageBytesWithType([]byte(payload), effectiveOpenAISSEEventType([]byte(payload), frame.EventType), usage)
	}
}

// Failed body reads with neither delivered content nor supplier metering do not
// create empty accounting receipts. Preserve reported usage and partial output.
func geiliMessagesReadFailureResult(c *gin.Context, result *OpenAIForwardResult) *OpenAIForwardResult {
	if result == nil || c == nil || result.Usage != (OpenAIUsage{}) || result.SearchCount != 0 || result.ImageCount != 0 {
		return result
	}
	if writer, ok := c.Writer.(*geiliTextForwardWriter); ok && !writer.semantic {
		return nil
	}
	return result
}
