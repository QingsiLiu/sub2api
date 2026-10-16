package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const geminiSupplierUnavailableCodeGeili = "upstream_unavailable"
const geminiSupplierUnavailableMessageGeili = "Upstream resources are temporarily unavailable"

// A proven supplier rejection owns a 503; its original status remains in the
// upstream events. It must not be exposed as a customer billing/parameter error.
func (h *GatewayHandler) writeGeminiSupplierUnavailableGeili(c *gin.Context, failure *service.UpstreamFailoverError, streamStarted bool) bool {
	if !service.IsGeminiSupplierUnavailableGeili(failure) && !(failure == nil && service.GeminiSupplierFailuresSeenGeili(c.Request.Context())) {
		return false
	}
	if service.IsResponseCommitted(c) || requestRecoveryStopped(c) {
		return true
	}
	c.Set("geili_upstream_failure_final", true)
	streamStarted = upstreamFailureStreamStarted(c, streamStarted)
	errorValue := gin.H{"type": "upstream_error", "code": geminiSupplierUnavailableCodeGeili, "message": geminiSupplierUnavailableMessageGeili}
	body := gin.H{"error": errorValue}
	endpoint := GetInboundEndpoint(c)
	if endpoint == EndpointGeminiModels {
		body = gin.H{"error": gin.H{"code": http.StatusServiceUnavailable, "status": "UNAVAILABLE", "message": geminiSupplierUnavailableMessageGeili,
			"details": []gin.H{{"reason": geminiSupplierUnavailableCodeGeili}}}}
	}
	if !streamStarted {
		c.JSON(http.StatusServiceUnavailable, body)
		return true
	}
	service.MarkOpsStreamFailure(c, "upstream_error", geminiSupplierUnavailableCodeGeili, geminiSupplierUnavailableMessageGeili, http.StatusServiceUnavailable)
	if endpoint == EndpointResponses {
		writeResponsesFailedSSE(c, "upstream_error", geminiSupplierUnavailableCodeGeili, geminiSupplierUnavailableMessageGeili)
	} else {
		prefix := "data: "
		if endpoint == EndpointMessages {
			prefix = "event: error\ndata: "
			body["type"] = "error"
			errorValue["type"] = "api_error"
		}
		encoded, _ := json.Marshal(body)
		_, _ = c.Writer.WriteString(prefix + string(encoded) + "\n\n")
		c.Writer.Flush()
	}
	service.MarkResponseCommitted(c)
	return true
}

func (h *GatewayHandler) geminiSupplierSelectionFailureGeili(c *gin.Context, err error, last *service.UpstreamFailoverError, streamStarted bool) bool {
	return errors.Is(err, service.ErrNoAvailableAccounts) && !strings.Contains(err.Error(), "pricing restriction") &&
		h.writeGeminiSupplierUnavailableGeili(c, last, streamStarted)
}

// Carry only diagnostic context between rejected groups. Local admission and
// response-commit flags must never leak into the next authorized attempt.
func CarryGeminiSupplierFailureContextGeili(dst, src *gin.Context) {
	if dst == nil || src == nil || src.Request == nil || !service.GeminiSupplierFailuresSeenGeili(src.Request.Context()) {
		return
	}
	for _, key := range []string{service.OpsUpstreamStatusCodeKey, service.OpsUpstreamErrorMessageKey, service.OpsUpstreamErrorDetailKey,
		service.OpsUpstreamModelKey, opsAccountIDKey, ctxKeyActualUpstreamEndpoint} {
		if value, ok := src.Get(key); ok {
			dst.Set(key, value)
		}
	}
	if value, ok := src.Get(service.OpsUpstreamErrorsKey); ok {
		if events, ok := value.([]*service.OpsUpstreamErrorEvent); ok {
			dst.Set(service.OpsUpstreamErrorsKey, append([]*service.OpsUpstreamErrorEvent(nil), events...))
		}
	}
	service.SetActualOpenAIUpstreamEndpoint(dst, service.GetActualOpenAIUpstreamEndpoint(src))
}
