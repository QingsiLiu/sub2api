package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	geminiSupplierBalanceReasonGeili      GatewayFailureReason = "supplier_balance_exhausted"
	geminiSupplierCapacityReasonGeili     GatewayFailureReason = "supplier_capacity_unavailable"
	geminiSupplierUnavailableMessageGeili                      = "Upstream resources are temporarily unavailable"
)

// Only these observed supplier templates are eligible for request-local recovery.
// A request ID may be appended by the supplier; arbitrary trailing text is not accepted.
var geminiSupplierRequestIDSuffixGeili = regexp.MustCompile(`(?i)\s*[（(]\s*request[ _-]?id\s*[:：]\s*([a-z0-9][a-z0-9._:-]{0,191})\s*[)）]\s*$`)

func normalizeGeminiSupplierMessageGeili(message string) string {
	message = strings.TrimSpace(message)
	for {
		stripped := strings.TrimSpace(geminiSupplierRequestIDSuffixGeili.ReplaceAllString(message, ""))
		if stripped == message {
			break
		}
		message = stripped
	}
	message = strings.TrimRight(message, "。!！")
	if strings.HasSuffix(message, "重试.") {
		message = strings.TrimSuffix(message, ".")
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune(",，。!！:：;；", r) {
			return -1
		}
		return r
	}, message)
}

var (
	geminiSupplierPrechargeGeili   = regexp.MustCompile(`^预扣费额度失败用户剩余额度[¥￥$][0-9]+(?:\.[0-9]+)?需要预扣费额度[¥￥$][0-9]+(?:\.[0-9]+)?$`)
	geminiSupplierZeroBalanceGeili = regexp.MustCompile(`^用户额度不足剩余额度[¥￥$]0(?:\.0+)?$`)
)

func classifyGeminiSupplierFailureGeili(account *Account, status int, body []byte) GatewayFailureReason {
	if account == nil || account.Platform != PlatformGemini || !gjson.ValidBytes(body) {
		return ""
	}
	switch status {
	case 400, 402, 403, 429, 503:
	default:
		return ""
	}
	root := gjson.ParseBytes(body)
	// Inspect the structured error field only. Prompts, HTML and echoed input are never evidence.
	var message string
	for _, path := range []string{"error.message", "response.error.message", "message"} {
		value := root.Get(path)
		if value.Type == gjson.String {
			message = value.String()
			break
		}
	}
	if len(message) > 4096 {
		return ""
	}
	message = normalizeGeminiSupplierMessageGeili(message)
	if message == "当前号池渠道暂时无可用资源请稍后重试" {
		return geminiSupplierCapacityReasonGeili
	}
	if status != 400 && status != 402 && status != 403 {
		return ""
	}
	if geminiSupplierPrechargeGeili.MatchString(message) || geminiSupplierZeroBalanceGeili.MatchString(message) {
		return geminiSupplierBalanceReasonGeili
	}
	return ""
}

// IsGeminiSupplierUnavailableGeili identifies an explicitly classified supplier
// response, never a customer billing failure or a local scheduling veto.
func IsGeminiSupplierUnavailableGeili(failure *UpstreamFailoverError) bool {
	return failure != nil && (failure.Reason == geminiSupplierBalanceReasonGeili || failure.Reason == geminiSupplierCapacityReasonGeili)
}

