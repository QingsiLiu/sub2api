package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// explicitKeyRouting runs after authentication. The selected subscription is
// immutable across attempts; each attempt receives its own group/key/context.
func explicitKeyRouting(keys *service.APIKeyService, resolver *service.CompositeRouteResolver, h *handler.Handlers, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestStarted := requestMemoryIngressTimeGeili(c.Request) // geili hook: include admission preread time in the existing deadline.
		// geili hook: mark every HTTP gateway request before legacy-key early
		// returns. Only the three text POST routes can trigger account probes.
		accountRecoveryText := isRecoveryTextRequest(c.Request) && !strings.HasSuffix(strings.TrimRight(c.Request.URL.Path, "/"), "/responses/compact")
		c.Request = c.Request.WithContext(service.WithHTTPAccountRecoveryScopeGeili(c.Request.Context(), accountRecoveryText))
		if accountRecoveryText {
			// Reuse the existing decoded PrereadBody fast path: legacy keys
			// need the same media-output scope check before their early return.
			body, readErr := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
			if readErr == nil {
				requestmodel.ResetRequestBody(c.Request, body)
				accountRecoveryText = service.ExplicitKeyStoppedTextRecoveryAllowedGeili(c.Request.Context(), body)
			} else {
				// Keep the original error available to the existing protocol handler.
				c.Request.Body = &accountRecoveryBodyReadErrorGeili{original: c.Request.Body, err: readErr}
				accountRecoveryText = false
			}
			c.Request = c.Request.WithContext(service.WithHTTPAccountRecoveryScopeGeili(c.Request.Context(), accountRecoveryText))
		}
		// geili: one recovery state survives account and selected-group attempts,
		// including legacy keys. Media/task endpoints keep their replay contract.
		recoveryTextRequest := isRecoveryTextRequest(c.Request)
		// geili hook: native endpoints get only a request-local exclusion set.
		// Do not preread native bodies, enable stopped-account probes or alter budgets.
		if c.Request.Method == http.MethodPost && strings.Contains(c.Request.URL.Path, "/v1beta/models/") &&
			(strings.HasSuffix(c.Request.URL.Path, ":generateContent") || strings.HasSuffix(c.Request.URL.Path, ":streamGenerateContent")) {
			c.Request = c.Request.WithContext(service.WithGeminiSupplierRecoveryGeili(c.Request.Context()))
		}
		if recoveryTextRequest {
			budget := 600 * time.Second
			if cfg != nil {
				budget = cfg.Gateway.RequestRecoveryBudget()
			}
			c.Request = c.Request.WithContext(service.WithRequestRecovery(c.Request.Context(), budget))
		}
		key, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || key == nil || key.BillingSource == "" {
			c.Next()
			return
		}
		ctx := service.WithKeyRequestRPM(c.Request.Context())
		rootContext := ctx
		if key.UsesGroupListRouting() {
			budget := 600 * time.Second
			if cfg != nil {
				budget = cfg.Gateway.KeyGroupRequestBudget(false)
			}
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, requestStarted.Add(budget))
			defer cancel()
		}
		c.Request = c.Request.WithContext(ctx)
		forcedPlatform, _ := middleware.GetForcePlatformFromContext(c)
		bindSingle := func() bool {
			group, err := keys.ExplicitSingleGroup(c.Request.Context(), key)
			if err != nil {
				writeCompositeRouteError(c, err)
				return false
			}
			if forcedPlatform != "" && group.Platform != forcedPlatform {
				writeCompositeRouteError(c, service.ErrGroupNotAllowed)
				return false
			}
			if strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
				models, err := keys.ExplicitKeyModels(c.Request.Context(), key, resolver, service.CompositeRouteEndpointResponses, forcedPlatform)
				if err != nil {
					writeCompositeRouteError(c, err)
					return false
				}
				scoped := *group
				scoped.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: models}
				group = &scoped
			}
			bindExplicitAttempt(c, key, service.CompositeRouteDecision{Source: "key_groups", TargetGroup: group, TargetGroupID: &group.ID, TargetPlatform: group.Platform}, nil)
			return true
		}
		path := strings.TrimRight(c.Request.URL.Path, "/")
		// Native task handlers authorize the persisted user/key owner themselves.
		// No model/account/group is reselected for an already accepted task.
		if ownedAUAPIMediaLookup(c) {
			c.Next()
			return
		}
		if c.Request.Method == http.MethodGet && (strings.HasSuffix(path, "/models") || strings.Contains(path, "/models/")) {
			models, err := keys.ExplicitKeyModels(c.Request.Context(), key, resolver, compositeRouteEndpointForPath(path), forcedPlatform)
			if err != nil {
				writeCompositeRouteError(c, err)
				return
			}
			if c.Query("client_version") != "" {
				body, err := service.BuildCodexModelsManifest(models)
				if err != nil {
					writeCompositeRouteError(c, err)
					return
				}
				etag := service.CodexModelsManifestETag(body)
				c.Header("ETag", etag)
				if service.CodexModelsManifestETagMatches(c.GetHeader("If-None-Match"), etag) {
					c.Status(http.StatusNotModified)
				} else {
					c.Data(http.StatusOK, "application/json", body)
				}
				c.Abort()
				return
			}
			if strings.Contains(path, "v1beta") {
				list := make([]gin.H, 0, len(models))
				for _, model := range models {
					list = append(list, gin.H{"name": "models/" + model, "displayName": model, "supportedGenerationMethods": []string{"generateContent", "countTokens"}})
				}
				if requested := c.Param("model"); requested != "" {
					for _, item := range list {
						if item["name"] == "models/"+requested {
							c.JSON(200, item)
							c.Abort()
							return
						}
					}
					writeCompositeRouteError(c, infraerrors.NotFound("MODEL_NOT_AVAILABLE", "model is not available in the selected groups"))
					return
				}
				c.JSON(200, gin.H{"models": list})
				c.Abort()
				return
			}
			list := make([]gin.H, 0, len(models))
			for _, model := range models {
				list = append(list, gin.H{"id": model, "object": "model", "owned_by": "selected-groups"})
			}
			if requested := c.Param("model"); requested != "" {
				for _, m := range list {
					if m["id"] == requested {
						c.JSON(200, m)
						c.Abort()
						return
					}
				}
				writeCompositeRouteError(c, infraerrors.NotFound("MODEL_NOT_AVAILABLE", "model is not available in the selected groups"))
				return
			}
			c.JSON(200, gin.H{"object": "list", "data": list})
			c.Abort()
			return
		}
		if c.Request.Method != http.MethodPost {
			if !bindSingle() {
				return
			}
			c.Next()
			return
		}
		body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			writeCompositeRouteError(c, infraerrors.BadRequest("INVALID_REQUEST_BODY", "unable to read request body"))
			return
		}
		requestmodel.ResetRequestBody(c.Request, body)
		model := requestmodel.FromBodyForRoute(c.FullPath(), c.GetHeader("Content-Type"), body)
		for _, candidate := range requestmodel.FromBodyCandidates(c.FullPath(), c.GetHeader("Content-Type"), body) {
			if candidate != model {
				writeCompositeRouteError(c, infraerrors.BadRequest("AMBIGUOUS_MODEL", "conflicting model fields are not allowed"))
				return
			}
		}
		if strings.Contains(path, "/v1beta/models/") {
			model = compositeGeminiModelFromParams(c)
		}
		// geili hook: cache creation uses Google's resource-style model name.
		// Authorize its bare public ID without changing the original payload.
		if compositeRouteEndpointForPath(path) == service.CompositeRouteEndpointGemini && strings.HasSuffix(path, "/cachedContents") {
			model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
		}
		if key.UsesGroupListRouting() && recoveryTextRequest && service.IsLongThinkingRequestModel(model) {
			gatewayConfig := config.GatewayConfig{}
			if cfg != nil {
				gatewayConfig = cfg.Gateway
			}
			// Base the larger default on ingress time, so body parsing does not
			// replenish the total budget. Parent/client deadlines remain shorter.
			if extended := gatewayConfig.KeyGroupRequestBudget(true); extended != gatewayConfig.KeyGroupRequestBudget(false) {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(rootContext, requestStarted.Add(extended))
				defer cancel()
				c.Request = c.Request.WithContext(ctx)
			}
		}
		if model == "" {
			if explicitGatewayHandler(h, path) != nil {
				writeCompositeRouteError(c, infraerrors.BadRequest("MODEL_REQUIRED", "model is required"))
				return
			}
			if !bindSingle() {
				return
			}
			c.Next()
			return
		}
		// geili hook: routing rejections must retain client intent before handler dispatch.
		service.SetOpsIngressRequestContext(c, model, gjson.GetBytes(body, "stream").Bool() || strings.HasSuffix(path, ":streamGenerateContent"))
		// geili hook: request-only declarations can reach recovery; catalog listing stays unchanged.
		allowStoppedText := recoveryTextRequest && !strings.HasSuffix(strings.TrimRight(path, "/"), "/responses/compact") && service.ExplicitKeyStoppedTextRecoveryAllowedGeili(c.Request.Context(), body)
		candidates, err := keys.ResolveExplicitKeyRoutes(c.Request.Context(), key, model, compositeRouteEndpointForPath(path), resolver, allowStoppedText)
		if err != nil {
			writeCompositeRouteError(c, err)
			return
		}
		if forcedPlatform != "" {
			filtered := candidates[:0]
			for _, decision := range candidates {
				if decision.TargetPlatform == forcedPlatform {
					filtered = append(filtered, decision)
				}
			}
			candidates = filtered
			if len(candidates) == 0 {
				writeCompositeRouteError(c, service.ErrGroupNotAllowed)
				return
			}
		}
		if key.UsesGroupListRouting() && recoveryTextRequest {
			for _, decision := range candidates {
				if resolver.LongThinkingRoute(c.Request.Context(), decision) {
					// A public alias may hide Opus/Astra. Classify only its frozen,
					// filtered routes before recovery freezes the root deadline.
					var cancel context.CancelFunc
					ctx, cancel = longThinkingKeyRouteContext(rootContext, requestStarted, cfg)
					defer cancel()
					c.Request = c.Request.WithContext(ctx)
					break
				}
			}
		}
		// geili hook: route ownership crosses candidate groups but never Key permissions.
		authorizedCandidates := append([]service.CompositeRouteDecision(nil), candidates...)
		hasOpenAIRoute := false
		for _, candidate := range candidates {
			hasOpenAIRoute = hasOpenAIRoute || candidate.TargetPlatform == service.PlatformOpenAI
		}
		if hasOpenAIRoute && (strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/responses/compact")) {
			owner, routeErr := h.OpenAIGateway.PrepareOpenAIEncryptedRouteGeili(c, key, body)
			if routeErr != nil {
				h.OpenAIGateway.WriteOpenAIEncryptedRouteErrorGeili(c, routeErr)
				return
			}
			if owner != nil {
				var routeErr error
				body, candidates, routeErr = recoverUnavailableEncryptedRouteGeili(c.Request.Context(), body, candidates, authorizedCandidates, owner)
				if routeErr != nil {
					h.OpenAIGateway.WriteOpenAIEncryptedRouteErrorGeili(c, routeErr)
					return
				}
				requestmodel.ResetRequestBody(c.Request, body)
			}
		}
		next := explicitGatewayHandler(h, path)
		if next == nil {
			// Async/task/other specialized handlers keep their existing transaction and
			// idempotency contract. Select once and never replay their side effects.
			bindExplicitAttempt(c, key, candidates[0], nil)
			c.Next()
			return
		}
		c.Abort()
		attempts := []service.KeyRouteAttempt{}
		for index := 0; index < len(candidates); index++ {
			decision := candidates[index]
			if err := c.Request.Context().Err(); err != nil {
				writeKeyRouteTimeout(c, "REQUEST_TIMEOUT", "request timeout budget exhausted")
				return
			}
			if !service.RequestRecoveryAllowed(c.Request.Context()) {
				writeKeyRouteTimeout(c, "UPSTREAM_RECOVERY_EXHAUSTED", "upstream recovery timeout budget exhausted")
				return
			}
			attempt := handler.CopyContextForAttempt(c) // geili hook: 与 Cloudflare 保活心跳互斥
			attemptContext, cancelAttempt := service.RequestRecoveryContext(c.Request.Context())
			defer cancelAttempt()
			attempt.Request = c.Request.Clone(attemptContext)
			requestmodel.ResetRequestBody(attempt.Request, body)
			writer := newKeyRouteWriter(c.Writer)
			writer.ctx = attempt
			attempt.Writer = writer
			history := append(append([]service.KeyRouteAttempt(nil), attempts...), service.KeyRouteAttempt{GroupID: *decision.TargetGroupID, Reason: "selected"})
			bindExplicitAttempt(attempt, key, decision, history)
			next(attempt)
			writer.upstreamFailure = attempt.GetBool("geili_upstream_failure_final")
			if service.OpenAIEncryptedRoutePinnedGeili(c.Request.Context()) && key.UsesGroupListRouting() && writer.canRetry() {
				if recovered, ok := service.ReleaseOpenAIEncryptedReasoningRouteGeili(c.Request.Context(), body); ok {
					body = recovered
					for _, alternative := range authorizedCandidates {
						if alternative.TargetGroupID != nil && *alternative.TargetGroupID != *decision.TargetGroupID {
							candidates = append(candidates, alternative)
						}
					}
				}
			}
			retry := !service.OpenAIEncryptedRoutePinnedGeili(c.Request.Context()) && key.UsesGroupListRouting() && index+1 < len(candidates) && writer.canRetry() && service.BeginRequestRecovery(c.Request.Context())
			if retry {
				// geili hook: retain upstream diagnostics when an authorized group takes over.
				handler.CarryGeminiSupplierFailureContextGeili(c, attempt)
				attempts = append(attempts, service.KeyRouteAttempt{GroupID: *decision.TargetGroupID, Reason: http.StatusText(writer.Status())})
				continue
			}
			writer.commit()
			c.Keys = attempt.Keys
			c.Request = attempt.Request
			c.Errors = append(c.Errors, attempt.Errors...)
			return
		}
	}
}

