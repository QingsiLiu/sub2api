package service

// A relay project that lost access to a service tier (for example priority)
// rejects it with a 400 naming param=service_tier. The request is valid on
// other accounts, so it fails over within the request; account state is not
// changed. When every account rejects it the native 400 is returned unchanged.
import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// isOpenAIServiceTierNotAllowedGeili matches only the structured error field.
func isOpenAIServiceTierNotAllowedGeili(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest || !gjson.ValidBytes(body) {
		return false
	}
	root := gjson.ParseBytes(body)
	if !strings.EqualFold(strings.TrimSpace(root.Get("error.param").String()), "service_tier") {
		return false
	}
	message := root.Get("error.message")
	if message.Type != gjson.String || len(message.String()) > 1024 {
		return false
	}
	lower := strings.ToLower(message.String())
	return strings.Contains(lower, "service tier is not allowed") || strings.Contains(lower, "service_tier is not allowed")
}

// WriteOpenAIServiceTierExhaustedGeili writes the native 400 the client saw
// before this failover existed, once every account rejected the tier.
func WriteOpenAIServiceTierExhaustedGeili(c *gin.Context, failoverErr *UpstreamFailoverError, streamStarted bool) bool {
	if c == nil || failoverErr == nil || streamStarted || c.Writer.Written() ||
		!isOpenAIServiceTierNotAllowedGeili(failoverErr.StatusCode, failoverErr.ResponseBody) {
		return false
	}
	upstreamMsg := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(failoverErr.ResponseBody))
	SetOpsUpstreamError(c, failoverErr.StatusCode, upstreamMsg, "")
	writeOpenAIUpstreamClientError(c, failoverErr.StatusCode, failoverErr.ResponseBody, upstreamMsg)
	return true
}