func geminiSupplierFailureGeili(ctx context.Context, c *gin.Context, account *Account, status int, body []byte, headers http.Header, actualURL, model string) *UpstreamFailoverError {
	if isImageGenerationModel(model) || geminiCachedContentBoundFromContext(ctx) {
		return nil
	}
	reason := classifyGeminiSupplierFailureGeili(account, status, body)
	if reason == "" {
		return nil
	}
	balance := reason == geminiSupplierBalanceReasonGeili
	RecordGeminiSupplierFailureGeili(ctx, account, model, balance)
	requestID := headers.Get("x-request-id")
	if requestID == "" {
		requestID = headers.Get("x-goog-request-id")
	}
	message := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if requestID == "" {
		if match := geminiSupplierRequestIDSuffixGeili.FindStringSubmatch(message); len(match) == 2 {
			requestID = match[1]
		}
	}
	setOpsUpstreamError(c, status, message, "")
	// Clear stale detail from earlier attempts, even when body logging is disabled.
	if c != nil {
		c.Set(OpsUpstreamErrorDetailKey, "")
	}
	scope := GatewayFailureScope("account_model")
	if balance {
		scope = GatewayFailureScopeAccount
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
		ProxyID: opsUpstreamProxyID(account), ProxyName: opsUpstreamProxyName(account),
		UpstreamStatusCode: status, UpstreamRequestID: requestID,
		UpstreamURL: safeUpstreamURL(actualURL), Kind: "failover", Message: message,
		Stage: string(GatewayFailureStageInference), Scope: string(scope), Reason: string(reason),
	})
	return &UpstreamFailoverError{
		StatusCode: status, ResponseBody: body, ResponseHeaders: headers.Clone(),
		Stage: GatewayFailureStageInference, Scope: scope, Reason: reason,
		NextAccountAction: NextAccountRetry, ClientStatusCode: http.StatusServiceUnavailable,
		ClientMessage: geminiSupplierUnavailableMessageGeili,
	}
}

// Read at most the standard upstream error limit, restoring unmatched responses
// so existing error policies retain their original behavior. Healthy requests do no work.
func (s *GeminiMessagesCompatService) geminiSupplierFailureResponseGeili(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, actualURL, model string) (*UpstreamFailoverError, *http.Response) {
	if resp == nil || resp.StatusCode < 400 {
		return nil, resp
	}
	switch resp.StatusCode {
	case 400, 402, 403, 429, 503:
	default:
		return nil, resp
	}
	body := s.readUpstreamErrorBody(resp)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return geminiSupplierFailureGeili(ctx, c, account, resp.StatusCode, unwrapIfNeeded(account.Type == AccountTypeOAuth, body), resp.Header, actualURL, model), resp
}

// Native media generation uses separate budgets and recovery contracts.
func geminiSupplierMediaRequestGeili(body []byte) bool {
	for _, path := range []string{"generationConfig.responseModalities", "generation_config.response_modalities", "request.generationConfig.responseModalities"} {
		for _, modality := range gjson.GetBytes(body, path).Array() {
			if strings.EqualFold(modality.String(), "IMAGE") || strings.EqualFold(modality.String(), "AUDIO") || strings.EqualFold(modality.String(), "VIDEO") {
				return true
			}
		}
	}
	return false
}

const (
	geminiActualUpstreamURLKeyGeili       = "geili_gemini_actual_upstream_url"
	geminiActualUpstreamRequestIDKeyGeili = "geili_gemini_actual_upstream_request_id"
)

// Capture the selected HTTP request rather than inferring a platform endpoint.
// Only the credential-free URL is retained, and each dispatch resets response attribution.
func SetGeminiActualUpstreamURLGeili(c *gin.Context, rawURL string) {
	if c == nil {
		return
	}
	c.Set(geminiActualUpstreamURLKeyGeili, safeGeminiUpstreamURLGeili(rawURL))
	c.Set(geminiActualUpstreamRequestIDKeyGeili, "")
}

func SetGeminiUpstreamResponseHeadersGeili(c *gin.Context, headers http.Header) {
	if c == nil {
		return
	}
	requestID := headers.Get("x-request-id")
	if requestID == "" {
		requestID = headers.Get("x-goog-request-id")
	}
	c.Set(geminiActualUpstreamRequestIDKeyGeili, strings.TrimSpace(requestID))
}

// Handlers reuse a Gin context when switching accounts. Clear both fields at
// selection time so a pre-dispatch failure cannot inherit another account's URL.
func ClearGeminiActualUpstreamGeili(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(geminiActualUpstreamURLKeyGeili, "")
	c.Set(geminiActualUpstreamRequestIDKeyGeili, "")
}

// safeUpstreamURL historically strips query/fragment only. Gemini attribution
// also removes URL userinfo so a configured base URL cannot expose credentials.
func safeGeminiUpstreamURLGeili(rawURL string) string {
	parsed, err := url.Parse(safeUpstreamURL(rawURL))
	if err != nil {
		return ""
	}
	parsed.User = nil
	return parsed.String()
}