func longThinkingKeyRouteContext(root context.Context, started time.Time, cfg *config.Config) (context.Context, context.CancelFunc) {
	gatewayConfig := config.GatewayConfig{}
	if cfg != nil {
		gatewayConfig = cfg.Gateway
	}
	return context.WithDeadline(root, started.Add(gatewayConfig.KeyGroupRequestBudget(true)))
}

func writeKeyRouteTimeout(c *gin.Context, code, message string) {
	body := gin.H{"error": gin.H{"type": "upstream_error", "code": code, "message": message}}
	if c.Writer.Written() && strings.Contains(c.Writer.Header().Get("Content-Type"), "text/event-stream") {
		encoded, _ := json.Marshal(body)
		handler.WriteTextRecoveryFailure(c, http.StatusGatewayTimeout, encoded)
		return
	}
	c.JSON(http.StatusGatewayTimeout, body)
}

func isRecoveryTextRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost || strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	path := strings.TrimRight(r.URL.Path, "/")
	return strings.HasSuffix(path, "/messages") || strings.HasSuffix(path, "/chat/completions") ||
		strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/responses/compact")
}

func bindExplicitAttempt(c *gin.Context, original *service.APIKey, decision service.CompositeRouteDecision, attempts []service.KeyRouteAttempt) {
	key := *original
	group := *decision.TargetGroup
	key.Group = &group
	key.GroupID = &group.ID
	key.CompositeRoute = &decision
	key.RouteAttempts = attempts
	if original.User != nil {
		owner := *original.User
		owner.UserGroupRPMOverride = nil
		key.User = &owner
	}
	c.Set(string(middleware.ContextKeyAPIKey), &key)
	ctx := service.WithCompositeRouteDecision(c.Request.Context(), decision)
	c.Request = c.Request.WithContext(context.WithValue(ctx, ctxkey.Group, &group))
}

