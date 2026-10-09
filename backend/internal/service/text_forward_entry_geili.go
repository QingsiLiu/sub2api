package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// geili hook: preserve the public forwarding interfaces and stage each account
// attempt independently. A replayable failure deliberately carries no billable
// result; existing partial-output failure billing remains unchanged.
func (s *GatewayService) Forward(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest) (result *ForwardResult, err error) {
	stream := parsed != nil && parsed.Stream
	recordCtx := ctx
	model := ""
	if parsed != nil {
		model = parsed.Model
	}
	finish := BeginTextForwardGuard(c, stream)
	defer func() {
		if result != nil && result.ClientDisconnect {
			GeiliMarkTextClientDisconnect(c)
		}
		err = finish(err)
		s.recordTextSupplierFailureGeili(recordCtx, c, account, model, err)
		var f *UpstreamFailoverError
		if errors.As(err, &f) && f.SafeToFailoverAfterWrite {
			result = nil
		}
	}()
	ctx, cancel := RequestRecoveryContext(ctx)
	defer cancel()
	return s.forwardGeiliAttempt(ctx, c, account, parsed)
}

func (s *OpenAIGatewayService) Forward(ctx context.Context, c *gin.Context, account *Account, body []byte) (result *OpenAIForwardResult, err error) {
	finish := BeginTextForwardGuard(c, gjson.GetBytes(body, "stream").Bool())
	defer func() {
		if result != nil {
			if result.ClientDisconnect {
				GeiliMarkTextClientDisconnect(c)
			}
			if err == nil && result.UpstreamTerminalEvent == "response.failed" {
				geiliAcceptTextFailureTerminal(c)
			}
		}
		err = finish(err)
		s.recordTextSupplierFailureGeili(c, account, gjson.GetBytes(body, "model").String(), err)
		var f *UpstreamFailoverError
		if errors.As(err, &f) && f.SafeToFailoverAfterWrite {
			result = nil
		}
	}()
	ctx, cancel := RequestRecoveryContext(ctx)
	defer cancel()
	return s.forwardGeiliAttempt(ctx, c, account, body)
}

func (s *OpenAIGatewayService) ForwardAsAnthropic(ctx context.Context, c *gin.Context, account *Account, body []byte, promptCacheKey, defaultMappedModel string) (result *OpenAIForwardResult, err error) {
	finish := BeginTextForwardGuard(c, gjson.GetBytes(body, "stream").Bool())
	defer func() {
		if result != nil {
			if result.ClientDisconnect {
				GeiliMarkTextClientDisconnect(c)
			}
			if err == nil && result.UpstreamTerminalEvent == "response.failed" {
				geiliAcceptTextFailureTerminal(c)
			}
		}
		err = finish(err)
		s.recordTextSupplierFailureGeili(c, account, gjson.GetBytes(body, "model").String(), err)
		var f *UpstreamFailoverError
		if errors.As(err, &f) && f.SafeToFailoverAfterWrite {
			result = nil
		}
	}()
	ctx, cancel := RequestRecoveryContext(ctx)
	defer cancel()
	return s.forwardAsAnthropicGeiliAttempt(ctx, c, account, body, promptCacheKey, defaultMappedModel)
}

func (s *OpenAIGatewayService) forwardChatWithGeiliGuard(ctx context.Context, c *gin.Context, account *Account, body []byte, promptCacheKey, defaultMappedModel string) (result *OpenAIForwardResult, err error) {
	finish := BeginTextForwardGuard(c, gjson.GetBytes(body, "stream").Bool())
	defer func() {
		if result != nil {
			if result.ClientDisconnect {
				GeiliMarkTextClientDisconnect(c)
			}
			if err == nil && result.UpstreamTerminalEvent == "response.failed" {
				geiliAcceptTextFailureTerminal(c)
			}
		}
		err = finish(err)
		s.recordTextSupplierFailureGeili(c, account, gjson.GetBytes(body, "model").String(), err)
		var f *UpstreamFailoverError
		if errors.As(err, &f) && f.SafeToFailoverAfterWrite {
			result = nil
		}
	}()
	ctx, cancel := RequestRecoveryContext(ctx)
	defer cancel()
	return s.forwardAsChatCompletions(ctx, c, account, body, promptCacheKey, defaultMappedModel, false)
}

func takeTextSupplierFailureGeili(c *gin.Context, err error) *geiliUpstreamPayloadFailure {
	if c == nil || err == nil {
		return nil
	}
	value, _ := c.Get("geili_supplier_failure")
	failure, _ := value.(*geiliUpstreamPayloadFailure)
	c.Set("geili_supplier_failure", nil)
	return failure
}

func (s *GatewayService) recordTextSupplierFailureGeili(ctx context.Context, c *gin.Context, account *Account, model string, err error) {
	failure := takeTextSupplierFailureGeili(c, err)
	if failure == nil || account == nil {
		return
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
		UpstreamStatusCode: failure.status, UpstreamRequestID: failure.headers.Get("x-request-id"), Kind: "stream_error", Message: failure.message})
	var failover *UpstreamFailoverError
	if errors.As(err, &failover) && s.rateLimitService != nil {
		response := &http.Response{StatusCode: failure.status, Header: failure.headers, Body: io.NopCloser(bytes.NewReader(failure.payload))}
		s.handleFailoverSideEffects(ctx, response, account, model)
	}
}

func (s *OpenAIGatewayService) recordTextSupplierFailureGeili(c *gin.Context, account *Account, model string, err error) {
	failure := takeTextSupplierFailureGeili(c, err)
	if failure == nil || account == nil {
		return
	}
	s.recordOpenAIStreamUpstreamError(c, account, false, failure.headers.Get("x-request-id"), "stream_error", failure.payload, failure.message)
	var failover *UpstreamFailoverError
	if errors.As(err, &failover) && s.rateLimitService != nil {
		s.handleOpenAIStreamTerminalAccountSideEffects(c, account, failure.payload, failure.message, failure.headers, model)
	}
}
