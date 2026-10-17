package service

// Anthropic-compatible relays report an exhausted upstream key or an
// unsupported model as 400/404. Neither is a defect in the client request:
// another account can complete the same bytes. Relays rotate their own keys,
// so these responses are transient and only exclude the account for the
// current request; account state follows the existing error policy.
import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Official Anthropic texts; relays may prefix them (e.g. "Provider API error: ").
var anthropicAccountQuotaMarkersGeili = []string{
	"you have reached your specified api usage limits",
	"you have reached your specified workspace api usage limits",
	"your credit balance is too low to access the anthropic api",
}

const anthropicAccountScopedMaxMessageGeili = 4096

// classifyAnthropicAccountScopedFailureGeili inspects structured error fields
// only. Echoed prompts, HTML and plain-text bodies are never evidence.
func classifyAnthropicAccountScopedFailureGeili(status int, body []byte) (reason GatewayFailureReason, matched bool) {
	switch status {
	case http.StatusBadRequest, http.StatusPaymentRequired, http.StatusNotFound:
	default:
		return "", false
	}
	if !gjson.ValidBytes(body) {
		return "", false
	}
	root := gjson.ParseBytes(body)
	errType := strings.ToLower(strings.TrimSpace(root.Get("error.type").String()))
	errCode := strings.ToLower(strings.TrimSpace(root.Get("error.code").String()))
	message := root.Get("error.message")
	if message.Type != gjson.String || len(message.String()) > anthropicAccountScopedMaxMessageGeili {
		return "", false
	}
	lower := strings.ToLower(strings.TrimSpace(message.String()))
	if errType == "model_not_found" || errCode == "model_not_found" ||
		(status == http.StatusNotFound && errType == "not_found_error" && strings.HasPrefix(lower, "model:")) {
		return "", true
	}
	if status == http.StatusNotFound {
		return "", false
	}
	for _, marker := range anthropicAccountQuotaMarkersGeili {
		if strings.Contains(lower, marker) {
			return geminiSupplierCapacityReasonGeili, true
		}
	}
	return "", false
}

// anthropicAccountScopedFailoverGeili reads at most the standard error limit
// and restores unmatched responses so existing error policies are unchanged.
func (s *GatewayService) anthropicAccountScopedFailoverGeili(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, requestedModel string) *UpstreamFailoverError {
	if s == nil || resp == nil || resp.Body == nil || account == nil || account.Platform != PlatformAnthropic {
		return nil
	}
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusPaymentRequired, http.StatusNotFound:
	default:
		return nil
	}
	body, _ := s.readUpstreamErrorBody(resp)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	reason, matched := classifyAnthropicAccountScopedFailureGeili(resp.StatusCode, body)
	if !matched {
		return nil
	}
	// Same account side effects as the non-failover path (pool mode skips them).
	s.handleFailoverSideEffects(ctx, resp, account, requestedModel)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	message := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		ProxyID:            opsUpstreamProxyID(account),
		ProxyName:          opsUpstreamProxyName(account),
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               "failover",
		Message:            message,
		Reason:             string(reason),
		Detail: func() string {
			if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
				return truncateString(string(body), s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
			}
			return ""
		}(),
	})
	failure := &UpstreamFailoverError{
		StatusCode:        resp.StatusCode,
		ResponseBody:      body,
		ResponseHeaders:   resp.Header.Clone(),
		Stage:             GatewayFailureStageInference,
		Scope:             GatewayFailureScopeAccount,
		Reason:            reason,
		NextAccountAction: NextAccountRetry,
	}
	if reason == geminiSupplierCapacityReasonGeili {
		failure.ClientStatusCode = http.StatusServiceUnavailable
		failure.ClientMessage = geminiSupplierUnavailableMessageGeili
	}
	return failure
}

// isAnthropicAccountScopedFailureGeili lets strict passthrough move the same
// native bytes to another account; the last native error is still returned.
func isAnthropicAccountScopedFailureGeili(account *Account, status int, body []byte) bool {
	if account == nil || account.Platform != PlatformAnthropic {
		return false
	}
	_, matched := classifyAnthropicAccountScopedFailureGeili(status, body)
	return matched
}