func explicitGatewayHandler(h *handler.Handlers, path string) gin.HandlerFunc {
	openAICompatible := func(c *gin.Context) bool {
		return domain.UsesOpenAIGateway(getGroupPlatform(c))
	}
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		return func(c *gin.Context) {
			if openAICompatible(c) {
				h.OpenAIGateway.ChatCompletions(c)
			} else {
				h.Gateway.ChatCompletions(c)
			}
		}
	case strings.HasSuffix(path, "/messages/count_tokens"):
		return func(c *gin.Context) {
			if getGroupPlatform(c) == service.PlatformGrok {
				h.OpenAIGateway.GrokCountTokens(c)
			} else if openAICompatible(c) {
				h.OpenAIGateway.CountTokens(c)
			} else {
				h.Gateway.CountTokens(c)
			}
		}
	case strings.HasSuffix(path, "/messages"):
		return func(c *gin.Context) {
			if openAICompatible(c) {
				h.OpenAIGateway.Messages(c)
			} else {
				h.Gateway.Messages(c)
			}
		}
	case strings.HasSuffix(path, "/responses/input_tokens"):
		return h.OpenAIGateway.ResponsesInputTokens
	case strings.HasSuffix(path, "/responses"), strings.HasSuffix(path, "/responses/compact"):
		return func(c *gin.Context) {
			if openAICompatible(c) {
				h.OpenAIGateway.Responses(c)
			} else {
				h.Gateway.Responses(c)
			}
		}
	case strings.HasSuffix(path, "/embeddings"):
		return h.OpenAIGateway.Embeddings
	case strings.HasSuffix(path, "/images/generations"), strings.HasSuffix(path, "/images/edits"):
		return func(c *gin.Context) {
			if getGroupPlatform(c) == service.PlatformGrok {
				h.OpenAIGateway.GrokImages(c)
			} else {
				h.OpenAIGateway.Images(c)
			}
		}
	case strings.Contains(path, "/v1beta/models/"):
		return h.Gateway.GeminiV1BetaModels
	default:
		return nil
	}
}

