package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// The ingress transports retain their original context for the lifetime of a
// connection. This carrier switches only the policy snapshot, never the account
// or connection, at each accepted response.create.
type openAIEncryptedWSTurnKeyGeili struct{}
type openAIEncryptedWSTurnGeili struct {
	mu  sync.RWMutex
	ctx context.Context
}

func encryptedWSContextGeili(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	if turn, ok := ctx.Value(openAIEncryptedWSTurnKeyGeili{}).(*openAIEncryptedWSTurnGeili); ok {
		turn.mu.RLock()
		active := turn.ctx
		turn.mu.RUnlock()
		if active != nil {
			return active
		}
	}
	return ctx
}

func observeOpenAIEncryptedWSOutputGeili(ctx context.Context, payload []byte, writeErr error) {
	if writeErr == nil {
		ObserveOpenAIEncryptedOutputGeili(encryptedWSContextGeili(ctx), payload)
	}
}

func normalizeOpenAIEncryptedWSFailureGeili(ctx context.Context, payload []byte) []byte {
	if !openAIEncryptedWSTurnProtectedGeili(ctx) || !ClassifyOpenAIEncryptedFailureGeili(payload) {
		return payload
	}
	kind := gjson.GetBytes(payload, "type").String()
	path := "error.code"
	if kind == "response.failed" {
		path = "response.error.code"
	} else if kind != "error" {
		return payload
	}
	updated, err := sjson.SetBytes(payload, path, "invalid_encrypted_content")
	if err != nil {
		return payload
	}
	return updated
}

func openAIEncryptedWSTurnProtectedGeili(ctx context.Context) bool {
	return OpenAIEncryptedRouteProtectedGeili(encryptedWSContextGeili(ctx))
}

