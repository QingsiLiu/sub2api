package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

// geili: 严格透传只承诺业务字节不变，不承诺单账号失败即失败。账号级错误
// （401/403/429/529/5xx）在尚未写出任何内容前仍按原样字节换号重发；
// 换号耗尽后由 handler 把最后一次上游原生状态码、正文和业务头原样回写。
func (s *GatewayService) strictAnthropicFailoverGeili(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, upstreamReq *http.Request, body []byte, requestModel string) error {
	resp.Body = io.NopCloser(bytes.NewReader(body))
	kind := "failover"
	if s.shouldRetryUpstreamError(account, resp.StatusCode) {
		kind = "retry_exhausted_failover"
	}
	if s.rateLimitService != nil {
		if kind == "failover" {
			s.handleFailoverSideEffects(ctx, resp, account, requestModel)
		} else {
			s.handleRetryExhaustedSideEffects(ctx, resp, account, requestModel)
		}
	}
	logger.LegacyPrintf("service.gateway", "[Anthropic Passthrough] Upstream error (%s): Account=%d(%s) Status=%d RequestID=%s Body=%s",
		kind, account.ID, account.Name, resp.StatusCode, resp.Header.Get("x-request-id"), truncateString(string(body), 1000))
	upstreamURL := ""
	if upstreamReq != nil {
		upstreamURL = safeUpstreamURL(upstreamReq.URL.String())
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		ProxyID:            opsUpstreamProxyID(account),
		ProxyName:          opsUpstreamProxyName(account),
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		UpstreamURL:        upstreamURL,
		Passthrough:        true,
		Kind:               kind,
		Message:            extractUpstreamErrorMessage(body),
		Detail: func() string {
			if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
				return truncateString(string(body), s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
			}
			return ""
		}(),
	})
	return &UpstreamFailoverError{
		StatusCode:             resp.StatusCode,
		ResponseBody:           append([]byte(nil), body...),
		ResponseHeaders:        resp.Header.Clone(),
		RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
		StrictPassthrough:      true,
	}
}

// WriteStrictAnthropicFailoverGeili 在换号耗尽时原样回写最后一次严格透传的上游错误。
// 已提交 HTTP 200（例如边缘心跳）时无法再改状态码，返回 false 交给流内错误处理。
func WriteStrictAnthropicFailoverGeili(c *gin.Context, failoverErr *UpstreamFailoverError, streamStarted bool) bool {
	if c == nil || failoverErr == nil || !failoverErr.StrictPassthrough || streamStarted || c.Writer.Written() {
		return false
	}
	SetOpsUpstreamError(c, failoverErr.StatusCode, sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(failoverErr.ResponseBody)), "")
	writeAnthropicPassthroughResponseHeaders(c.Writer.Header(), failoverErr.ResponseHeaders, nil)
	c.Data(failoverErr.StatusCode, failoverErr.ResponseHeaders.Get("Content-Type"), failoverErr.ResponseBody)
	return true
}

// UseClientBodyGeili switches a strict Anthropic attempt to the caller's exact
// bytes. Later refreshes keep them unnormalized; the routed model stays the
// billing and account-cooling identity.
func (p *ParsedRequest) UseClientBodyGeili(body []byte) error {
	if p == nil {
		return fmt.Errorf("parse request: empty request")
	}
	model := p.Model
	p.preserveBody = true
	if err := p.ReplaceBody(body); err != nil {
		return err
	}
	p.Model = model
	return nil
}