// Buffer only rejected responses. Successful and streaming responses are
// forwarded immediately; after a flush/write no cross-group replay is possible.
type keyRouteWriter struct {
	gin.ResponseWriter
	headers          http.Header
	status           int
	size             int
	written          bool
	committed        bool
	upstreamFailure  bool // Set only by the final typed upstream failure, never inferred from its code.
	heartbeatWritten bool
	ctx              *gin.Context
	rejected         bytes.Buffer
}

func newKeyRouteWriter(parent gin.ResponseWriter) *keyRouteWriter {
	return &keyRouteWriter{ResponseWriter: parent, headers: parent.Header().Clone(), status: http.StatusOK, size: -1,
		heartbeatWritten: parent.Written() && strings.Contains(parent.Header().Get("Content-Type"), "text/event-stream")}
}
func (w *keyRouteWriter) Header() http.Header {
	if w.committed {
		return w.ResponseWriter.Header()
	}
	return w.headers
}
func (w *keyRouteWriter) WriteHeader(code int) {
	if w.committed || w.written {
		return
	}
	w.status = code
}
func (w *keyRouteWriter) WriteHeaderNow() {
	if w.written {
		return
	}
	w.written = true
	w.size = 0
}
func (w *keyRouteWriter) Status() int   { return w.status }
func (w *keyRouteWriter) Size() int     { return w.size }
func (w *keyRouteWriter) Written() bool { return w.written || w.committed }
func (w *keyRouteWriter) Write(data []byte) (int, error) {
	// Transport heartbeats keep the Python connection alive without committing
	// an account/group identity or preventing a later group from taking over.
	if !w.committed && w.status < 400 && strings.Contains(w.headers.Get("Content-Type"), "text/event-stream") && keyRouteNeutralSSE(data) {
		for _, name := range []string{"Content-Type", "Cache-Control", "X-Accel-Buffering"} {
			if value := w.headers.Get(name); value != "" {
				w.ResponseWriter.Header().Set(name, value)
			}
		}
		w.heartbeatWritten = true
		return w.ResponseWriter.Write(data)
	}
	w.WriteHeaderNow()
	if w.status >= 400 && !w.committed {
		// Error messages from the gateway are bounded; limit fallback buffering too.
		if w.rejected.Len()+len(data) > 256*1024 {
			w.commit()
			n, err := w.ResponseWriter.Write(data)
			w.size += n
			return n, err
		}
		n, err := w.rejected.Write(data)
		w.size += n
		return n, err
	}
	w.commit()
	n, err := w.ResponseWriter.Write(data)
	w.size += n
	return n, err
}
func (w *keyRouteWriter) WriteString(data string) (int, error) { return w.Write([]byte(data)) }
func (w *keyRouteWriter) Flush() {
	if w.status >= 400 && !w.committed {
		return
	}
	if w.heartbeatWritten && !w.committed {
		w.ResponseWriter.Flush()
		return
	}
	w.commit()
	w.ResponseWriter.Flush()
}
func (w *keyRouteWriter) commit() {
	if w.committed {
		return
	}
	w.committed = true
	if w.heartbeatWritten && w.status >= 400 {
		// HTTP 200 is already on the wire. Emit the final error using the
		// requested streaming protocol, including on non-Cloudflare ingress.
		if w.ctx != nil {
			original := w.ctx.Writer
			w.ctx.Writer = w.ResponseWriter
			// Any committed marker refers to this still-buffered JSON rejection,
			// not bytes delivered after the heartbeat. Convert it exactly once.
			w.ctx.Set(service.ResponseCommittedKey, false)
			handler.WriteTextRecoveryFailure(w.ctx, w.status, w.rejected.Bytes())
			w.ctx.Writer = original
		} else {
			_, _ = w.ResponseWriter.WriteString("event: error\ndata: " + w.rejected.String() + "\n\n")
			w.ResponseWriter.Flush()
		}
		return
	}
	headers := w.ResponseWriter.Header()
	for k := range headers {
		delete(headers, k)
	}
	for k, v := range w.headers {
		headers[k] = append([]string(nil), v...)
	}
	w.ResponseWriter.WriteHeader(w.status)
	if w.rejected.Len() > 0 {
		_, _ = w.ResponseWriter.Write(w.rejected.Bytes())
	}
}