func (s *OpenAIGatewayService) withOpenAIEncryptedWSHooksGeili(ctx context.Context, c *gin.Context, conn *coderws.Conn, account *Account, first []byte, hooks *OpenAIWSIngressHooks) (context.Context, *OpenAIWSIngressHooks, error) {
	key := getAPIKeyFromContext(c)
	if key == nil {
		return ctx, hooks, nil
	}
	carrier := &openAIEncryptedWSTurnGeili{}
	base := ctx
	ctx = context.WithValue(ctx, openAIEncryptedWSTurnKeyGeili{}, carrier)
	copyHooks := OpenAIWSIngressHooks{}
	if hooks != nil {
		copyHooks = *hooks
	}
	mapModel := copyHooks.MapRequestModel
	type mappingResult struct {
		original, mapped string
		err              error
	}
	var mappingMu sync.Mutex
	mappings := map[int]mappingResult{}
	resolveModel := func(turn int, model string) (string, error) {
		mappingMu.Lock()
		defer mappingMu.Unlock()
		if cached, ok := mappings[turn]; ok && cached.original == model {
			return cached.mapped, cached.err
		}
		mapped := model
		var err error
		if mapModel != nil {
			mapped, err = mapModel(turn, model)
		}
		if mapped == "" {
			mapped = model
		}
		for n := range mappings {
			if n < turn-1 {
				delete(mappings, n)
			}
		}
		mappings[turn] = mappingResult{original: model, mapped: mapped, err: err}
		return mapped, err
	}
	prepare := func(turn int, payload []byte, model string, firstTurn bool) error {
		turnCtx := base
		var err error
		if !firstTurn || encryptedRouteStateGeili(base) == nil {
			// Off must not inherit the first frame's previously enabled snapshot.
			fresh := context.WithValue(base, openAIEncryptedRouteContextKeyGeili{}, (*openAIEncryptedRouteStateGeili)(nil))
			turnCtx, _, err = s.PrepareOpenAIEncryptedTurnGeili(fresh, key, payload)
		}
		// HTTP recovery budgets are request scoped; a long-lived WS connection
		// must not consume the next turn's budget through an earlier failure.
		if OpenAIEncryptedRouteProtectedGeili(turnCtx) {
			budget := defaultRequestRecoveryBudget
			if old := requestRecoveryFromContext(base); old != nil {
				old.mu.Lock()
				budget = old.budget
				old.mu.Unlock()
			}
			turnCtx = context.WithValue(turnCtx, requestRecoveryContextKey{}, &requestRecoveryState{budget: budget, now: time.Now})
		}
		if err == nil && OpenAIEncryptedRouteProtectedGeili(turnCtx) {
			if copyHooks.AuthorizeEncryptedTurn != nil {
				err = copyHooks.AuthorizeEncryptedTurn(turnCtx, turn, payload, model)
			}
			if err == nil {
				model, err = resolveModel(turn, model)
			}
		}
		if err == nil {
			turnCtx = WithOpenAIEncryptedOutputOriginGeili(turnCtx, key.GroupID, account, model)
			err = ValidateOpenAIEncryptedAccountGeili(turnCtx, account, model)
		}
		if err != nil {
			// No upstream write or new generation occurs after a source conflict.
			code, message := "encrypted_session_route_conflict", "Encrypted session route changed; reconnect to the original route."
			status := 409
			var routeErr *OpenAIEncryptedRouteErrorGeili
			if errors.As(err, &routeErr) {
				code, message = routeErr.Code, routeErr.Message
				status = routeErr.Status
			} else if errors.Is(err, ErrOpenAIEncryptedRouteUnavailableGeili) {
				code, message, status = "encrypted_session_route_unavailable", "原会话线路暂时不可用，请稍后重试或联系技术支持", 503
			}
			BeginOpsStreamTurn(c, turn)
			MarkOpsStreamErrorValue(c, OpsStreamError{ErrType: "api_error", Code: code, Message: message, IntendedStatus: status, CountTowardsSLA: status >= 500 || code == "invalid_encrypted_content", RequestScoped: true})
			frame, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "code": code, "message": message}})
			writeCtx, cancel := newOpenAIWSDownstreamWriteContext(base, hooks, s.openAIWSWriteTimeout())
			defer cancel()
			writeErr := conn.Write(writeCtx, coderws.MessageText, frame)
			ObserveResponseAuditWSWrite(base, frame, writeErr)
			return NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "encrypted session route conflict; reconnect", err)
		}
		carrier.mu.Lock()
		carrier.ctx = turnCtx
		carrier.mu.Unlock()
		return nil
	}
	model := gjson.GetBytes(first, "model").String()
	if model == "" {
		model = copyHooks.InitialRequestModel
	}
	if err := prepare(1, first, model, true); err != nil {
		return ctx, &copyHooks, err
	}
	copyHooks.MapRequestModel = func(turn int, original string) (string, error) {
		mapped, err := resolveModel(turn, original)
		if err == nil {
			WithOpenAIEncryptedOutputOriginGeili(encryptedWSContextGeili(ctx), key.GroupID, account, mapped)
		}
		return mapped, err
	}
	before := copyHooks.BeforeRequest
	copyHooks.BeforeRequest = func(turn int, payload []byte, model string) error {
		if err := prepare(turn, payload, model, turn == 1); err != nil {
			return err
		}
		if before != nil {
			return before(turn, payload, model)
		}
		return nil
	}
	return ctx, &copyHooks, nil
}

// Restrict recovery to failures before any frame or metered generation was
// observed. Built-in tools can act upstream, so even an undeclared side effect
// is avoided by declining recovery for requests that enable them.
func prepareOpenAIEncryptedWSRecoveryGeili(ctx context.Context, body, failure []byte, wrote bool, usage OpenAIUsage) ([]byte, bool) {
	turnCtx := encryptedWSContextGeili(ctx)
	// A bare error may be followed by response.failed carrying authoritative
	// usage. Never abandon that stream before its billable terminal arrives.
	if gjson.GetBytes(failure, "type").String() == "error" {
		return nil, false
	}
	if gjson.GetBytes(failure, "usage").Exists() || gjson.GetBytes(failure, "response.usage").Exists() {
		return nil, false
	}
	if wrote || turnCtx.Err() != nil || !OpenAIEncryptedRouteRecoveryEnabledGeili(turnCtx) ||
		!ClassifyOpenAIEncryptedFailureGeili(failure) || usage != (OpenAIUsage{}) {
		return nil, false
	}
	payload := openAIRequestPayloadView(body)
	unsafe := false
	payload.Get("tools").ForEach(func(_, tool gjson.Result) bool {
		if tool.Get("type").String() != "function" {
			unsafe = true
		}
		return !unsafe
	})
	if unsafe {
		return nil, false
	}
	next, eligible, _ := PrepareOpenAIEncryptedReasoningRecoveryGeili(body)
	if !eligible || !ClaimOpenAIEncryptedRecoveryGeili(turnCtx) {
		return nil, false
	}
	return next, true
}
