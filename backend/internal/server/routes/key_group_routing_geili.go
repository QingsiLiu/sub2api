package routes

import (
	"bytes"
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"net/http"
	"strings"
	"time"

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
		key, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || key == nil || key.BillingSource == "" {
			c.Next()
			return
		}
		ctx := service.WithKeyRequestRPM(c.Request.Context())
		if key.UsesGroupListRouting() {
			budget := 600 * time.Second
			if cfg != nil && cfg.Gateway.KeyGroupRequestTimeoutSeconds > 0 {
				budget = time.Duration(cfg.Gateway.KeyGroupRequestTimeoutSeconds) * time.Second
			}
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, budget)
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
		candidates, err := keys.ResolveExplicitKeyRoutes(c.Request.Context(), key, model, compositeRouteEndpointForPath(path), resolver)
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
		for index, decision := range candidates {
			if err := c.Request.Context().Err(); err != nil {
				c.JSON(http.StatusGatewayTimeout, gin.H{"error": gin.H{"code": "REQUEST_TIMEOUT", "message": "request timeout budget exhausted"}})
				return
			}
			attempt := c.Copy()
			attempt.Request = c.Request.Clone(c.Request.Context())
			requestmodel.ResetRequestBody(attempt.Request, body)
			writer := newKeyRouteWriter(c.Writer)
			attempt.Writer = writer
			history := append(append([]service.KeyRouteAttempt(nil), attempts...), service.KeyRouteAttempt{GroupID: *decision.TargetGroupID, Reason: "selected"})
			bindExplicitAttempt(attempt, key, decision, history)
			next(attempt)
			retry := key.UsesGroupListRouting() && index+1 < len(candidates) && writer.canRetry()
			if retry {
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
		switch getGroupPlatform(c) {
		case service.PlatformOpenAI, service.PlatformGrok, service.PlatformKimi, service.PlatformZhipu, service.PlatformDeepseek, service.PlatformMiniMax, service.PlatformOpenCodeGo:
			return true
		}
		return false
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
	headers   http.Header
	status    int
	size      int
	written   bool
	committed bool
	rejected  bytes.Buffer
}

func newKeyRouteWriter(parent gin.ResponseWriter) *keyRouteWriter {
	return &keyRouteWriter{ResponseWriter: parent, headers: parent.Header().Clone(), status: http.StatusOK, size: -1}
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
	w.commit()
	w.ResponseWriter.Flush()
}
func (w *keyRouteWriter) commit() {
	if w.committed {
		return
	}
	w.committed = true
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
func (w *keyRouteWriter) canRetry() bool {
	if w.committed {
		return false
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
		return kind == "upstream_error" || kind == "overloaded_error" || code == "no_available_accounts" || code == "upstream_rate_limited"
	default:
		return false
	}
}