func keyRouteNeutralSSE(data []byte) bool {
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ":") || line == "event: ping" || line == "event: keepalive" {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			kind := gjson.Get(strings.TrimSpace(strings.TrimPrefix(line, "data:")), "type").String()
			if kind == "ping" || kind == "keepalive" {
				continue
			}
		}
		return false
	}
	return true
}
func (w *keyRouteWriter) canRetry() bool {
	if w.committed {
		return false
	}
	if w.upstreamFailure && w.status == http.StatusTooManyRequests {
		return true
	}
	body := w.rejected.Bytes()
	code := strings.ToLower(gjson.GetBytes(body, "error.code").String())
	kind := strings.ToLower(gjson.GetBytes(body, "error.type").String())
	for _, blocked := range []string{"subscription", "quota", "balance", "billing", "invalid_request", "permission", "rpm", "api_key"} {
		if strings.Contains(code, blocked) || strings.Contains(kind, blocked) {
			return false
		}
	}
	switch w.status {
	case 502, 503, 504:
		return true
	case 429:
		return kind == "upstream_error" || kind == "overloaded_error" || code == "no_available_accounts" || code == "upstream_rate_limited" ||
			isUpstreamSideRateLimit(kind, code, gjson.GetBytes(body, "error.message").String())
	default:
		return false
	}
}

// Gateway handlers map an exhausted upstream 429 to a generic rate_limit_error
// without a code. Only these upstream-side messages may try the next group;
// local concurrency/RPM/quota 429s carry a code and remain final.
var upstreamSideRateLimitMessages = map[string]bool{
	"Upstream rate limit exceeded, please retry later":                       true,
	"Upstream rate limit exceeded":                                           true,
	"All available accounts are currently rate-limited. Please retry later.": true,
}

func isUpstreamSideRateLimit(kind, code, message string) bool {
	return kind == "rate_limit_error" && code == "" && upstreamSideRateLimitMessages[strings.TrimSpace(message)]
}

// Restrict model-free composite-key lookups to native task endpoints. Other
// model-free requests retain ExplicitSingleGroup and cannot reach a global pool.
func ownedAUAPIMediaLookup(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet {
		return false
	}
	id, prefix := "", ""
	switch c.FullPath() {
	case "/v1/images/tasks/:task_id":
		id, prefix = c.Param("task_id"), "auimgtask_"
	case "/v1/videos/:request_id", "/v1/videos/:request_id/content",
		"/v1/videos/generations/:request_id", "/v1/videos/generations/:request_id/content",
		"/videos/:request_id", "/videos/:request_id/content",
		"/videos/generations/:request_id", "/videos/generations/:request_id/content":
		id, prefix = c.Param("request_id"), "auvidtask_"
	default:
		return false
	}
	if !strings.HasPrefix(id, prefix) || len(id) != len(prefix)+32 {
		return false
	}
	for _, char := range id[len(prefix):] {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

// recoverUnavailableEncryptedRouteGeili handles a source group absent from the
// live catalog. The frozen alternatives were already filtered by Key permission
// and forced platform; never rebuild them from an unfiltered catalog here.
func recoverUnavailableEncryptedRouteGeili(ctx context.Context, body []byte, candidates, authorizedCandidates []service.CompositeRouteDecision, owner *service.OpenAIEncryptedRouteGeili) ([]byte, []service.CompositeRouteDecision, error) {
	filtered := service.FilterOpenAIEncryptedRoutesGeili(candidates, owner)
	if len(filtered) > 0 {
		return body, filtered, nil
	}
	if len(authorizedCandidates) > 0 {
		if recovered, ok := service.ReleaseOpenAIEncryptedReasoningRouteGeili(ctx, body); ok {
			return recovered, append([]service.CompositeRouteDecision(nil), authorizedCandidates...), nil
		}
	}
	return body, nil, &service.OpenAIEncryptedRouteErrorGeili{Status: http.StatusConflict, Code: "encrypted_session_route_conflict", Message: "原会话线路已不在此 Key 的可用渠道中，请恢复原线路或联系技术支持保留会话进度"}
}

// A failed preread disables probing while preserving the normal handler's
// body-error path, including MaxBytesError and decompression failures.
type accountRecoveryBodyReadErrorGeili struct {
	original io.ReadCloser
	err      error
}

func (r *accountRecoveryBodyReadErrorGeili) Read([]byte) (int, error) { return 0, r.err }
func (r *accountRecoveryBodyReadErrorGeili) Close() error {
	if r.original != nil {
		return r.original.Close()
	}
	return nil
}
